// Package camera exposes a bounded, authenticated WHEP signaling gateway.
// Video remains DTLS-SRTP between MediaMTX and the enrolled viewer.
package camera

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const prefix = "/hub/v1/camera/"
const maxSDP = 256 << 10

type Config struct {
	Upstream    string
	MaxSessions int
	SessionTTL  time.Duration
	Recording   func() bool
	ICEServers  []ICEServer
	// ICEProvider is evaluated for each status request so credentials can be
	// short-lived. It must not return permanent TURN credentials.
	ICEProvider func() []ICEServer
}

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type session struct {
	owner    string
	endpoint *url.URL
	expires  time.Time
	auth     *http.Request
}

type Gateway struct {
	upstream    *url.URL
	authorize   func(*http.Request) (string, bool)
	client      *http.Client
	mu          sync.Mutex
	sessions    map[string]*session
	iceServers  []ICEServer
	iceProvider func() []ICEServer
	max         int
	ttl         time.Duration
	stop        chan struct{}
	closed      bool
	recording   func() bool
}

// Register accepts an administrator-supplied loopback upstream only. Never pass
// a browser-supplied URL here. The camera itself is never captured by the hub.
func Register(mux *http.ServeMux, cfg Config, authorize func(*http.Request) (string, bool)) (*Gateway, error) {
	if cfg.Upstream == "" {
		cfg.Upstream = "http://127.0.0.1:8889/camera/whep"
	}
	u, err := url.Parse(cfg.Upstream)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasSuffix(u.Path, "/whep") {
		return nil, errors.New("camera upstream must be a fixed loopback HTTP WHEP URL")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("camera upstream must use a numeric loopback address")
	}
	if authorize == nil {
		return nil, errors.New("camera authorization is required")
	}
	if cfg.MaxSessions == 0 {
		cfg.MaxSessions = 2
	}
	if cfg.MaxSessions < 1 || cfg.MaxSessions > 32 {
		return nil, errors.New("camera sessions must be between 1 and 32")
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = time.Hour
	}
	if cfg.SessionTTL <= 0 || cfg.SessionTTL > 24*time.Hour {
		return nil, errors.New("camera session lifetime must be positive and at most 24 hours")
	}
	g := &Gateway{recording: cfg.Recording, upstream: u, authorize: authorize, sessions: map[string]*session{}, iceServers: append([]ICEServer(nil), cfg.ICEServers...), iceProvider: cfg.ICEProvider, max: cfg.MaxSessions, ttl: cfg.SessionTTL, stop: make(chan struct{})}
	g.client = &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, IdleConnTimeout: time.Minute, ResponseHeaderTimeout: 5 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	mux.HandleFunc(prefix+"status", g.status)
	mux.HandleFunc(prefix+"whep", g.offer)
	mux.HandleFunc(prefix+"session/", g.remove)
	go g.maintain()
	return g, nil
}

func reply(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func reject(w http.ResponseWriter, code int, message string) {
	reply(w, code, map[string]string{"error": message})
}
func (g *Gateway) allowed(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := g.authorize(r)
	if !ok || id == "" {
		reject(w, 403, "Camera permission required")
		return "", false
	}
	return id, true
}
func (g *Gateway) status(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.allowed(w, r); !ok {
		return
	}
	if r.Method != "GET" {
		reject(w, 405, "Method not allowed")
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), "OPTIONS", g.upstream.String(), nil)
	response, err := g.client.Do(req)
	reachable := err == nil && response.StatusCode < 500 && response.StatusCode < 300
	if response != nil {
		response.Body.Close()
	}
	g.mu.Lock()
	iceServers := append([]ICEServer(nil), g.iceServers...)
	if g.iceProvider != nil {
		iceServers = append([]ICEServer(nil), g.iceProvider()...)
	}
	g.mu.Unlock()
	reply(w, 200, map[string]any{"configured": true, "reachable": reachable, "recording": g.recording != nil && g.recording(), "availability": "Live frames are verified only after playback connects", "maxSessions": g.max, "iceServers": iceServers})
}

