package main

import (
	"context"
	"crypto/tls"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed web/index.html web/drive.js
var files embed.FS

func (a *App) serveUI(w http.ResponseWriter) {
	b, e := fs.ReadFile(files, "web/index.html")
	if e != nil {
		http.Error(w, "UI missing", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; media-src 'self' blob:; img-src 'self' data:; base-uri 'none'; form-action 'none'")
	cloudJS, e := fs.ReadFile(files, "web/drive.js")
	if e != nil {
		http.Error(w, "Cloud UI missing", 500)
		return
	}
	b = []byte(strings.ReplaceAll(string(b), "/*__CLOUD_UI__*/", string(cloudJS)))
	w.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(string(b), "__DESK_TOKEN__", a.s.deskToken), "__APP_VERSION__", appVersion)))
}
func openBrowser(url string) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Edge app mode provides a separate desktop window and taskbar entry,
		// while the native Go background server remains independent of UI.
		for _, candidate := range []string{
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
		} {
			if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
				cmd = exec.Command(candidate, "--app="+url)
				break
			}
		}
		if cmd == nil {
			cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
		}
	} else if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", url)
	} else {
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("Open manually: %s (%v)", url, err)
	}
}
func main() {
	cameraUI := flag.Bool("camera-ui", false, "Open the isolated built-in camera player")
	root := flag.String("data", "", "Data directory (default user configuration directory)")
	nativeUI := flag.Bool("native-ui", true, "Open the native Windows dashboard preview")
	legacyUI := flag.Bool("legacy-ui", false, "Open the previous browser dashboard instead of the native UI")
	noBrowser := flag.Bool("no-browser", false, "Do not open dashboard automatically")
	flag.Parse()
	if *root == "" {
		base, e := os.UserConfigDir()
		if e != nil {
			log.Fatal(e)
		}
		*root = filepath.Join(base, "OneSpace")
	}
	s, err := NewState(*root)
	if err != nil {
		log.Fatal(err)
	}
	if *cameraUI {
		if err := openNativeCamera(newApp(s, "")); err != nil {
			log.Print(err)
			showNativeError(err.Error())
		}
		return
	}
	profileLog, err := configureProfileLog(*root)
	if err != nil {
		log.Fatal("Cannot open diagnostic log: ", err)
	}
	defer profileLog.Close()
	cert, fingerprint, err := loadOrCreateCert(*root)
	if err != nil {
		log.Fatal("Cannot read/create secure device identity: ", err)
	}
	app := newApp(s, fingerprint)
	app.pcCertificate = cert
	localListener, err := net.Listen("tcp", "127.0.0.1:47600")
	if err != nil {
		log.Fatal("Local dashboard port 47600 unavailable: ", err)
	}
	lanListener, err := net.Listen("tcp", ":47601")
	if err != nil {
		log.Fatal("LAN port 47601 unavailable: ", err)
	}
	// Only accept traffic from RFC1918/ULA and localhost addresses: no public Internet exposure.
	lanOnly := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ipStr, _, e := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(ipStr)
		if e != nil || ip == nil || (!ip.IsPrivate() && !ip.IsLoopback() && !app.hub.allowsOverlay(ip)) {
			http.Error(w, "Local network only", 403)
			return
		}
		app.remote(w, r)
	})
	localSrv := &http.Server{Handler: http.HandlerFunc(app.local), ReadHeaderTimeout: 8 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 16}
	lanSrv := &http.Server{Handler: lanOnly, ReadHeaderTimeout: 8 * time.Second, IdleTimeout: 80 * time.Second, MaxHeaderBytes: 1 << 16, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	go func() {
		if e := localSrv.Serve(localListener); e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	}()
	go func() {
		tlsListener := tls.NewListener(lanListener, lanSrv.TLSConfig)
		if e := lanSrv.Serve(tlsListener); e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	}()
	discoveryCtx, cancelDiscovery := context.WithCancel(context.Background())
	go func() {
		if e := serveDiscovery(discoveryCtx, fingerprint); e != nil {
			log.Printf("Optional LAN discovery unavailable: %v", e)
		}
	}()
	clipStop := make(chan struct{})
	go app.startClipboardMonitor(clipStop)
	go app.hub.Run(discoveryCtx)
	go watchNetworkChanges(discoveryCtx, func() {
		select {
		case app.hub.changed <- struct{}{}:
		default:
		}
	})
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for range t.C {
			s.cleanup()
		}
	}()
	fmt.Println("OneSpace Windows desktop running")
	fmt.Println("Dashboard: http://127.0.0.1:47600")
	fmt.Println("LAN endpoint: port 47601 (encrypted)")
	fmt.Println("Fingerprint SHA-256: " + fingerprint)
	uiDone := make(chan struct{})
	if *nativeUI && !*legacyUI && !*noBrowser {
		go func() {
			defer close(uiDone)
			if err := openNativeDashboard(app); err != nil {
				log.Printf("Native dashboard: %v", err)
			}
		}()
	} else if !*noBrowser {
		openBrowser("http://127.0.0.1:47600")
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	select {
	case <-ch:
	case <-uiDone:
	}
	fmt.Println("Stopping OneSpace")
	cancelDiscovery()
	close(clipStop)
	_ = localSrv.Close()
	_ = lanSrv.Close()
}
