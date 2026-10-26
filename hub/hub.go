// Package hub implements the bounded, authenticated OneSpace preview control hub.
// It never replaces the existing peers' identities or local network listeners.
package hub

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const Version = "0.8.0-preview"
const MaxPending = 256
const MaxDevices = 128
const MaxTextBytes = 64 << 10
const maxStateBytes = 8 << 20
const maxReceipts = 4096

// TrustedSource bounds ingress even when an operator deliberately binds every
// interface. Tailscale IPv4 is CGNAT space, which net.IP.IsPrivate excludes.
// ULA includes Tailscale's fd7a:115c:a1e0::/48; forwarded headers are irrelevant.
func TrustedSource(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return true
	}
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

var validDevice = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)
var validEventID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Device struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Scopes   map[string]bool `json:"scopes"`
	LastSeen time.Time       `json:"lastSeen"`
	Revoked  bool            `json:"revoked,omitempty"`
}

type deviceRecord struct {
	Device
	TokenHash string `json:"tokenHash"`
}

type Event struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text,omitempty"`
	Action    string    `json:"action,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type EventRequest struct {
	ID     string `json:"id,omitempty"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Action string `json:"action,omitempty"`
}

type receipt struct {
	EventID   string    `json:"eventId"`
	To        string    `json:"to"`
	Digest    string    `json:"digest"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type state struct {
	Schema   int                     `json:"schema"`
	Devices  map[string]deviceRecord `json:"devices"`
	Events   []Event                 `json:"events"`
	Receipts map[string]receipt      `json:"receipts"`
}

type Hub struct {
	mu              sync.Mutex
	state           state
	dir             string
	mux             *http.ServeMux
	wake            chan struct{}
	started         time.Time
	lock            *os.File
	closed          bool
	done            chan struct{}
	storageHealthy  bool
	recordingStatus func() bool
	polls           map[string]int
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func digest(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

// Open takes an exclusive lifetime lock. Stop a running service before local
// provisioning so an offline CLI cannot overwrite the daemon's newer snapshot.
func Open(dir string) (*Hub, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("hub data directory must be owner-only (chmod 700)")
	}
	lock, err := lockState(filepath.Join(dir, "state.lock"))
	if err != nil {
		return nil, err
	}
	h := &Hub{dir: dir, mux: http.NewServeMux(), wake: make(chan struct{}), started: time.Now(), lock: lock, done: make(chan struct{}), storageHealthy: true, polls: map[string]int{},
		state: state{Schema: 1, Devices: map[string]deviceRecord{}, Events: []Event{}, Receipts: map[string]receipt{}}}
	file := filepath.Join(dir, "hub.json")
	if data, err := os.ReadFile(file); err == nil {
		if len(data) > maxStateBytes {
			h.Close()
			return nil, errors.New("hub state exceeds size limit")
		}
		if err = json.Unmarshal(data, &h.state); err != nil {
			h.Close()
			return nil, fmt.Errorf("invalid hub state; preserve it and restore a backup: %w", err)
		}
		if h.state.Schema != 1 || h.state.Devices == nil || h.state.Receipts == nil || len(h.state.Devices) > MaxDevices || len(h.state.Events) > MaxPending || len(h.state.Receipts) > maxReceipts {
			h.Close()
			return nil, errors.New("unsupported or invalid hub state")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		h.Close()
		return nil, err
	}
	h.mux.HandleFunc("/hub/v1/info", h.info)
	h.mux.HandleFunc("/hub/v1/devices", h.devices)
	h.mux.HandleFunc("/hub/v1/events", h.events)
	h.mux.HandleFunc("/hub/v1/events/", h.ack)
	h.mux.HandleFunc("/hub/v1/health", h.health)
	go h.retentionLoop()
	return h, nil
}

func (h *Hub) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	close(h.done)
	h.signalLocked()
	return h.lock.Close()
}
func (h *Hub) Mux() *http.ServeMux { return h.mux }

// SetRecordingStatus is configured once before the HTTP listener starts.
func (h *Hub) SetRecordingStatus(status func() bool) { h.recordingStatus = status }
func (h *Hub) recording() bool                       { return h.recordingStatus != nil && h.recordingStatus() }

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.TLS == nil {
		fail(w, 403, "TLS required")
		return
	}
	// Control responses are buffered until handlers release state locks. A slow
	// network reader must not hold the shared metadata mutex while bytes drain.
	if r.URL.Path == "/hub/v1/devices" || strings.HasPrefix(r.URL.Path, "/hub/v1/events") {
		buffer := &responseBuffer{headers: make(http.Header)}
		h.mux.ServeHTTP(buffer, r)
		if buffer.exceeded {
			fail(w, 500, "hub response exceeds policy")
			return
		}
		for key, values := range buffer.headers {
			w.Header()[key] = values
		}
		status := buffer.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = w.Write(buffer.body.Bytes())
		return
	}
	h.mux.ServeHTTP(w, r)
}

type responseBuffer struct {
	headers  http.Header
	body     bytes.Buffer
	status   int
	exceeded bool
}

func (b *responseBuffer) Header() http.Header { return b.headers }
func (b *responseBuffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *responseBuffer) Write(data []byte) (int, error) {
	if b.body.Len()+len(data) > 1<<20 {
		b.exceeded = true
		return 0, errors.New("response too large")
	}
	return b.body.Write(data)
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]string{"error": message})
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	fail(w, 405, "method not allowed")
	return false
}

func (h *Hub) authorizedLocked(r *http.Request, scope string) (Device, bool) {
	return h.authorizeLocked(r, scope, true)
}
func (h *Hub) checkLocked(r *http.Request, scope string) (Device, bool) {
	return h.authorizeLocked(r, scope, false)
}
func (h *Hub) authorizeLocked(r *http.Request, scope string, touch bool) (Device, bool) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || len(auth) != len("Bearer ")+64 {
		return Device{}, false
	}
	tokenHash := digest(strings.TrimPrefix(auth, "Bearer "))
	for id, record := range h.state.Devices {
		if record.Revoked || subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(tokenHash)) != 1 {
			continue
		}
		if scope != "" && !record.Scopes[scope] {
			return Device{}, false
		}
		if touch {
			record.LastSeen = time.Now().UTC()
			h.state.Devices[id] = record
		}
		// Give callers a separate scope map: camera code cannot mutate hub policy.
		d := record.Device
		d.Scopes = cloneScopes(d.Scopes)
		return d, true
	}
	return Device{}, false
}
func cloneScopes(scopes map[string]bool) map[string]bool {
	result := map[string]bool{}
	for k, v := range scopes {
		result[k] = v
	}
	return result
}
func (h *Hub) Authorize(r *http.Request, scope string) (Device, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return Device{}, false
	}
	return h.authorizedLocked(r, scope)
}

// CheckPermission revalidates policy without inventing an authenticated client
// heartbeat. Session reapers and sleeping long polls must use this variant.
func (h *Hub) CheckPermission(r *http.Request, scope string) (Device, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return Device{}, false
	}
	return h.checkLocked(r, scope)
}

func clone(s state) state {
	c := state{Schema: s.Schema, Devices: make(map[string]deviceRecord, len(s.Devices)), Events: append([]Event{}, s.Events...), Receipts: make(map[string]receipt, len(s.Receipts))}
	for k, v := range s.Devices {
		v.Scopes = cloneScopes(v.Scopes)
		c.Devices[k] = v
	}
	for k, v := range s.Receipts {
		c.Receipts[k] = v
	}
	return c
}

// persist writes only a fully validated candidate. On failure, the live state
// remains unchanged; a successful ACK never hides a failed durable write.
func (h *Hub) persist(candidate state) (result error) {
	defer func() { h.storageHealthy = result == nil }()
	data, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	if len(data) > maxStateBytes {
		return errors.New("hub state quota exceeded")
	}
	temp, err := os.CreateTemp(h.dir, ".hub-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(h.dir, "hub.json")); err != nil {
		return err
	}
	// Rename is the commit point. Linux directory sync increases power-loss
	// durability, but failure here cannot be rolled back after a successful rename.
	if dir, err := os.Open(h.dir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	h.state = candidate
	return nil
}
func (h *Hub) signalLocked() { close(h.wake); h.wake = make(chan struct{}) }
func prune(s *state, now time.Time) {
	events := s.Events[:0]
	for _, event := range s.Events {
		if now.Before(event.ExpiresAt) {
			events = append(events, event)
		}
	}
	s.Events = events
	for key, value := range s.Receipts {
		if !now.Before(value.ExpiresAt) {
			delete(s.Receipts, key)
		}
	}
}

func (h *Hub) retentionLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-h.done:
			return
		case now := <-ticker.C:
			_ = h.PurgeExpired(now)
		}
	}
}

// PurgeExpired removes expired plaintext from the durable queue, including when
// no clients send requests. API filtering is immediate; successful storage sweep
// bounds residual on-disk payload retention to five seconds after expiry.
func (h *Hub) PurgeExpired(now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	expired := false
	for _, event := range h.state.Events {
		if !now.Before(event.ExpiresAt) {
			expired = true
			break
		}
	}
	if !expired {
		for _, entry := range h.state.Receipts {
			if !now.Before(entry.ExpiresAt) {
				expired = true
				break
			}
		}
	}
	if !expired {
		return nil
	}
	candidate := clone(h.state)
	prune(&candidate, now)
	if err := h.persist(candidate); err != nil {
		return err
	}
	h.signalLocked()
	return nil
}

// Enroll is a local/offline administrative operation. The returned secret is
// intentionally shown once by the CLI. Only its SHA-256 hash is persisted.
func (h *Hub) Enroll(id, name, kind string, scopes []string) (string, error) {
	if !validDevice.MatchString(id) || strings.TrimSpace(name) == "" || len([]rune(name)) > 80 {
		return "", errors.New("invalid device id or name")
	}
	if kind != "windows" && kind != "android" && kind != "linux" {
		return "", errors.New("kind must be windows, android, or linux")
	}
	allowed := map[string]bool{}
	for _, scope := range scopes {
		if scope != "clipboard" && scope != "media" && scope != "camera" && scope != "cloud" && scope != "recording" {
			return "", errors.New("unknown scope")
		}
		allowed[scope] = true
	}
	if len(allowed) == 0 {
		return "", errors.New("at least one explicit scope is required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", errors.New("hub closed")
	}
	if _, exists := h.state.Devices[id]; exists {
		return "", errors.New("device id already exists; do not silently replace trust")
	}
	if len(h.state.Devices) >= MaxDevices {
		return "", errors.New("device limit reached")
	}
	token, err := randomHex(32)
	if err != nil {
		return "", err
	}
	candidate := clone(h.state)
	candidate.Devices[id] = deviceRecord{Device: Device{ID: id, Name: strings.TrimSpace(name), Kind: kind, Scopes: allowed}, TokenHash: digest(token)}
	if err = h.persist(candidate); err != nil {
		return "", err
	}
	h.signalLocked()
	return token, nil
}

func (h *Hub) Revoke(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("hub closed")
	}
	record, exists := h.state.Devices[id]
	if !exists {
		return errors.New("unknown device")
	}
	candidate := clone(h.state)
	record.Revoked = true
	candidate.Devices[id] = record
	events := candidate.Events[:0]
	for _, event := range candidate.Events {
		if event.From != id && event.To != id {
			events = append(events, event)
		}
	}
	candidate.Events = events
	if err := h.persist(candidate); err != nil {
		return err
	}
	h.signalLocked()
	return nil
}

func (h *Hub) info(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	reply(w, 200, map[string]any{"app": "OneSpace Hub", "version": Version, "protocolVersion": 1, "recording": h.recording(), "capabilities": []string{"clipboard.text", "media", "live.camera", "cloud.drive", "camera.recording.opt-in"}, "maxTextBytes": MaxTextBytes})
}
func (h *Hub) devices(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.authorizedLocked(r, ""); !ok {
		fail(w, 401, "device authentication required")
		return
	}
	result := []map[string]any{}
	now := time.Now()
	for _, d := range h.state.Devices {
		if d.Revoked {
			continue
		}
		result = append(result, map[string]any{"id": d.ID, "name": d.Name, "kind": d.Kind, "online": !d.LastSeen.IsZero() && now.Sub(d.LastSeen) < 120*time.Second, "lastSeen": d.LastSeen, "scopes": d.Scopes})
	}
	reply(w, 200, result)
}

func eventScope(kind string) string {
	switch kind {
	case "clipboard.text":
		return "clipboard"
	case "media":
		return "media"
	}
	return ""
}
func (h *Hub) pendingLocked(device Device, now time.Time) []Event {
	result := []Event{}
	bytes := 0
	for _, event := range h.state.Events {
		source, exists := h.state.Devices[event.From]
		scope := eventScope(event.Kind)
		if event.To == device.ID && now.Before(event.ExpiresAt) && device.Scopes[scope] && exists && !source.Revoked && source.Scopes[scope] {
			// Bound one response independently of the durable queue's aggregate quota.
			encoded, _ := json.Marshal(event)
			size := len(encoded) + 1
			if len(result) >= 16 || bytes+size > 512<<10 {
				break
			}
			result = append(result, event)
			bytes += size
		}
	}
	return result
}
func (h *Hub) events(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.poll(w, r)
	case http.MethodPost:
		h.post(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		fail(w, 405, "method not allowed")
	}
}
func (h *Hub) poll(w http.ResponseWriter, r *http.Request) {
	wait := 0
	if raw := r.URL.Query().Get("wait"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			fail(w, 400, "invalid wait")
			return
		}
		wait = n
	}
	if wait > 55 {
		wait = 55
	}
	if wait > 0 {
		h.mu.Lock()
		device, ok := h.authorizedLocked(r, "")
		if !ok || h.closed {
			h.mu.Unlock()
			fail(w, 401, "device authentication required")
			return
		}
		total := 0
		for _, count := range h.polls {
			total += count
		}
		if h.polls[device.ID] >= 2 || total >= 64 {
			h.mu.Unlock()
			fail(w, 429, "too many held polls; retry after backoff")
			return
		}
		h.polls[device.ID]++
		h.mu.Unlock()
		defer func() {
			h.mu.Lock()
			h.polls[device.ID]--
			if h.polls[device.ID] == 0 {
				delete(h.polls, device.ID)
			}
			h.mu.Unlock()
		}()
	} else if _, ok := h.Authorize(r, ""); !ok {
		fail(w, 401, "device authentication required")
		return
	}
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()
	for {
		h.mu.Lock()
		device, ok := h.checkLocked(r, "")
		if !ok || h.closed {
			h.mu.Unlock()
			fail(w, 401, "device authentication required")
			return
		}
		events := h.pendingLocked(device, time.Now().UTC())
		wake := h.wake
		h.mu.Unlock()
		if len(events) > 0 || wait == 0 {
			reply(w, 200, events)
			return
		}
		select {
		case <-wake:
			continue
		case <-deadline.C:
			// Check auth again even on timeout; never answer a revoked waiter as ready.
			if _, ok := h.CheckPermission(r, ""); !ok {
				fail(w, 401, "device authentication required")
				return
			}
			reply(w, 200, []Event{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Hub) post(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.Authorize(r, ""); !ok {
		fail(w, 401, "device authentication required")
		return
	}
	var request EventRequest
	body := http.MaxBytesReader(w, r.Body, MaxTextBytes+2048)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		fail(w, 400, "invalid or oversized event")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, 400, "one JSON event required")
		return
	}
	scope := eventScope(request.Kind)
	if scope == "" || !validDevice.MatchString(request.To) || len(request.Text) > MaxTextBytes {
		fail(w, 400, "invalid event kind, destination or size")
		return
	}
	if request.ID != "" && !validRequestID.MatchString(request.ID) {
		fail(w, 400, "invalid idempotency id")
		return
	}
	if request.Kind == "clipboard.text" && (request.Text == "" || request.Action != "") {
		fail(w, 400, "clipboard text required")
		return
	}
	if request.Kind == "media" {
		if request.Text != "" || (request.Action != "play_pause" && request.Action != "next" && request.Action != "previous" && request.Action != "stop") {
			fail(w, 400, "invalid media action")
			return
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	device, ok := h.authorizedLocked(r, scope)
	if !ok || h.closed {
		fail(w, 403, "event permission denied")
		return
	}
	destination, exists := h.state.Devices[request.To]
	if !exists || destination.Revoked || !destination.Scopes[scope] || request.To == device.ID {
		fail(w, 403, "destination permission denied")
		return
	}
	now := time.Now().UTC()
	candidate := clone(h.state)
	prune(&candidate, now)
	encoded, _ := json.Marshal(struct{ To, Kind, Text, Action string }{request.To, request.Kind, request.Text, request.Action})
	sum := digest(string(encoded))
	key := ""
	if request.ID != "" {
		key = device.ID + ":" + request.ID
		if previous, exists := candidate.Receipts[key]; exists {
			if previous.Digest != sum {
				fail(w, 409, "idempotency id already used for different content")
				return
			}
			for _, event := range candidate.Events {
				if event.ID == previous.EventID {
					reply(w, 200, event)
					return
				}
			}
			reply(w, 200, map[string]any{"id": previous.EventID, "duplicate": true, "queued": false})
			return
		}
	}
	if request.Kind == "clipboard.text" {
		// Clipboard is a short-lived latest-value stream, not a history queue.
		// Preserve the idempotency receipt but supersede unsent older copies from
		// this sender to this recipient so a reconnect cannot paste a stale chain.
		events := candidate.Events[:0]
		for _, event := range candidate.Events {
			if event.Kind != "clipboard.text" || event.From != device.ID || event.To != request.To {
				events = append(events, event)
			}
		}
		candidate.Events = events
	}
	if len(candidate.Events) >= MaxPending || len(candidate.Receipts) >= maxReceipts {
		fail(w, 429, "hub queue is full; retry after backoff")
		return
	}
	id, err := randomHex(16)
	if err != nil {
		fail(w, 500, "cannot create event")
		return
	}
	ttl := 60 * time.Second
	if request.Kind == "media" {
		ttl = 15 * time.Second
	}
	event := Event{ID: id, From: device.ID, To: request.To, Kind: request.Kind, Text: request.Text, Action: request.Action, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	candidate.Events = append(candidate.Events, event)
	if key == "" {
		key = device.ID + ":auto:" + id
	}
	candidate.Receipts[key] = receipt{EventID: id, To: request.To, Digest: sum, ExpiresAt: now.Add(5 * time.Minute)}
	if err = h.persist(candidate); err != nil {
		fail(w, 507, "cannot persist event within storage quota")
		return
	}
	h.signalLocked()
	reply(w, 201, event)
}

func (h *Hub) ack(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/hub/v1/events/")
	if !strings.HasSuffix(suffix, "/ack") {
		fail(w, 404, "event endpoint missing")
		return
	}
	id := strings.TrimSuffix(suffix, "/ack")
	if !validEventID.MatchString(id) {
		fail(w, 400, "invalid event id")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	device, ok := h.authorizedLocked(r, "")
	if !ok || h.closed {
		fail(w, 401, "device authentication required")
		return
	}
	candidate := clone(h.state)
	found := false
	events := candidate.Events[:0]
	for _, event := range candidate.Events {
		if event.ID == id && event.To == device.ID {
			found = true
			continue
		}
		events = append(events, event)
	}
	if !found {
		for _, entry := range candidate.Receipts {
			if entry.EventID == id && entry.To == device.ID && time.Now().Before(entry.ExpiresAt) {
				reply(w, 200, map[string]bool{"received": true})
				return
			}
		}
		fail(w, 404, "event missing")
		return
	}
	candidate.Events = events
	if err := h.persist(candidate); err != nil {
		fail(w, 507, "cannot persist acknowledgement")
		return
	}
	h.signalLocked()
	reply(w, 200, map[string]bool{"received": true})
}

func (h *Hub) health(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if _, ok := h.Authorize(r, ""); !ok {
		fail(w, 401, "device authentication required")
		return
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	h.mu.Lock()
	pending := 0
	now := time.Now().UTC()
	for _, event := range h.state.Events {
		if now.Before(event.ExpiresAt) {
			pending++
		}
	}
	healthy := h.storageHealthy
	h.mu.Unlock()
	reply(w, 200, map[string]any{"measuredAt": now, "uptimeSeconds": time.Since(h.started).Seconds(), "goHeapBytes": memory.HeapAlloc, "goroutines": runtime.NumGoroutine(), "pendingEvents": pending, "recording": h.recording(), "powerStatus": "not measured", "storageHealthy": healthy})
}

// Shutdown is for embedding services: it wakes blocked polls before shutdown.
func (h *Hub) Shutdown(ctx context.Context, server *http.Server) error {
	closeErr := h.Close()
	err := server.Shutdown(ctx)
	if err != nil {
		return err
	}
	return closeErr
}

// SetScopes changes only the explicitly selected hub device permissions. It
// preserves the existing token, certificate, pairing, and revoked status.
func (h *Hub) SetScopes(id string, scopes []string) error {
	allowed := map[string]bool{}
	for _, s := range scopes {
		if s != "clipboard" && s != "media" && s != "camera" && s != "cloud" && s != "recording" {
			return errors.New("unknown scope")
		}
		allowed[s] = true
	}
	if len(allowed) == 0 {
		return errors.New("at least one explicit scope required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("hub closed")
	}
	record, ok := h.state.Devices[id]
	if !ok {
		return errors.New("unknown device")
	}
	if record.Revoked {
		return errors.New("revoked device cannot be granted permissions")
	}
	candidate := clone(h.state)
	record.Scopes = allowed
	candidate.Devices[id] = record
	if err := h.persist(candidate); err != nil {
		return err
	}
	h.signalLocked()
	return nil
}
