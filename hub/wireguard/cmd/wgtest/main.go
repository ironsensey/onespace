// wgtest exercises OneSpace's embedded WireGuard transport on real interfaces.
// It authenticates the peer and HTTPS server; it is NOT application client authorization.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"onespace.local/hub/wireguard"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type options struct {
	mode, name, keyFile, peerKey, peerIP, overlay, peerOverlay, caCert, certFile, tlsKey, revokeFile string
	listenPort, peerPort                                                                             int
	duration, readyTimeout, probeInterval                                                            time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func parseOptions(args []string) (options, error) {
	var o options
	f := flag.NewFlagSet("wgtest", flag.ContinueOnError)
	f.StringVar(&o.mode, "mode", "", "init-client, init-server, client, or server")
	f.StringVar(&o.name, "name", "wg-test", "temporary adapter name")
	f.StringVar(&o.keyFile, "key-file", "", "persistent PRIVATE key path; never exported")
	f.StringVar(&o.peerKey, "peer-key", "", "peer PUBLIC key: Base64 or hex")
	f.StringVar(&o.peerIP, "peer-ip", "", "peer LAN endpoint IP")
	f.IntVar(&o.peerPort, "peer-port", 51820, "peer UDP port")
	f.IntVar(&o.listenPort, "listen-port", 51820, "local UDP port")
	f.StringVar(&o.overlay, "overlay-ip", "10.200.200.2/32", "local overlay prefix")
	f.StringVar(&o.peerOverlay, "peer-overlay-ip", "10.200.200.1", "peer overlay address (/32 route)")
	f.StringVar(&o.caCert, "ca-cert", "", "explicit trusted public server certificate (client)")
	f.StringVar(&o.certFile, "cert-file", "", "persistent public server certificate PEM")
	f.StringVar(&o.tlsKey, "tls-key-file", "", "persistent PRIVATE server TLS key PEM")
	f.StringVar(&o.revokeFile, "revoke-file", "", "local sentinel: remove configured peer when this file appears")
	f.DurationVar(&o.duration, "duration", 15*time.Minute, "maximum process lifetime (up to 1 hour)")
	f.DurationVar(&o.readyTimeout, "enroll-timeout", 45*time.Second, "bounded cancellable HTTPS readiness deadline")
	f.DurationVar(&o.probeInterval, "probe-interval", 0, "fresh HTTPS connection interval after success; zero exits immediately")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, fmt.Errorf("unexpected positional arguments")
	}
	if o.mode != "init-client" && o.mode != "init-server" && o.mode != "client" && o.mode != "server" {
		return o, fmt.Errorf("invalid mode")
	}
	if o.keyFile == "" {
		return o, fmt.Errorf("-key-file is required")
	}
	if o.duration <= 0 || o.duration > time.Hour || o.readyTimeout <= 0 || o.readyTimeout > 5*time.Minute || o.probeInterval < 0 {
		return o, fmt.Errorf("invalid timeout/interval")
	}
	if o.listenPort < 1 || o.listenPort > 65535 || o.peerPort < 1 || o.peerPort > 65535 {
		return o, fmt.Errorf("invalid UDP port")
	}
	a, err := netip.ParsePrefix(o.overlay)
	if err != nil || !a.Addr().Is4() || a.Bits() != 32 {
		return o, fmt.Errorf("local overlay must be IPv4 /32")
	}
	p, err := netip.ParseAddr(o.peerOverlay)
	if err != nil || !p.Is4() || p == a.Addr() {
		return o, fmt.Errorf("invalid peer overlay")
	}
	if o.mode == "client" || o.mode == "server" {
		o.peerKey, err = wireguard.NormalizePublicKey(o.peerKey)
		if err != nil {
			return o, err
		}
		if ip, err := netip.ParseAddr(o.peerIP); err != nil || !ip.Is4() {
			return o, fmt.Errorf("peer endpoint must be IPv4")
		}
	}
	if o.mode == "client" && o.caCert == "" {
		return o, fmt.Errorf("-ca-cert is required")
	}
	if (o.mode == "server" || o.mode == "init-server") && (o.certFile == "" || o.tlsKey == "") {
		return o, fmt.Errorf("-cert-file and -tls-key-file are required")
	}
	return o, nil
}

