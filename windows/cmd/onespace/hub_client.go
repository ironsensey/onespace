package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"onespace.local/desktop/internal/clipboard"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type HubConfig struct {
	LocalAddress     string   `json:"localAddress,omitempty"`
	Connect          bool     `json:"connect"`
	Address          string   `json:"address"`
	Fingerprint      string   `json:"fingerprint"`
	Token            string   `json:"token,omitempty"`
	Target           string   `json:"target"`
	Enabled          bool     `json:"enabled"`
	ClipboardMode    string   `json:"clipboardMode"`
	ReceiveClipboard bool     `json:"receiveClipboard"`
	AllowMedia       bool     `json:"allowMedia"`
	OverlayCIDRs     []string `json:"overlayCIDRs"`
}
type hubDiskConfig struct {
	HubConfig
	ProtectedToken string `json:"protectedToken"`
}
type HubEvent struct {
	Route     string    `json:"-"`
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text,omitempty"`
	Action    string    `json:"action,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type HubBridge struct {
	app       *App
	mu        sync.Mutex
	cfg       HubConfig
	changed   chan struct{}
	outgoing  chan HubEvent
	connected bool
	lastSeen  time.Time
	lastError string
	receipts  map[string]time.Time
}

func newHubBridge(a *App) *HubBridge {
	b := &HubBridge{app: a, changed: make(chan struct{}, 1), outgoing: make(chan HubEvent, 1), receipts: map[string]time.Time{}}
	b.cfg.ClipboardMode = "lan"
	data, err := os.ReadFile(filepath.Join(a.s.root, "hub.json"))
	if err == nil {
		var disk hubDiskConfig
		if json.Unmarshal(data, &disk) == nil {
			token, e := unprotectHubToken(disk.ProtectedToken)
			if e == nil {
				disk.HubConfig.Token = token
				if validateHubConfig(disk.HubConfig) == nil {
					b.cfg = disk.HubConfig
				}
			}
		}
	}
	data, err = os.ReadFile(filepath.Join(a.s.root, "hub-receipts.json"))
	if err == nil {
		_ = json.Unmarshal(data, &b.receipts)
	}
	if b.receipts == nil {
		b.receipts = map[string]time.Time{}
	}
	return b
}
func validateHubConfig(c HubConfig) error {
	for _, network := range c.OverlayCIDRs {
		_, subnet, err := net.ParseCIDR(network)
		if err != nil || !safeOverlayNetwork(subnet) {
			return errors.New("Overlay allowlist supports only subsets of 100.64.0.0/10 or fd7a:115c:a1e0::/48")
		}
	}
	if !c.Enabled && c.Address == "" {
		return nil
	}
	if c.Enabled && c.ClipboardMode == "hub" && c.Target == "" {
		return errors.New("Select a target device before routing clipboard text through the hub")
	}
	if c.LocalAddress != "" {
		local, e := url.Parse(c.LocalAddress)
		if e != nil || local.Scheme != "https" || local.Hostname() == "" || local.User != nil || (local.Path != "" && local.Path != "/") || local.RawQuery != "" || local.Fragment != "" {
			return errors.New("Local hub address must be an HTTPS origin")
		}
	}
	u, err := url.Parse(c.Address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("Use an HTTPS hub address, e.g. https://100.x.y.z:47603")
	}
	pin, err := hex.DecodeString(c.Fingerprint)
	if err != nil || len(pin) != 32 {
		return errors.New("Hub certificate fingerprint must contain 64 hexadecimal characters")
	}
	if c.Enabled && len(c.Token) < 32 {
		return errors.New("Enter the scoped token issued on your Pi")
	}
	if c.ClipboardMode != "lan" && c.ClipboardMode != "hub" {
		return errors.New("Clipboard mode must be lan or hub")
	}
	for _, s := range c.OverlayCIDRs {
		_, n, e := net.ParseCIDR(s)
		if e != nil || !safeOverlayNetwork(n) {
			return errors.New("Overlay allowlist supports only subsets of 100.64.0.0/10 or fd7a:115c:a1e0::/48")
		}
	}
	return nil
}
func safeOverlayNetwork(n *net.IPNet) bool {
	for _, s := range []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"} {
		_, allowed, _ := net.ParseCIDR(s)
		ones, bits := n.Mask.Size()
		ao, ab := allowed.Mask.Size()
		if bits == ab && ones >= ao && allowed.Contains(n.IP) {
			return true
		}
	}
	return false
}
func newPinnedHubClient(c HubConfig) (*http.Client, error) {
	if err := validateHubConfig(c); err != nil {
		return nil, err
	}
	expected, _ := hex.DecodeString(c.Fingerprint)
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 6, IdleConnTimeout: 80 * time.Second, ResponseHeaderTimeout: 60 * time.Second,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12,
			// Self-signed endpoint identity is verified by the out-of-band pin below,
			// instead of public CA/DNS validation. Do not remove VerifyConnection.
			InsecureSkipVerify: true, VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("Hub supplied no certificate")
				}
				cert := cs.PeerCertificates[0]
				actual := sha256.Sum256(cert.Raw)
				if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
					return errors.New("Hub certificate identity mismatch")
				}
				now := time.Now()
				if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
					return errors.New("Hub certificate expired or clock incorrect")
				}
				return nil
			}}}
	transport.DialTLSContext = pinnedHubDial(c, transport.TLSClientConfig)
	return &http.Client{Transport: transport, Timeout: 65 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func hubRequest(ctx context.Context, cli *http.Client, c HubConfig, method, path, contentType string, body io.Reader) (*http.Response, error) {
	if !strings.HasPrefix(path, "/hub/v1/") {
		return nil, errors.New("Invalid hub path")
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Address, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		return nil, &hubStatusError{Code: res.StatusCode}
	}
	return res, nil
}
func (b *HubBridge) snapshot() HubConfig {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.cfg
	c.OverlayCIDRs = append([]string(nil), c.OverlayCIDRs...)
	return c
}
func (b *HubBridge) setStatus(connected bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connected = connected
	if connected {
		b.lastSeen = time.Now().UTC()
		b.lastError = ""
	} else if err != nil {
		b.lastError = "Hub unavailable: check address, private network, fingerprint and scoped token"
	}
}
func (b *HubBridge) Run(ctx context.Context) {
	for {
		cfg := b.snapshot()
		if !cfg.Enabled {
			select {
			case <-ctx.Done():
				return
			case <-b.changed:
				continue
			}
		}
		child, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); b.session(child, cfg) }()
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return
		case <-b.changed:
			cancel()
			<-done
			b.setStatus(false, nil)
		}
	}
}
func (b *HubBridge) session(ctx context.Context, c HubConfig) {
	cli, err := newPinnedHubClient(c)
	if err != nil {
		b.setStatus(false, err)
		return
	}
	defer cli.CloseIdleConnections()
	var connectWorkers sync.WaitGroup
	if c.Connect && len(b.app.pcCertificate.Certificate) > 0 {
		for i := 0; i < 4; i++ {
			connectWorkers.Add(1)
			go func() { defer connectWorkers.Done(); b.connectLoop(ctx, c) }()
		}
		defer connectWorkers.Wait()
	}
	// Validate presence promptly rather than waiting for the first empty 50s poll.
	if response, e := hubRequest(ctx, cli, c, "GET", "/hub/v1/devices", "", nil); e == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		b.setStatus(true, nil)
	} else {
		b.setStatus(false, e)
	}
	sendCtx, stopSend := context.WithCancel(ctx)
	defer stopSend()
	sendDone := make(chan struct{})
	go func() { defer close(sendDone); b.sendLoop(sendCtx, cli, c) }()

	defer func() { stopSend(); <-sendDone }()
	retry := 1500 * time.Millisecond
	for ctx.Err() == nil {
		res, e := hubRequest(ctx, cli, c, "GET", "/hub/v1/events?wait=50", "", nil)
		if e == nil {
			var events []HubEvent
			e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&events)
			res.Body.Close()
			if e == nil {
				b.setStatus(true, nil)
				retry = 1500 * time.Millisecond
				for _, event := range events {
					if ctx.Err() != nil {
						return
					}
					if e = b.process(ctx, cli, c, event); e != nil {
						break
					}
				}
			}
		}
		if e != nil {
			b.setStatus(false, e)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retry):
			}
			retry *= 2
			if retry > 30*time.Second {
				retry = 30 * time.Second
			}
		}
	}
}

// Hub POSTs are idempotent: bounded retries reuse the same event id. A newer
// copy supersedes pending work instead of queueing behind a stale WAN request.
func (b *HubBridge) sendLoop(ctx context.Context, cli *http.Client, c HubConfig) {
	for {
		var event HubEvent
		select {
		case <-ctx.Done():
			return
		case event = <-b.outgoing:
		}
		delay := 1500 * time.Millisecond
		for {
			if event.Route != c.Fingerprint+":"+c.Token || event.To != c.Target || !time.Now().Before(event.ExpiresAt) {
				break
			}
			payload, _ := json.Marshal(map[string]string{"id": event.ID, "to": event.To, "kind": event.Kind, "text": event.Text, "action": event.Action})
			deadline := time.Now().Add(8 * time.Second)
			if event.ExpiresAt.Before(deadline) {
				deadline = event.ExpiresAt
			}
			attempt, cancel := context.WithDeadline(ctx, deadline)
			response, err := hubRequest(attempt, cli, c, "POST", "/hub/v1/events", "application/json", bytes.NewReader(payload))
			if err == nil {
				response.Body.Close()
			}
			cancel()
			if err == nil {
				break
			}
			b.setStatus(false, err)
			var status *hubStatusError
			if errors.As(err, &status) && status.Code >= 400 && status.Code < 500 && status.Code != 429 {
				break
			}
			select {
			case <-ctx.Done():
				return
			case newer := <-b.outgoing:
				event = newer
				delay = 1500 * time.Millisecond
			case <-time.After(delay):
				delay *= 2
				if delay > 10*time.Second {
					delay = 10 * time.Second
				}
			}
		}
	}
}

type hubStatusError struct{ Code int }

func (e *hubStatusError) Error() string {
	return fmt.Sprintf("Hub returned HTTP %d; check connection, token and permissions", e.Code)
}

func (b *HubBridge) process(ctx context.Context, cli *http.Client, c HubConfig, event HubEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(event.ID) > 128 || strings.ContainsAny(event.ID, "/\\?#") {
		return errors.New("Invalid hub event id")
	}
	key := c.Fingerprint + ":" + event.ID
	b.mu.Lock()
	_, done := b.receipts[key]
	b.mu.Unlock()
	if !done && time.Now().Before(event.ExpiresAt) {
		// Persist before side effects: at-most-once media toggles, with possible loss
		// on a crash between commit and action; never promise exactly-once OS actions.
		b.mu.Lock()
		now := time.Now()
		for id, expiry := range b.receipts {
			if expiry.Before(now) {
				delete(b.receipts, id)
			}
		}
		if len(b.receipts) >= 512 {
			b.mu.Unlock()
			return errors.New("Receipt capacity reached")
		}
		b.receipts[key] = now.Add(2 * time.Minute)
		data, _ := json.Marshal(b.receipts)
		err := writeHubFile(filepath.Join(b.app.s.root, "hub-receipts.json"), data)
		if err != nil {
			delete(b.receipts, key)
			b.mu.Unlock()
			return err
		}
		b.mu.Unlock()
		switch event.Kind {
		case "clipboard.text":
			if c.ReceiveClipboard && len(event.Text) > 0 && len(event.Text) <= 65536 {
				b.app.clipMu.Lock()
				if ctx.Err() != nil {
					b.app.clipMu.Unlock()
					return ctx.Err()
				}
				err := clipboard.SetText(event.Text)
				if err == nil {
					b.app.clipSeq = clipboard.Sequence()
					b.app.clipReady = true
					b.app.clipFailures = 0
				}
				b.app.clipMu.Unlock()
				if err != nil {
					b.mu.Lock()
					delete(b.receipts, key)
					pending, _ := json.Marshal(b.receipts)
					_ = writeHubFile(filepath.Join(b.app.s.root, "hub-receipts.json"), pending)
					b.mu.Unlock()
					return err
				}
			}
		case "media":
			if c.AllowMedia {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err := performMediaAction(event.Action); err != nil {
					return err
				}
			}
		}
	}
	res, err := hubRequest(ctx, cli, c, "POST", "/hub/v1/events/"+url.PathEscape(event.ID)+"/ack", "application/json", strings.NewReader("{}"))
	if err == nil {
		res.Body.Close()
	}
	return err
}
func writeHubFile(path string, data []byte) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Queue only the newest copied text. The clipboard monitor never waits for WAN I/O.
func (b *HubBridge) queueText(text string) bool {
	c := b.snapshot()
	if !c.Enabled || c.ClipboardMode != "hub" {
		return false
	}
	if c.Target == "" || len(text) > 16384 {
		return true
	}
	event := HubEvent{Route: c.Fingerprint + ":" + c.Token, ID: randomHex(16), To: c.Target, Kind: "clipboard.text", Text: text, ExpiresAt: time.Now().Add(60 * time.Second)}
	select {
	case b.outgoing <- event:
	default:
		select {
		case <-b.outgoing:
		default:
		}
		select {
		case b.outgoing <- event:
		default:
		}
	}
	return true
}
func (b *HubBridge) allowsOverlay(ip net.IP) bool {
	for _, s := range b.snapshot().OverlayCIDRs {
		_, n, e := net.ParseCIDR(s)
		if e == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}
func (a *App) hubLocal(w http.ResponseWriter, r *http.Request) {
	b := a.hub
	if b == nil {
		bad(w, 503, "Hub support unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/local/hub")
	if path == "/config" {
		switch r.Method {
		case "GET":
			c := b.snapshot()
			hasToken := c.Token != ""
			c.Token = ""
			b.mu.Lock()
			status := map[string]any{"config": c, "hasToken": hasToken, "connected": b.connected, "lastSeen": b.lastSeen, "error": b.lastError}
			b.mu.Unlock()
			jsonReply(w, 200, status)
		case "POST":
			var c HubConfig
			if readJSON(r, &c) != nil {
				bad(w, 400, "Invalid hub settings")
				return
			}
			old := b.snapshot()
			if c.Token == "" {
				normalizedPin := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(c.Fingerprint, ":", ""), " ", ""))
				if old.Token != "" && (normalizedPin != old.Fingerprint || strings.TrimRight(c.Address, "/") != old.Address) {
					bad(w, 400, "Enter a newly issued token when changing the hub address or identity")
					return
				}
				c.Token = old.Token
			}
			c.Address = strings.TrimRight(c.Address, "/")
			c.Fingerprint = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(c.Fingerprint, ":", ""), " ", ""))
			if e := validateHubConfig(c); e != nil {
				bad(w, 400, e.Error())
				return
			}
			secret, e := protectHubToken(c.Token)
			if e != nil {
				bad(w, 500, "Cannot protect hub credential")
				return
			}
			disk := hubDiskConfig{HubConfig: c, ProtectedToken: secret}
			disk.Token = ""
			data, _ := json.MarshalIndent(disk, "", "  ")
			b.mu.Lock()
			e = writeHubFile(filepath.Join(a.s.root, "hub.json"), data)
			if e == nil {
				b.cfg = c
				b.connected = false
			}
			b.mu.Unlock()
			if e != nil {
				bad(w, 500, "Cannot save hub settings")
				return
			}
			select {
			case b.changed <- struct{}{}:
			default:
			}
			jsonReply(w, 200, map[string]bool{"saved": true})
		default:
			bad(w, 405, "Method not allowed")
		}
		return
	}
	c := b.snapshot()
	if !c.Enabled {
		bad(w, 409, "Enable the Pi hub connection first")
		return
	}
	cli, e := newPinnedHubClient(c)
	if e != nil {
		bad(w, 400, e.Error())
		return
	}
	defer cli.CloseIdleConnections()
	switch {
	case path == "/devices" && r.Method == "GET":
		res, e := hubRequest(r.Context(), cli, c, "GET", "/hub/v1/devices", "", nil)
		if e != nil {
			bad(w, 502, e.Error())
			return
		}
		defer res.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, io.LimitReader(res.Body, 1<<20))
	case path == "/send" && r.Method == "POST":
		var in struct {
			To     string `json:"to"`
			Kind   string `json:"kind"`
			Text   string `json:"text"`
			Action string `json:"action"`
		}
		if readJSON(r, &in) != nil || (in.Kind == "clipboard.text" && (len(in.Text) == 0 || len(in.Text) > 16384)) {
			bad(w, 400, "Enter text up to 16 KiB or a valid media event")
			return
		}
		payload, _ := json.Marshal(map[string]string{"id": randomHex(16), "to": in.To, "kind": in.Kind, "text": in.Text, "action": in.Action})
		res, e := hubRequest(r.Context(), cli, c, "POST", "/hub/v1/events", "application/json", bytes.NewReader(payload))
		if e != nil {
			bad(w, 502, e.Error())
			return
		}
		defer res.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		io.Copy(w, io.LimitReader(res.Body, 1<<20))
	case strings.HasPrefix(path, "/updates/"):
		a.proxyHubUpdates(w, r, cli, c, path)
	case strings.HasPrefix(path, "/cloud/"):
		a.proxyHubCloud(w, r, cli, c, path)
	case strings.HasPrefix(path, "/camera/"):
		if path != "/camera/status" && path != "/camera/whep" && !strings.HasPrefix(path, "/camera/session/") {
			bad(w, 404, "Unknown camera path")
			return
		}
		body := http.MaxBytesReader(w, r.Body, 256<<10)
		res, e := hubRequest(r.Context(), cli, c, r.Method, "/hub/v1"+path, r.Header.Get("Content-Type"), body)
		if e != nil {
			bad(w, 502, e.Error())
			return
		}
		defer res.Body.Close()
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		if location := res.Header.Get("Location"); strings.HasPrefix(location, "/hub/v1/camera/session/") {
			w.Header().Set("Location", "/local/hub"+strings.TrimPrefix(location, "/hub/v1"))
		}
		w.WriteHeader(res.StatusCode)
		io.Copy(w, io.LimitReader(res.Body, 512<<10))
	default:
		bad(w, 404, "Unknown hub operation")
	}
}
