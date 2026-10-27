package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	hub "onespace.local/hub"
	"onespace.local/hub/camera"
	"onespace.local/hub/cloud"
	"onespace.local/hub/updates"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: onespace-hub serve|enroll|revoke|fingerprint|status|permissions|drive-connect|drive-disconnect|recording-complete [flags]")
	}
	command := args[0]
	if command != "serve" && command != "enroll" && command != "revoke" && command != "fingerprint" && command != "status" && command != "permissions" && command != "drive-connect" && command != "drive-disconnect" && command != "recording-complete" {
		return errors.New("unknown command")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	data := flags.String("data", "/var/lib/onespace-hub", "owner-only hub data directory")
	listen := flags.String("listen", "127.0.0.1:47603", "explicit trusted LAN/overlay address:port")
	allowPublic := flags.Bool("allow-public", false, "Explicitly allow Internet ingress; deploy firewall and rate limits before use")
	upstream := flags.String("camera-upstream", "http://127.0.0.1:8889/camera/whep", "fixed loopback MediaMTX WHEP endpoint")
	mediaAPI := flags.String("recording-api", "http://127.0.0.1:9997", "fixed loopback MediaMTX API origin")
	clientFile := flags.String("oauth-client", "", "Google OAuth Desktop client JSON for local sign-in")
	oauthPort := flags.Int("oauth-port", 53682, "loopback OAuth callback port")
	spool := flags.String("spool", "/var/lib/onespace-hub/drive/spool/camera", "local recording completion spool")
	id := flags.String("id", "", "stable device id; never replace an existing id")
	name := flags.String("name", "", "device display name")
	kind := flags.String("kind", "", "windows, android, or linux")
	scopes := flags.String("scopes", "", "explicit comma-separated clipboard,media,camera,cloud,recording,connect,updates,updates.publish scopes")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if command == "recording-complete" {
		return cloud.CompleteSegment(*spool, os.Getenv("MTX_SEGMENT_PATH"))
	}
	h, err := hub.Open(*data)
	if err != nil {
		return err
	}
	defer h.Close()
	cert, fingerprint, err := hub.Identity(*data)
	if err != nil {
		return err
	}
	switch command {
	case "drive-connect":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		err := cloud.Connect(ctx, *clientFile, filepath.Join(*data, "drive", "google-credentials.json"), *oauthPort, func(link string) {
			fmt.Println("Open this Google sign-in URL in your browser (do not share it):")
			fmt.Println(link)
		})
		if err == nil {
			fmt.Println("Google Drive connected. Start the hub; use the Cloud page to verify file access.")
		}
		return err
	case "drive-disconnect":
		d, err := cloud.New(filepath.Join(*data, "drive", "google-credentials.json"))
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		return d.Disconnect(ctx)
	case "permissions":
		if err := h.SetScopes(*id, strings.Split(*scopes, ",")); err != nil {
			return err
		}
		fmt.Println("Hub device permissions updated; existing token preserved")
		return nil
	case "enroll":
		token, err := h.Enroll(*id, *name, *kind, strings.Split(*scopes, ","))
		if err != nil {
			return err
		}
		// Deliberate one-time local credential output. Never write this to logs or
		// shared reports; transfer it only through a trusted provisioning channel.
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"id": *id, "name": *name, "token": token, "fingerprint": fingerprint, "version": hub.Version})
	case "revoke":
		if err := h.Revoke(*id); err != nil {
			return err
		}
		fmt.Println("Device revoked")
		return nil
	case "fingerprint":
		fmt.Println(fingerprint)
		return nil
	case "status":
		fmt.Printf("OneSpace Hub %s; Drive recording available as an opt-in; metadata directory opened successfully\n", hub.Version)
		return nil
	case "serve":
		listener, err := net.Listen("tcp", *listen)
		if err != nil {
			return err
		}
		defer listener.Close()
		storage, err := cloud.Open(filepath.Join(*data, "drive"), *mediaAPI)
		if err != nil {
			return err
		}
		storage.Register(h.Mux(), func(r *http.Request, scope string) (string, bool) { d, ok := h.Authorize(r, scope); return d.ID, ok })
		defer storage.Close()

		_, err = updates.Register(h.Mux(), filepath.Join(*data, "updates"), func(r *http.Request, scope string) bool { _, ok := h.Authorize(r, scope); return ok })
		if err != nil {
			return err
		}
		h.SetRecordingStatus(storage.RecordingEnabled)
		gateway, err := camera.Register(h.Mux(), camera.Config{Upstream: *upstream, Recording: storage.RecordingEnabled}, func(r *http.Request) (string, bool) {
			if r.RemoteAddr == "" {
				d, ok := h.CheckPermission(r, "camera")
				return d.ID, ok
			}
			d, ok := h.Authorize(r, "camera")
			return d.ID, ok
		})
		if err != nil {
			return err
		}
		defer gateway.Close()
		server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 70 * time.Second, IdleTimeout: 80 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}}
		errorsCh := make(chan error, 1)
		go func() {
			errorsCh <- server.Serve(tls.NewListener(&ingressListener{Listener: listener, allowPublic: *allowPublic}, server.TLSConfig))
		}()
		log.Printf("OneSpace Hub %s listening on %s; Drive recording is opt-in", hub.Version, listener.Addr())
		log.Printf("Verify hub certificate SHA-256 locally: %s", fingerprint)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		select {
		case err := <-errorsCh:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		case <-ctx.Done():
			// Close upstream viewers before releasing the durable hub lock. Then
			// wake long polls so HTTP shutdown can drain without a fifty-second wait.
			storage.Close()
			gateway.Close()
			_ = h.Close()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return server.Shutdown(shutdown)
		}
	default:
		return errors.New("unknown command")
	}
}

// Reject disallowed network peers before paying for a TLS handshake. Never use
// X-Forwarded-For or other caller-supplied headers as the ingress identity.
type privateListener struct{ net.Listener }

func (l privateListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		peer, ok := connection.RemoteAddr().(*net.TCPAddr)
		if ok && hub.TrustedSource(peer.IP) {
			return connection, nil
		}
		_ = connection.Close()
	}
}