func execute(parent context.Context, args []string, out io.Writer) error {
	o, err := parseOptions(args)
	if err != nil {
		return err
	}
	// Verify explicit trust before generating identities, opening adapters, or dialing.
	var roots *x509.CertPool
	if o.mode == "client" {
		var fp string
		roots, fp, err = LoadCACertificate(o.caCert)
		if err != nil {
			return err
		}
		if err = validateServerCertificate(o.caCert, o.peerOverlay); err != nil {
			return err
		}
		fmt.Fprintln(out, "TRUSTED_CERT_SHA256="+fp)
	}
	var cert tls.Certificate
	if o.mode == "server" || o.mode == "init-server" {
		cert, err = loadOrCreateCertificate(o.certFile, o.tlsKey)
		if err != nil {
			return err
		}
		if err = validateServerCertificate(o.certFile, netip.MustParsePrefix(o.overlay).Addr().String()); err != nil {
			return err
		}
	}
	kp, err := loadOrGenerateKeyPair(o.keyFile)
	if err != nil {
		return fmt.Errorf("key file: %w", err)
	}
	fmt.Fprintf(out, "PUBLIC_KEY=%s\nPUBLIC_KEY_HEX=%s\nMODE=%s\n", kp.Base64PublicKey, kp.PublicKey, o.mode)
	if o.mode == "init-client" || o.mode == "init-server" {
		return nil
	}
	if kp.PublicKey == o.peerKey {
		return fmt.Errorf("peer cannot use local identity")
	}
	if o.revokeFile != "" {
		if _, err := os.Lstat(o.revokeFile); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("revoke sentinel must be absent at startup")
		}
	}
	ctx, cancel := context.WithTimeout(parent, o.duration)
	defer cancel()
	tunnel, err := wireguard.NewTunnel(wireguard.TunnelConfig{Name: o.name, MTU: 1420})
	if err != nil {
		return err
	}
	defer tunnel.Close()
	cfg := wireguard.Config{PrivateKey: kp.PrivateKey, ListenPort: o.listenPort, Peers: []wireguard.Peer{{PublicKey: o.peerKey, Endpoint: net.JoinHostPort(o.peerIP, fmt.Sprint(o.peerPort)), AllowedIPs: []string{o.peerOverlay + "/32"}, PersistentKeepalive: 5}}}
	if err = tunnel.Configure(cfg); err != nil {
		return err
	}
	setupCtx, setupCancel := context.WithTimeout(ctx, 30*time.Second)
	err = tunnel.ConfigureNetwork(setupCtx, o.overlay, []string{o.peerOverlay + "/32"})
	setupCancel()
	if err != nil {
		return fmt.Errorf("network setup: %w", err)
	}
	fmt.Fprintf(out, "TUNNEL_UP interface=%s local=%s peer=%s/32 expires=%s\n", tunnel.Name, o.overlay, o.peerOverlay, time.Now().Add(o.duration).UTC().Format(time.RFC3339))
	if o.mode == "server" {
		return serve(ctx, tunnel, o, cert, out)
	}
	return runClient(ctx, tunnel, o, roots, out)
}

func loadOrGenerateKeyPair(path string) (wireguard.KeyPair, error) {
	return wireguard.LoadOrCreateKeyPair(path)
}

func printStats(t *wireguard.Tunnel, out io.Writer) error {
	peers, err := t.PeerStats()
	if err != nil {
		return err
	}
	data, err := json.Marshal(peers)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "STATS time=%s peers=%s\n", time.Now().UTC().Format(time.RFC3339Nano), data)
	return nil
}

func startHTTPS(ctx context.Context, address string, cert tls.Certificate, out io.Writer) (*http.Server, <-chan error, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return nil, nil, fmt.Errorf("bind HTTPS: %w", err)
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 5 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(out, "HTTPS_REQUEST time=%s remote=%s tls=%x path=/\n", time.Now().UTC().Format(time.RFC3339Nano), r.RemoteAddr, r.TLS.Version)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok")
	})}
	done := make(chan error, 1)
	go func() { done <- server.ServeTLS(listener, "", "") }()
	fmt.Fprintln(out, "HTTPS_LISTENING="+listener.Addr().String())
	return server, done, nil
}

