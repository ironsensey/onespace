package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testHubConfig(server *httptest.Server) HubConfig {
	hash := sha256.Sum256(server.Certificate().Raw)
	return HubConfig{Address: server.URL, Fingerprint: hex.EncodeToString(hash[:]), Token: strings.Repeat("a", 64), Enabled: true, ClipboardMode: "lan"}
}

func TestHubPinnedClientRejectsWrongIdentityBeforeAuthentication(t *testing.T) {
	var authenticated atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			authenticated.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	config := testHubConfig(server)
	config.Fingerprint = strings.Repeat("0", 64)
	client, err := newPinnedHubClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if response, err := hubRequest(context.Background(), client, config, "GET", "/hub/v1/info", "", nil); err == nil {
		response.Body.Close()
		t.Fatal("wrong certificate pin was accepted")
	}
	if authenticated.Load() != 0 {
		t.Fatal("credential reached untrusted endpoint")
	}
	config = testHubConfig(server)
	client, err = newPinnedHubClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := hubRequest(context.Background(), client, config, "GET", "/hub/v1/info", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if authenticated.Load() != 1 {
		t.Fatal("correctly pinned endpoint not authenticated")
	}
}

func TestHubClientDoesNotFollowCredentialRedirect(t *testing.T) {
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer destination.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer server.Close()
	config := testHubConfig(server)
	client, err := newPinnedHubClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if response, err := hubRequest(context.Background(), client, config, "GET", "/hub/v1/info", "", nil); err == nil {
		response.Body.Close()
		t.Fatal("redirect accepted as successful hub response")
	}
	if leaked.Load() != 0 {
		t.Fatal("redirect reached external endpoint")
	}
}

func TestHubConfigRejectsPublicPlaintextAndBroadOverlayRoutes(t *testing.T) {
	good := HubConfig{Address: "https://100.64.0.2:47603", Fingerprint: strings.Repeat("a", 64), Token: strings.Repeat("b", 64), Enabled: true, ClipboardMode: "lan"}
	for _, address := range []string{"http://100.64.0.2:47603", "https://user:password@example.com", "https://example.com/path", "https://example.com?token=secret"} {
		config := good
		config.Address = address
		if validateHubConfig(config) == nil {
			t.Fatalf("unsafe address accepted: %s", address)
		}
	}
	for _, cidr := range []string{"0.0.0.0/0", "100.0.0.0/8", "192.168.0.0/16", "::/0", "fd00::/8", "invalid"} {
		config := good
		config.OverlayCIDRs = []string{cidr}
		if validateHubConfig(config) == nil {
			t.Fatalf("broad/non-overlay route accepted: %s", cidr)
		}
	}
	for _, cidr := range []string{"100.64.0.0/10", "100.100.100.100/32", "fd7a:115c:a1e0::/48", "fd7a:115c:a1e0::1/128"} {
		config := good
		config.OverlayCIDRs = []string{cidr}
		if err := validateHubConfig(config); err != nil {
			t.Fatalf("valid private-overlay route rejected: %s: %v", cidr, err)
		}
	}
}

func TestHubOverlayAdmissionIsExplicitAndScoped(t *testing.T) {
	state, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(state, "fixture")
	if app.hub.allowsOverlay(net.ParseIP("100.100.100.100")) {
		t.Fatal("overlay permitted by default")
	}
	app.hub.cfg.OverlayCIDRs = []string{"100.100.100.100/32"}
	if !app.hub.allowsOverlay(net.ParseIP("100.100.100.100")) {
		t.Fatal("explicit peer not admitted")
	}
	if app.hub.allowsOverlay(net.ParseIP("100.100.100.101")) {
		t.Fatal("different peer admitted")
	}
}

func TestHubSettingsRequireLocalKeyAndNeverReturnToken(t *testing.T) {
	state, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(state, "fixture")
	app.hub.cfg.Token = "sensitive-fixture-hub-token"
	endpoint := httptest.NewServer(http.HandlerFunc(app.local))
	defer endpoint.Close()
	for _, key := range []string{"", state.deskToken} {
		request, _ := http.NewRequest("GET", endpoint.URL+"/local/hub/config", nil)
		if key != "" {
			request.Header.Set("X-Local-Key", key)
		}
		response, err := endpoint.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if strings.Contains(string(body), app.hub.cfg.Token) {
			t.Fatal("raw hub token returned to dashboard")
		}
		if key == "" {
			if response.StatusCode != 403 {
				t.Fatal("settings available without local key")
			}
			continue
		}
		if response.StatusCode != 200 {
			t.Fatal("authorized settings request failed")
		}
		var result struct {
			HasToken bool      `json:"hasToken"`
			Config   HubConfig `json:"config"`
		}
		if err = json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		if !result.HasToken || result.Config.Token != "" {
			t.Fatal("incorrect redacted credential status")
		}
	}
	request, _ := http.NewRequest("POST", endpoint.URL+"/local/hub/camera/whep", strings.NewReader("v=0\r\n"))
	response, err := endpoint.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("camera signaling lacks local authorization")
	}
}