func (g *Gateway) offer(w http.ResponseWriter, r *http.Request) {
	owner, ok := g.allowed(w, r)
	if !ok {
		return
	}
	if r.Method != "POST" {
		reject(w, 405, "Method not allowed")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/sdp" {
		reject(w, 415, "Expected application/sdp")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSDP))
	if err != nil {
		reject(w, 413, "SDP exceeds 256 KiB")
		return
	}
	if len(body) == 0 {
		reject(w, 400, "Empty SDP")
		return
	}
	random := make([]byte, 24)
	if _, err = rand.Read(random); err != nil {
		reject(w, 500, "Cannot allocate session")
		return
	}
	id := hex.EncodeToString(random)
	auth := &http.Request{Header: make(http.Header)}
	auth.Header.Set("Authorization", r.Header.Get("Authorization"))
	s := &session{owner: owner, expires: time.Now().Add(g.ttl), auth: auth}
	g.mu.Lock()
	if g.closed || len(g.sessions) >= g.max {
		g.mu.Unlock()
		reject(w, 429, "Camera viewer limit reached")
		return
	}
	g.sessions[id] = s
	g.mu.Unlock()
	committed := false
	defer func() {
		if !committed {
			g.mu.Lock()
			delete(g.sessions, id)
			g.mu.Unlock()
		}
	}()
	req, _ := http.NewRequestWithContext(r.Context(), "POST", g.upstream.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/sdp")
	response, err := g.client.Do(req)
	if err != nil {
		reject(w, 503, "Live camera signaling unavailable")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 201 {
		reject(w, 502, "Camera publisher unavailable or SDP rejected")
		return
	}
	endpoint, err := g.sessionURL(response.Header.Get("Location"))
	if err != nil {
		reject(w, 502, "Invalid camera session location")
		return
	}
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxSDP+1))
	if err != nil || len(answer) == 0 || len(answer) > maxSDP {
		g.deleteUpstream(endpoint)
		reject(w, 502, "Invalid camera answer")
		return
	}
	// Revoke/expiry can occur while MediaMTX is negotiating.
	validOwner, valid := g.authorize(auth)
	g.mu.Lock()
	_, exists := g.sessions[id]
	if !valid || validOwner != owner || !exists || g.closed {
		g.mu.Unlock()
		g.deleteUpstream(endpoint)
		reject(w, 403, "Camera authorization changed")
		return
	}
	s.endpoint = endpoint
	g.mu.Unlock()
	committed = true
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", prefix+"session/"+id)
	w.WriteHeader(201)
	_, _ = w.Write(answer)
}

func (g *Gateway) sessionURL(location string) (*url.URL, error) {
	if location == "" {
		return nil, errors.New("missing session location")
	}
	raw, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	u := g.upstream.ResolveReference(raw)
	base := strings.TrimSuffix(g.upstream.Path, "/") + "/"
	// Do not forward tokens, redirects, query strings or path escapes. Only the
	// specific local WHEP session resource can ever be deleted through this API.
	if u.Scheme != g.upstream.Scheme || u.Host != g.upstream.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, base) || strings.Contains(strings.TrimPrefix(u.Path, base), "/") || strings.TrimPrefix(u.Path, base) == "" {
		return nil, errors.New("untrusted session location")
	}
	sessionID := strings.TrimPrefix(u.Path, base)
	if len(sessionID) > 128 {
		return nil, errors.New("oversized session identifier")
	}
	for _, r := range sessionID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return nil, errors.New("invalid session identifier")
		}
	}
	return u, nil
}

func (g *Gateway) remove(w http.ResponseWriter, r *http.Request) {
	owner, ok := g.allowed(w, r)
	if !ok {
		return
	}
	if r.Method != "DELETE" {
		reject(w, 405, "Method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix+"session/")
	g.mu.Lock()
	s, exists := g.sessions[id]
	if !exists || s.owner != owner {
		g.mu.Unlock()
		reject(w, 404, "Camera session missing")
		return
	}
	delete(g.sessions, id)
	endpoint := s.endpoint
	g.mu.Unlock()
	if endpoint != nil {
		g.deleteUpstream(endpoint)
	}
	w.WriteHeader(204)
}
func (g *Gateway) deleteUpstream(u *url.URL) {
	req, _ := http.NewRequest("DELETE", u.String(), nil)
	response, err := g.client.Do(req)
	if err == nil {
		response.Body.Close()
	}
}

// Sessions are bounded and active viewers lose access on expiry/revocation.
// Revocation closes the upstream WHEP session at the next 15-second check.
func (g *Gateway) maintain() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-ticker.C:
			g.sweep()
		}
	}
}
func (g *Gateway) sweep() {
	g.mu.Lock()
	stale := []*url.URL{}
	for id, s := range g.sessions {
		owner, valid := g.authorize(s.auth)
		if time.Now().After(s.expires) || !valid || owner != s.owner {
			if s.endpoint != nil {
				stale = append(stale, s.endpoint)
			}
			delete(g.sessions, id)
		}
	}
	g.mu.Unlock()
	g.deleteMany(stale)
}

// At most MaxSessions deletes run in parallel. A slow MediaMTX endpoint
// cannot multiply shutdown latency by the number of viewers.
func (g *Gateway) deleteMany(endpoints []*url.URL) {
	var pending sync.WaitGroup
	for _, endpoint := range endpoints {
		pending.Add(1)
		go func(u *url.URL) { defer pending.Done(); g.deleteUpstream(u) }(endpoint)
	}
	pending.Wait()
}

func (g *Gateway) Close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	close(g.stop)
	stale := []*url.URL{}
	for _, s := range g.sessions {
		if s.endpoint != nil {
			stale = append(stale, s.endpoint)
		}
	}
	g.sessions = map[string]*session{}
	g.mu.Unlock()
	g.deleteMany(stale)
	g.client.CloseIdleConnections()
}