func serve(ctx context.Context, t *wireguard.Tunnel, o options, cert tls.Certificate, out io.Writer) error {
	address := net.JoinHostPort(netip.MustParsePrefix(o.overlay).Addr().String(), "8443")
	server, done, err := startHTTPS(ctx, address, cert, out)
	if err != nil {
		return err
	}
	defer server.Close()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	revoked := false
	for {
		select {
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return fmt.Errorf("HTTPS serve: %w", err)
		case <-ctx.Done():
			fmt.Fprintln(out, "SERVER_STOPPED:", ctx.Err())
			return nil
		case <-ticker.C:
			if err = printStats(t, out); err != nil {
				return err
			}
			if o.revokeFile != "" && !revoked {
				_, e := os.Stat(o.revokeFile)
				if e == nil {
					if err = t.RemovePeer(o.peerKey); err != nil {
						return err
					}
					revoked = true
					fmt.Fprintln(out, "PEER_REVOKED="+o.peerKey)
				} else if !errors.Is(e, os.ErrNotExist) {
					return e
				}
			}
		}
	}
}

// Each request uses a new connection and explicit source address; no proxy or TLS bypass.
func requestHTTPS(ctx context.Context, url, local string, roots *x509.CertPool, out io.Writer) error {
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	if local != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(local)}
	}
	transport := &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true, TLSHandshakeTimeout: 4 * time.Second, TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "test-receiver", MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 6 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect refused") }}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1025))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		return fmt.Errorf("unexpected HTTPS response status=%d body=%q", resp.StatusCode, body)
	}
	if resp.TLS == nil || len(resp.TLS.VerifiedChains) == 0 {
		return fmt.Errorf("TLS certificate was not verified")
	}
	fmt.Fprintf(out, "REQUEST_RESULT time=%s status=%d body=%s tls_verified=true\n", time.Now().UTC().Format(time.RFC3339Nano), resp.StatusCode, body)
	return nil
}

func awaitReady(ctx context.Context, attempt func(context.Context) error) error {
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("readiness deadline: %w (last request: %v)", err, last)
		}
		last = attempt(ctx)
		if last == nil {
			return nil
		}
		// A certificate/status failure is not a readiness problem.
		var verification *tls.CertificateVerificationError
		var network net.Error
		if errors.As(last, &verification) || !errors.As(last, &network) {
			return last
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("readiness failed: %w (last request: %v)", ctx.Err(), last)
		case <-timer.C:
		}
	}
}

func runClient(ctx context.Context, t *wireguard.Tunnel, o options, roots *x509.CertPool, out io.Writer) error {
	local := netip.MustParsePrefix(o.overlay).Addr().String()
	attempt := func(c context.Context) error {
		return requestHTTPS(c, "https://"+net.JoinHostPort(o.peerOverlay, "8443")+"/", local, roots, out)
	}
	ready, cancel := context.WithTimeout(ctx, o.readyTimeout)
	err := awaitReady(ready, attempt)
	cancel()
	if statsErr := printStats(t, out); statsErr != nil {
		return statsErr
	}
	if err != nil {
		return fmt.Errorf("REQUEST_FAILED: %w", err)
	}
	peers, err := t.PeerStats()
	if err != nil {
		return err
	}
	found := false
	for _, p := range peers {
		if p.PublicKey == o.peerKey && !p.LastHandshake.IsZero() && p.RxBytes > 0 && p.TxBytes > 0 {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("HTTPS completed without peer handshake/traffic evidence")
	}
	if o.probeInterval == 0 {
		return nil
	}
	ticker := time.NewTicker(o.probeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(out, "CLIENT_STOPPED:", ctx.Err())
			return nil
		case <-ticker.C:
			err = attempt(ctx)
			if statErr := printStats(t, out); statErr != nil {
				return statErr
			}
			if err != nil {
				return fmt.Errorf("REQUEST_FAILED fresh_connection: %w", err)
			}
		}
	}
}