func TestExpiredHubMediaIsAcknowledgedWithoutExecution(t *testing.T) {
	var acknowledgements atomic.Int32
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/hub/v1/events/expired-media/ack" {
			t.Error("unexpected event action")
		}
		acknowledgements.Add(1)
		io.WriteString(w, `{"received":true}`)
	}))
	defer endpoint.Close()
	config := testHubConfig(endpoint)
	config.AllowMedia = true
	client, err := newPinnedHubClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	state, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(state, "fixture")
	event := HubEvent{ID: "expired-media", Kind: "media", Action: "invalid-must-never-execute", ExpiresAt: time.Now().Add(-time.Second)}
	if err = app.hub.process(context.Background(), client, config, event); err != nil {
		t.Fatal(err)
	}
	if acknowledgements.Load() != 1 {
		t.Fatal("expired media not acknowledged")
	}
	if len(app.hub.receipts) != 0 {
		t.Fatal("expired media recorded as an executed side effect")
	}
}

func TestDisabledHubCannotBypassOverlayAdmissionPolicy(t *testing.T) {
	config := HubConfig{Enabled: false, OverlayCIDRs: []string{"0.0.0.0/0"}}
	if validateHubConfig(config) == nil {
		t.Fatal("disabled profile accepted broad source admission")
	}
}

func TestHubIdentityChangeRequiresReplacementCredential(t *testing.T) {
	state, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(state, "fixture")
	original := HubConfig{Address: "https://100.64.0.2:47603", Fingerprint: strings.Repeat("a", 64), Token: strings.Repeat("b", 64), Enabled: true, ClipboardMode: "lan"}
	app.hub.cfg = original
	endpoint := httptest.NewServer(http.HandlerFunc(app.local))
	defer endpoint.Close()
	for _, change := range []string{"address", "pin"} {
		next := original
		next.Token = ""
		if change == "address" {
			next.Address = "https://100.64.0.3:47603"
		} else {
			next.Fingerprint = strings.Repeat("c", 64)
		}
		body, _ := json.Marshal(next)
		request, _ := http.NewRequest("POST", endpoint.URL+"/local/hub/config", strings.NewReader(string(body)))
		request.Header.Set("X-Local-Key", state.deskToken)
		response, err := endpoint.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("new %s accepted without replacement token", change)
		}
		if app.hub.snapshot().Token != original.Token || app.hub.snapshot().Address != original.Address {
			t.Fatal("rejected profile mutated live credentials")
		}
	}
}

func TestCancelledHubEventDoesNotExecuteOrAcknowledge(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer endpoint.Close()
	config := testHubConfig(endpoint)
	config.AllowMedia = true
	client, err := newPinnedHubClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	state, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(state, "fixture")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	event := HubEvent{ID: "cancelled-media", Kind: "media", Action: "invalid-must-not-execute", ExpiresAt: time.Now().Add(time.Minute)}
	if err = app.hub.process(ctx, client, config, event); err == nil {
		t.Fatal("cancelled event accepted")
	}
	if len(app.hub.receipts) != 0 || calls.Load() != 0 {
		t.Fatal("cancelled event had a side effect or ACK")
	}
}

