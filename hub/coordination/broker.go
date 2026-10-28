// Package coordination implements the OneSpace signaling broker.
// It carries authenticated, bounded SDP/ICE messages only; it never relays
// camera media or accepts arbitrary upstream connections.
package coordination

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	prefix             = "/v1/coord/"
	maxSessions        = 128
	maxMessage         = 256 << 10
	maxQueue           = 32
	pollWait           = 55 * time.Second
	sessionTTL         = 15 * time.Minute
	maxDeviceLen       = 64
	maxRequestAttempts = 8
)

type Authenticate func(*http.Request) (string, bool)
type AllowTarget func(string, string) bool

type Broker struct {
	mu           sync.Mutex
	sessions     map[string]*session
	requests     map[string][]string
	authenticate Authenticate
	allowTarget  AllowTarget
	closed       bool
}

func (b *Broker) requestsFor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	target, ok := b.authenticate(r)
	if !ok {
		writeError(w, http.StatusForbidden, "authenticated device required")
		return
	}
	b.mu.Lock()
	ids := append([]string(nil), b.requests[target]...)
	result := make([]createResponse, 0, len(ids))
	for _, id := range ids {
		if s := b.sessions[id]; s != nil && time.Now().Before(s.expires) {
			if s.attempts[target] >= maxRequestAttempts {
				continue
			}
			s.attempts[target]++
			result = append(result, createResponse{Session: s.id, Viewer: s.viewer, Target: s.target, Expires: s.expires})
		}
	}
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

type session struct {
	id       string
	viewer   string
	target   string
	expires  time.Time
	messages map[string][]Message
	seen     map[string]map[string]struct{}
	attempts map[string]int
	notify   chan struct{}
}

type Message struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	From string `json:"from"`
	Body string `json:"body"`
}

type createRequest struct {
	Viewer string `json:"viewer"`
	Target string `json:"target"`
}

type createResponse struct {
	Session string    `json:"session"`
	Viewer  string    `json:"viewer"`
	Target  string    `json:"target"`
	Expires time.Time `json:"expires"`
}

type messageRequest struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	Body string `json:"body"`
}

func New(mux *http.ServeMux, authenticate Authenticate, allowTarget AllowTarget) (*Broker, error) {
	if mux == nil || authenticate == nil || allowTarget == nil {
		return nil, errors.New("coordination mux and authorization are required")
	}
	b := &Broker{sessions: make(map[string]*session), requests: make(map[string][]string), authenticate: authenticate, allowTarget: allowTarget}
	mux.HandleFunc(prefix+"sessions", b.create)
	mux.HandleFunc(prefix+"requests", b.requestsFor)
	mux.HandleFunc(prefix+"requests/", b.acknowledgeRequest)
	mux.HandleFunc(prefix+"sessions/", b.sessionRoute)
	return b, nil
}

func (b *Broker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	for _, s := range b.sessions {
		close(s.notify)
	}
	b.sessions = map[string]*session{}
	b.requests = map[string][]string{}
	b.mu.Unlock()
}

func (b *Broker) create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input createRequest
	if err := decodeJSON(w, r, &input); err != nil {
		return
	}
	caller, ok := b.authenticate(r)
	if !ok || input.Viewer != caller || !validDevice(input.Target) || input.Viewer == input.Target || !b.allowTarget(caller, input.Target) {
		writeError(w, http.StatusBadRequest, "invalid session participants")
		return
	}
	idBytes := make([]byte, 24)
	if _, err := rand.Read(idBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "cannot allocate session")
		return
	}
	now := time.Now()
	s := &session{
		id:       hex.EncodeToString(idBytes),
		viewer:   input.Viewer,
		target:   input.Target,
		expires:  now.Add(sessionTTL),
		messages: map[string][]Message{input.Viewer: {}, input.Target: {}},
		seen:     map[string]map[string]struct{}{input.Viewer: {}, input.Target: {}},
		attempts: map[string]int{},
		notify:   make(chan struct{}, 1),
	}
	b.mu.Lock()
	if b.closed || len(b.sessions) >= maxSessions {
		b.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "coordination capacity reached")
		return
	}
	b.sessions[s.id] = s
	b.requests[input.Target] = append(b.requests[input.Target], s.id)
	b.mu.Unlock()
	writeJSON(w, http.StatusCreated, createResponse{Session: s.id, Expires: s.expires})
}

func (b *Broker) sessionRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, prefix+"sessions/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "unknown coordination route")
		return
	}
	id, operation := parts[0], parts[1]
	switch operation {
	case "messages":
		b.postMessage(w, r, id)
	case "poll":
		b.poll(w, r, id)
	case "close":
		b.closeSession(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "unknown coordination operation")
	}
}

