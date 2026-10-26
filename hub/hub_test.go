package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCloudPermissionUpgradePreservesExistingToken(t *testing.T) {
	env := newTestHub(t)
	r := httptest.NewRequest("GET", "/hub/v1/cloud/status", nil)
	r.Header.Set("Authorization", "Bearer "+env.tokens["phone"])
	if _, ok := env.h.CheckPermission(r, "cloud"); ok {
		t.Fatal("new cloud permission silently enabled")
	}
	if err := env.h.SetScopes("phone", []string{"clipboard", "media", "camera", "cloud", "recording"}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"clipboard", "camera", "cloud", "recording"} {
		if _, ok := env.h.CheckPermission(r, scope); !ok {
			t.Fatal("existing token lost after explicit upgrade")
		}
	}
	if err := env.h.SetScopes("phone", []string{"camera"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.h.CheckPermission(r, "cloud"); ok {
		t.Fatal("cloud permission removal ineffective")
	}
	if err := env.h.Revoke("phone"); err != nil {
		t.Fatal(err)
	}
	if err := env.h.SetScopes("phone", []string{"cloud"}); err == nil {
		t.Fatal("revoked device resurrected")
	}
}

type testHub struct {
	h      *Hub
	server *httptest.Server
	tokens map[string]string
}

func newTestHub(t *testing.T) *testHub {
	t.Helper()
	h, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := &testHub{h: h, tokens: map[string]string{}}
	for _, device := range []struct {
		id     string
		scopes []string
	}{{"pc", []string{"clipboard", "media", "camera"}}, {"phone", []string{"clipboard", "media", "camera"}}, {"viewer", []string{"camera"}}} {
		token, err := h.Enroll(device.id, device.id, "linux", device.scopes)
		if err != nil {
			t.Fatal(err)
		}
		env.tokens[device.id] = token
	}
	env.server = httptest.NewTLSServer(h)
	t.Cleanup(func() { h.Close(); env.server.Close() })
	return env
}
func (e *testHub) call(t *testing.T, method, path, device string, body any) (int, []byte) {
	t.Helper()
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, e.server.URL+path, input)
	if err != nil {
		t.Fatal(err)
	}
	if token := e.tokens[device]; token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := e.server.Client()
	client.Timeout = 3 * time.Second
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, data
}
func queued(t *testing.T, data []byte) Event {
	t.Helper()
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if !validEventID.MatchString(event.ID) {
		t.Fatal("missing opaque event id")
	}
	return event
}

func TestAuthAndNoCredentialDisclosure(t *testing.T) {
	env := newTestHub(t)
	if code, _ := env.call(t, "GET", "/hub/v1/info", "", nil); code != 200 {
		t.Fatal(code)
	}
	if code, _ := env.call(t, "GET", "/hub/v1/devices", "", nil); code != 401 {
		t.Fatal(code)
	}
	code, data := env.call(t, "GET", "/hub/v1/devices", "phone", nil)
	if code != 200 {
		t.Fatal(code)
	}
	for _, token := range env.tokens {
		if strings.Contains(string(data), token) || strings.Contains(string(data), digest(token)) {
			t.Fatal("credential disclosed")
		}
	}
	if strings.Contains(string(data), "tokenHash") {
		t.Fatal("credential metadata disclosed")
	}
	if code, _ := env.call(t, "POST", "/hub/v1/events", "viewer", EventRequest{To: "phone", Kind: "clipboard.text", Text: "private"}); code != 403 {
		t.Fatal("camera-only source sent clipboard", code)
	}
	if code, _ := env.call(t, "POST", "/hub/v1/events", "phone", EventRequest{To: "viewer", Kind: "clipboard.text", Text: "private"}); code != 403 {
		t.Fatal("destination consent bypass", code)
	}
	if code, _ := env.call(t, "POST", "/hub/v1/events", "phone", EventRequest{To: "phone", Kind: "clipboard.text", Text: "loop"}); code != 403 {
		t.Fatal("self-delivery loop", code)
	}
}

func TestDurableDeliveryScopedAckAndIdempotency(t *testing.T) {
	env := newTestHub(t)
	request := EventRequest{ID: "d22a86b4-0229-4140-b046-442ddbf7aab6", To: "phone", Kind: "clipboard.text", Text: "Hello, नमस्ते 👋"}
	code, data := env.call(t, "POST", "/hub/v1/events", "pc", request)
	if code != 201 {
		t.Fatalf("post %d %s", code, data)
	}
	event := queued(t, data)
	if event.ExpiresAt.Sub(event.CreatedAt) != 60*time.Second {
		t.Fatal("incorrect clipboard TTL")
	}
	code, data = env.call(t, "POST", "/hub/v1/events", "pc", request)
	if code != 200 || queued(t, data).ID != event.ID {
		t.Fatal("duplicate request not stable")
	}
	request.Text = "changed"
	if code, _ = env.call(t, "POST", "/hub/v1/events", "pc", request); code != 409 {
		t.Fatal("idempotency key collision accepted")
	}
	code, data = env.call(t, "GET", "/hub/v1/events", "viewer", nil)
	if code != 200 || string(bytes.TrimSpace(data)) != "[]" {
		t.Fatal("unrelated device saw a clipboard event")
	}
	if code, _ = env.call(t, "POST", "/hub/v1/events/"+event.ID+"/ack", "viewer", nil); code != 404 {
		t.Fatal("foreign device acknowledged event")
	}
	// Real persisted state, not a mock, must contain the queued event and hashed credentials.
	stored, err := os.ReadFile(filepath.Join(env.h.dir, "hub.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved state
	if err = json.Unmarshal(stored, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Events) != 1 || saved.Events[0].ID != event.ID {
		t.Fatal("event not persisted")
	}
	for _, token := range env.tokens {
		if strings.Contains(string(stored), token) {
			t.Fatal("raw credential persisted")
		}
	}
	if code, _ = env.call(t, "POST", "/hub/v1/events/"+event.ID+"/ack", "phone", nil); code != 200 {
		t.Fatal(code)
	}
	if code, _ = env.call(t, "POST", "/hub/v1/events/"+event.ID+"/ack", "phone", nil); code != 200 {
		t.Fatal("ack not idempotent")
	}
	code, data = env.call(t, "GET", "/hub/v1/events", "phone", nil)
	if code != 200 || string(bytes.TrimSpace(data)) != "[]" {
		t.Fatal("acked event was replayed")
	}
	request.Text = "Hello, नमस्ते 👋"
	if code, _ = env.call(t, "POST", "/hub/v1/events", "pc", request); code != 200 {
		t.Fatal("post-ACK retry recreated event")
	}
	env.h.mu.Lock()
	count := len(env.h.state.Events)
	env.h.mu.Unlock()
	if count != 0 {
		t.Fatal("duplicate event persisted after ack")
	}
}

func TestLongPollWakesAndRevocationCancelsIt(t *testing.T) {
	env := newTestHub(t)
	startPoll := func() (<-chan int, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan int, 1)
		go func() {
			req, _ := http.NewRequestWithContext(ctx, "GET", env.server.URL+"/hub/v1/events?wait=3", nil)
			req.Header.Set("Authorization", "Bearer "+env.tokens["phone"])
			res, err := env.server.Client().Do(req)
			if err != nil {
				result <- 0
				return
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			result <- res.StatusCode
		}()
		// Wait for admission, not an arbitrary timer, before enqueue/revoke.
		until := time.Now().Add(time.Second)
		for time.Now().Before(until) {
			env.h.mu.Lock()
			active := env.h.polls["phone"] > 0
			env.h.mu.Unlock()
			if active {
				return result, cancel
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
		t.Fatal("poll not admitted")
		return nil, nil
	}
	result, cancel := startPoll()
	defer cancel()
	code, data := env.call(t, "POST", "/hub/v1/events", "pc", EventRequest{ID: "notification-1", To: "phone", Kind: "media", Action: "play_pause"})
	if code != 201 {
		t.Fatal(code)
	}
	event := queued(t, data)
	if event.ExpiresAt.Sub(event.CreatedAt) != 15*time.Second {
		t.Fatal("incorrect media TTL")
	}
	select {
	case code := <-result:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("enqueue did not wake waiter")
	}
	env.call(t, "POST", "/hub/v1/events/"+event.ID+"/ack", "phone", nil)
	result, cancel2 := startPoll()
	defer cancel2()
	if err := env.h.Revoke("phone"); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-result:
		if code != 401 {
			t.Fatalf("revoked poll returned %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation did not wake waiter")
	}
	if code, _ := env.call(t, "GET", "/hub/v1/devices", "phone", nil); code != 401 {
		t.Fatal("revoked token still works")
	}
}

func TestExpiryPurgesPlaintextFromDisk(t *testing.T) {
	env := newTestHub(t)
	code, data := env.call(t, "POST", "/hub/v1/events", "pc", EventRequest{ID: "expire-request", To: "phone", Kind: "clipboard.text", Text: "unique-sensitive-test-value"})
	if code != 201 {
		t.Fatal(code)
	}
	event := queued(t, data)
	if err := env.h.PurgeExpired(event.ExpiresAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(env.h.dir, "hub.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("unique-sensitive-test-value")) {
		t.Fatal("expired clipboard remained on disk")
	}
	if code, data := env.call(t, "GET", "/hub/v1/events", "phone", nil); code != 200 || string(bytes.TrimSpace(data)) != "[]" {
		t.Fatal("expired event delivered")
	}
}

func TestFailedPersistenceDoesNotAcknowledgeOrPublish(t *testing.T) {
	env := newTestHub(t)
	code, data := env.call(t, "POST", "/hub/v1/events", "pc", EventRequest{ID: "durable-ack", To: "phone", Kind: "clipboard.text", Text: "keep pending"})
	if code != 201 {
		t.Fatal(code)
	}
	event := queued(t, data)
	// A directory at the final snapshot path makes the atomic rename fail. The
	// transaction must leave live state untouched; restore it for test cleanup.
	path := filepath.Join(env.h.dir, "hub.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path); os.WriteFile(path, original, 0600) })
	if code, _ := env.call(t, "POST", "/hub/v1/events/"+event.ID+"/ack", "phone", nil); code != 507 {
		t.Fatal("failed ack persisted as success", code)
	}
	if code, _ := env.call(t, "POST", "/hub/v1/events", "pc", EventRequest{ID: "durable-post", To: "phone", Kind: "clipboard.text", Text: "must not publish"}); code != 507 {
		t.Fatal("failed enqueue reported success", code)
	}
	code, data = env.call(t, "GET", "/hub/v1/events", "phone", nil)
	var events []Event
	json.Unmarshal(data, &events)
	if code != 200 || len(events) != 1 || events[0].ID != event.ID {
		t.Fatal("in-memory transaction rollback failed")
	}
}

func TestResourceLimitsAndStrictRequestValidation(t *testing.T) {
	env := newTestHub(t)
	for _, request := range []EventRequest{{To: "phone", Kind: "clipboard.text", Text: strings.Repeat("x", MaxTextBytes+1)}, {To: "phone", Kind: "media", Action: "shutdown"}, {ID: "bad/id", To: "phone", Kind: "media", Action: "stop"}} {
		if code, _ := env.call(t, "POST", "/hub/v1/events", "pc", request); code != 400 {
			t.Fatal("invalid request accepted", code)
		}
	}
	env.h.mu.Lock()
	candidate := clone(env.h.state)
	for i := 0; i < MaxPending; i++ {
		candidate.Events = append(candidate.Events, Event{ID: strings.Repeat("a", 32), From: "pc", To: "phone", Kind: "media", Action: "stop", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)})
	}
	if err := env.h.persist(candidate); err != nil {
		env.h.mu.Unlock()
		t.Fatal(err)
	}
	env.h.mu.Unlock()
	if code, _ := env.call(t, "POST", "/hub/v1/events", "pc", EventRequest{To: "phone", Kind: "media", Action: "stop"}); code != 429 {
		t.Fatal("queue quota not enforced", code)
	}
	code, data := env.call(t, "GET", "/hub/v1/events", "phone", nil)
	var events []Event
	json.Unmarshal(data, &events)
	if code != 200 || len(events) > 16 {
		t.Fatal("poll batch unbounded")
	}
}

func TestLockAndRestartPreserveCredentialsAndIdentity(t *testing.T) {
	dir := t.TempDir()
	h, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	token, err := h.Enroll("phone", "Phone", "android", []string{"clipboard"})
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := Identity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Open(dir); err == nil {
		other.Close()
		t.Fatal("concurrent writer accepted")
	}
	h.Close()
	h, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	_, second, err := Identity(dir)
	if err != nil || first != second {
		t.Fatal("identity changed after restart")
	}
	req := httptest.NewRequest("GET", "https://localhost/hub/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if d, ok := h.Authorize(req, "clipboard"); !ok || d.ID != "phone" {
		t.Fatal("enrollment did not survive restart")
	}
	if _, err = h.Enroll("phone", "replacement", "android", []string{"clipboard"}); err == nil {
		t.Fatal("existing trust silently replaced")
	}
	path := filepath.Join(dir, "identity.key")
	if err = os.WriteFile(path, []byte("damaged key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Identity(dir); err == nil {
		t.Fatal("damaged identity regenerated")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "damaged key" {
		t.Fatal("damaged key overwritten")
	}
}

func TestTLSRequired(t *testing.T) {
	env := newTestHub(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://localhost/hub/v1/info", nil)
	env.h.ServeHTTP(recorder, req)
	if recorder.Code != 403 {
		t.Fatal("plaintext API allowed")
	}
}

func TestTrustedSourcesIncludeOverlayButNotPublicInternet(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::1", "192.168.0.106", "10.0.0.1", "172.31.0.1", "100.64.0.1", "100.127.255.254", "fd7a:115c:a1e0::1", "fd00::1"} {
		if !TrustedSource(net.ParseIP(address)) {
			t.Fatalf("trusted address rejected: %s", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "100.63.255.255", "100.128.0.1", "172.32.0.1", "0.0.0.0", "2001:4860:4860::8888", "invalid"} {
		if TrustedSource(net.ParseIP(address)) {
			t.Fatalf("public/invalid address accepted: %s", address)
		}
	}
}

func TestCoalescedClipboardOldAckDoesNotDelayLatest(t *testing.T) {
	env := newTestHub(t)
	code, data := env.call(t, "POST", "/hub/v1/events", "pc", EventRequest{ID: "first-copy-uuid", To: "phone", Kind: "clipboard.text", Text: "first"})
	if code != 201 {
		t.Fatal(code)
	}
	first := queued(t, data)
	code, data = env.call(t, "GET", "/hub/v1/events", "phone", nil)
	if code != 200 || !bytes.Contains(data, []byte(first.ID)) {
		t.Fatal("first copy not fetched")
	}
	code, data = env.call(t, "POST", "/hub/v1/events", "pc", EventRequest{ID: "second-copy-uuid", To: "phone", Kind: "clipboard.text", Text: "newest"})
	if code != 201 {
		t.Fatal(code)
	}
	second := queued(t, data)
	if code, _ := env.call(t, "POST", "/hub/v1/events/"+first.ID+"/ack", "viewer", nil); code != 404 {
		t.Fatal("foreign coalesced ACK accepted")
	}
	if code, _ := env.call(t, "POST", "/hub/v1/events/"+first.ID+"/ack", "phone", nil); code != 200 {
		t.Fatal("fetched superseded copy produced an ACK error")
	}
	code, data = env.call(t, "GET", "/hub/v1/events", "phone", nil)
	var events []Event
	json.Unmarshal(data, &events)
	if code != 200 || len(events) != 1 || events[0].ID != second.ID || events[0].Text != "newest" {
		t.Fatal("newest copy missing")
	}
}

func TestCameraRechecksDoNotInventPresence(t *testing.T) {
	env := newTestHub(t)
	req := httptest.NewRequest("GET", "https://localhost/hub/v1/camera/status", nil)
	req.Header.Set("Authorization", "Bearer "+env.tokens["viewer"])
	if _, ok := env.h.CheckPermission(req, "camera"); !ok {
		t.Fatal("camera permission unavailable")
	}
	env.h.mu.Lock()
	seen := env.h.state.Devices["viewer"].LastSeen
	env.h.mu.Unlock()
	if !seen.IsZero() {
		t.Fatal("pure policy check invented a heartbeat")
	}
	if _, ok := env.h.Authorize(req, "camera"); !ok {
		t.Fatal("camera authorization failed")
	}
	env.h.mu.Lock()
	seen = env.h.state.Devices["viewer"].LastSeen
	env.h.mu.Unlock()
	if seen.IsZero() {
		t.Fatal("real authenticated request failed to update presence")
	}
	if _, ok := env.h.CheckPermission(req, "camera"); !ok {
		t.Fatal("second policy check failed")
	}
	env.h.mu.Lock()
	after := env.h.state.Devices["viewer"].LastSeen
	env.h.mu.Unlock()
	if !seen.Equal(after) {
		t.Fatal("policy recheck fabricated later client activity")
	}
}

func TestEscapedClipboardBatchIsBoundedAndDeliverable(t *testing.T) {
	env := newTestHub(t)
	// The JSON encoder escapes '<'; count encoded bytes, not raw string length.
	env.h.mu.Lock()
	candidate := clone(env.h.state)
	for i := 0; i < 16; i++ {
		candidate.Events = append(candidate.Events, Event{ID: strings.Repeat("b", 32), From: "pc", To: "phone", Kind: "clipboard.text", Text: strings.Repeat("<", 16<<10), CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)})
	}
	if err := env.h.persist(candidate); err != nil {
		env.h.mu.Unlock()
		t.Fatal(err)
	}
	env.h.mu.Unlock()
	code, data := env.call(t, "GET", "/hub/v1/events", "phone", nil)
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	if code != 200 || len(events) == 0 || len(data) > 513<<10 {
		t.Fatal("valid escaped clipboard could not be fetched within response limit")
	}
}

var benchmarkPending []Event

func BenchmarkIdleQueueSnapshot(b *testing.B) {
	h := &Hub{state: state{Devices: map[string]deviceRecord{}, Events: []Event{}}}
	device := Device{ID: "phone", Scopes: map[string]bool{"clipboard": true}}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkPending = h.pendingLocked(device, now)
	}
}

func BenchmarkBoundedPendingQueueSnapshot(b *testing.B) {
	h := &Hub{state: state{Devices: map[string]deviceRecord{"pc": {Device: Device{ID: "pc", Scopes: map[string]bool{"clipboard": true}}}}, Events: []Event{}}}
	now := time.Now()
	for i := 0; i < MaxPending; i++ {
		h.state.Events = append(h.state.Events, Event{ID: strings.Repeat("a", 32), From: "pc", To: "phone", Kind: "clipboard.text", Text: strings.Repeat("x", 1024), CreatedAt: now, ExpiresAt: now.Add(time.Minute)})
	}
	device := Device{ID: "phone", Scopes: map[string]bool{"clipboard": true}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkPending = h.pendingLocked(device, now)
	}
}