func sendFixture(t *testing.T, handler http.HandlerFunc) (*HubBridge, HubConfig, *http.Client, func()) {
	t.Helper()
	endpoint := httptest.NewTLSServer(handler)
	config := testHubConfig(endpoint)
	config.Target = "phone"
	config.ClipboardMode = "hub"
	client, err := newPinnedHubClient(config)
	if err != nil {
		endpoint.Close()
		t.Fatal(err)
	}
	state, err := NewState(t.TempDir())
	if err != nil {
		client.CloseIdleConnections()
		endpoint.Close()
		t.Fatal(err)
	}
	bridge := newApp(state, "fixture").hub
	bridge.cfg = config
	return bridge, config, client, func() { client.CloseIdleConnections(); endpoint.Close() }
}
func queuedFixture(c HubConfig, id string) HubEvent {
	return HubEvent{Route: c.Fingerprint + ":" + c.Token, ID: id, To: c.Target, Kind: "clipboard.text", Text: "fixture text", ExpiresAt: time.Now().Add(time.Minute)}
}
func waitSender(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sender did not stop on cancellation")
	}
}

func TestHubSenderRetriesTransientFailureWithStableID(t *testing.T) {
	calls := make(chan string, 3)
	var count atomic.Int32
	bridge, config, client, closeFixture := sendFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		calls <- request.ID
		if count.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(201)
		io.WriteString(w, `{}`)
	})
	defer closeFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); bridge.sendLoop(ctx, client, config) }()
	bridge.outgoing <- queuedFixture(config, "retry-stable-id")
	for i := 0; i < 2; i++ {
		select {
		case id := <-calls:
			if id != "retry-stable-id" {
				t.Fatal("retry changed idempotency key")
			}
		case <-time.After(4 * time.Second):
			t.Fatal("transient event was not retried")
		}
	}
	cancel()
	waitSender(t, done)
}

func TestHubSenderDoesNotRetryPermanentDenial(t *testing.T) {
	calls := make(chan struct{}, 3)
	bridge, config, client, closeFixture := sendFixture(t, func(w http.ResponseWriter, r *http.Request) { calls <- struct{}{}; w.WriteHeader(403) })
	defer closeFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); bridge.sendLoop(ctx, client, config) }()
	bridge.outgoing <- queuedFixture(config, "denied-event-id")
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("initial request missing")
	}
	select {
	case <-calls:
		t.Fatal("permanent permission denial was retried")
	case <-time.After(1800 * time.Millisecond):
	}
	cancel()
	waitSender(t, done)
}

func TestHubNewCopySupersedesFailedPendingCopy(t *testing.T) {
	calls := make(chan string, 3)
	var count atomic.Int32
	bridge, config, client, closeFixture := sendFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		calls <- request.ID
		if count.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(201)
		io.WriteString(w, `{}`)
	})
	defer closeFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); bridge.sendLoop(ctx, client, config) }()
	bridge.outgoing <- queuedFixture(config, "old-pending-copy")
	select {
	case id := <-calls:
		if id != "old-pending-copy" {
			t.Fatal("unexpected first copy")
		}
	case <-time.After(time.Second):
		t.Fatal("initial request missing")
	}
	bridge.outgoing <- queuedFixture(config, "new-current-copy")
	select {
	case id := <-calls:
		if id != "new-current-copy" {
			t.Fatal("stale copy retried ahead of newer copy")
		}
	case <-time.After(time.Second):
		t.Fatal("new copy waited for stale retry backoff")
	}
	cancel()
	waitSender(t, done)
}

func TestHubClipboardQueueKeepsOnlyLatestAndDoesNotSerializeRouteSecret(t *testing.T) {
	bridge, config, _, closeFixture := sendFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	defer closeFixture()
	if !bridge.queueText("first") || !bridge.queueText("second") {
		t.Fatal("configured hub route not selected")
	}
	if len(bridge.outgoing) != 1 {
		t.Fatal("clipboard queue is not coalesced to one item")
	}
	event := <-bridge.outgoing
	if event.Text != "second" {
		t.Fatal("queue retained stale copy")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), config.Token) || strings.Contains(string(payload), "Route") {
		t.Fatal("internal route secret serialized")
	}
}

func TestHubSenderCancellationInterruptsActiveRequest(t *testing.T) {
	started := make(chan struct{}, 1)
	bridge, config, client, closeFixture := sendFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		<-r.Context().Done()
	})
	defer closeFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); bridge.sendLoop(ctx, client, config) }()
	bridge.outgoing <- queuedFixture(config, "cancel-active-post")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request never started")
	}
	cancel()
	waitSender(t, done)
}