func (b *Broker) postMessage(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sender, ok := b.authenticate(r)
	if !ok || (r.URL.Query().Get("from") != "" && r.URL.Query().Get("from") != sender) {
		writeError(w, http.StatusForbidden, "authenticated sender required")
		return
	}
	var input messageRequest
	if err := decodeJSON(w, r, &input); err != nil {
		return
	}
	if input.Type != "offer" && input.Type != "answer" && input.Type != "candidate" && input.Type != "close" {
		writeError(w, http.StatusBadRequest, "unsupported signaling message")
		return
	}
	if input.Body == "" || len(input.Body) > maxMessage {
		writeError(w, http.StatusRequestEntityTooLarge, "signaling message exceeds limit")
		return
	}
	if input.ID == "" {
		input.ID = messageID(sender, input.Type, input.Body)
	}
	if !validMessageID(input.ID) {
		writeError(w, http.StatusBadRequest, "invalid signaling message id")
		return
	}
	b.mu.Lock()
	s, ok := b.sessions[id]
	if !ok || time.Now().After(s.expires) {
		b.mu.Unlock()
		writeError(w, http.StatusNotFound, "coordination session missing")
		return
	}
	if sender != s.viewer && sender != s.target {
		b.mu.Unlock()
		writeError(w, http.StatusForbidden, "sender is not a session participant")
		return
	}
	recipient := s.viewer
	if sender == s.viewer {
		recipient = s.target
	}
	if _, duplicate := s.seen[recipient][input.ID]; duplicate {
		b.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(s.messages[recipient]) >= maxQueue {
		b.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "signaling queue is full")
		return
	}
	s.seen[recipient][input.ID] = struct{}{}
	s.messages[recipient] = append(s.messages[recipient], Message{ID: input.ID, Type: input.Type, From: sender, Body: input.Body})
	select {
	case s.notify <- struct{}{}:
	default:
	}
	b.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func (b *Broker) acknowledgeRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	target, ok := b.authenticate(r)
	if !ok {
		writeError(w, http.StatusForbidden, "authenticated device required")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix+"requests/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid request id")
		return
	}
	b.mu.Lock()
	s, exists := b.sessions[id]
	if !exists || s.target != target || time.Now().After(s.expires) {
		b.mu.Unlock()
		writeError(w, http.StatusNotFound, "coordination session missing")
		return
	}
	ids := b.requests[target]
	filtered := ids[:0]
	for _, candidate := range ids {
		if candidate != id {
			filtered = append(filtered, candidate)
		}
	}
	b.requests[target] = filtered
	b.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (b *Broker) poll(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	recipient, ok := b.authenticate(r)
	if !ok || (r.URL.Query().Get("for") != "" && r.URL.Query().Get("for") != recipient) {
		writeError(w, http.StatusForbidden, "authenticated recipient required")
		return
	}
	deadline := time.NewTimer(pollWait)
	defer deadline.Stop()
	for {
		b.mu.Lock()
		s, ok := b.sessions[id]
		if !ok || time.Now().After(s.expires) {
			b.mu.Unlock()
			writeError(w, http.StatusNotFound, "coordination session missing")
			return
		}
		if recipient != s.viewer && recipient != s.target {
			b.mu.Unlock()
			writeError(w, http.StatusForbidden, "recipient is not a session participant")
			return
		}
		queue := append([]Message(nil), s.messages[recipient]...)
		s.messages[recipient] = nil
		b.mu.Unlock()
		if len(queue) > 0 {
			writeJSON(w, http.StatusOK, queue)
			return
		}
		select {
		case <-s.notify:
		case <-deadline.C:
			writeJSON(w, http.StatusOK, []Message{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (b *Broker) closeSession(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	requester, ok := b.authenticate(r)
	if !ok || (r.URL.Query().Get("by") != "" && r.URL.Query().Get("by") != requester) {
		writeError(w, http.StatusForbidden, "authenticated requester required")
		return
	}
	b.mu.Lock()
	s, ok := b.sessions[id]
	if !ok {
		b.mu.Unlock()
		writeError(w, http.StatusNotFound, "coordination session missing")
		return
	}
	if requester != s.viewer && requester != s.target {
		b.mu.Unlock()
		writeError(w, http.StatusForbidden, "requester is not a session participant")
		return
	}
	delete(b.sessions, id)
	for target, ids := range b.requests {
		filtered := ids[:0]
		for _, candidate := range ids {
			if candidate != id {
				filtered = append(filtered, candidate)
			}
		}
		b.requests[target] = filtered
	}
	close(s.notify)
	b.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func validDevice(value string) bool {
	if len(value) == 0 || len(value) > maxDeviceLen {
		return false
	}

	for _, c := range value {
		if c != '-' && c != '_' && (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

func validMessageID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func messageID(sender, kind, body string) string {
	sum := sha256.Sum256([]byte(sender + "\x00" + kind + "\x00" + body))
	return hex.EncodeToString(sum[:])
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxMessage+4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
