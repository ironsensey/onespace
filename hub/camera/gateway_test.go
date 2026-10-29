package camera

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, upstream http.Handler, max int) (*Gateway, *http.ServeMux, *atomic.Bool) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	allowed := &atomic.Bool{}
	allowed.Store(true)
	mux := http.NewServeMux()
	gateway, err := Register(mux, Config{Upstream: server.URL + "/camera/whep", MaxSessions: max}, func(r *http.Request) (string, bool) {
		token := r.Header.Get("Authorization")
		if !allowed.Load() || (token != "Bearer first" && token != "Bearer second") {
			return "", false
		}
		return token, true
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	return gateway, mux, allowed
}
func request(mux http.Handler, method, path, token string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/sdp")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestGatewayAuthorizationAndOwnership(t *testing.T) {
	var deletes atomic.Int32
	_, mux, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("hub credential leaked to MediaMTX")
		}
		if r.Method == "DELETE" {
			deletes.Add(1)
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Location", "/camera/whep/session-one")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("v=0\r\n"))
	}), 2)
	if w := request(mux, "POST", prefix+"whep", "", strings.NewReader("v=0")); w.Code != 403 {
		t.Fatalf("unauthorized %d", w.Code)
	}
	w := request(mux, "POST", prefix+"whep", "first", strings.NewReader("v=0"))
	if w.Code != 201 || !strings.HasPrefix(w.Header().Get("Location"), prefix+"session/") {
		t.Fatalf("offer %d %s", w.Code, w.Body.String())
	}
	path := w.Header().Get("Location")
	if w = request(mux, "DELETE", path, "second", nil); w.Code != 404 {
		t.Fatalf("other owner removed session: %d", w.Code)
	}
	if w = request(mux, "DELETE", path, "first", nil); w.Code != 204 || deletes.Load() != 1 {
		t.Fatalf("delete %d count %d", w.Code, deletes.Load())
	}
}

func TestGatewayRejectsLargeSDPAndCapsViewers(t *testing.T) {
	_, mux, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Location", "/camera/whep/s1")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("v=0"))
	}), 1)
	if w := request(mux, "POST", prefix+"whep", "first", bytes.NewReader(make([]byte, maxSDP+1))); w.Code != 413 {
		t.Fatalf("oversized %d", w.Code)
	}
	if w := request(mux, "POST", prefix+"whep", "first", strings.NewReader("v=0")); w.Code != 201 {
		t.Fatalf("first viewer %d", w.Code)
	}
	if w := request(mux, "POST", prefix+"whep", "second", strings.NewReader("v=0")); w.Code != 429 {
		t.Fatalf("unbounded viewers %d", w.Code)
	}
}

func TestGatewayRefusesRedirectAndForeignSessionLocation(t *testing.T) {
	var external atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { external.Add(1) }))
	defer other.Close()
	for _, mode := range []string{"redirect", "location"} {
		t.Run(mode, func(t *testing.T) {
			_, mux, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", other.URL+"/camera/whep/foreign")
				if mode == "redirect" {
					w.WriteHeader(307)
				} else {
					w.WriteHeader(201)
					_, _ = w.Write([]byte("v=0"))
				}
			}), 1)
			if w := request(mux, "POST", prefix+"whep", "first", strings.NewReader("v=0")); w.Code != 502 {
				t.Fatalf("untrusted redirect/location accepted %d", w.Code)
			}
		})
	}
	if external.Load() != 0 {
		t.Fatal("gateway made an external request")
	}
}

func TestGatewayReaperClosesRevokedAndExpiredViewers(t *testing.T) {
	var deletes atomic.Int32
	g, mux, allowed := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deletes.Add(1)
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Location", "/camera/whep/s1")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("v=0"))
	}), 2)
	if w := request(mux, "POST", prefix+"whep", "first", strings.NewReader("v=0")); w.Code != 201 {
		t.Fatal(w.Code)
	}
	allowed.Store(false)
	g.sweep()
	if deletes.Load() != 1 || len(g.sessions) != 0 {
		t.Fatal("revoked stream remained open")
	}
	allowed.Store(true)
	if w := request(mux, "POST", prefix+"whep", "first", strings.NewReader("v=0")); w.Code != 201 {
		t.Fatal(w.Code)
	}
	g.mu.Lock()
	for _, s := range g.sessions {
		s.expires = time.Now().Add(-time.Second)
	}
	g.mu.Unlock()
	g.sweep()
	if deletes.Load() != 2 || len(g.sessions) != 0 {
		t.Fatal("expired stream remained open")
	}
}

func TestGatewayOnlyAcceptsLoopbackUpstream(t *testing.T) {
	for _, u := range []string{"http://example.com/camera/whep", "https://127.0.0.1/camera/whep", "http://127.0.0.1/camera/whep?url=http://example.com", "http://user:pass@127.0.0.1/camera/whep"} {
		if _, err := Register(http.NewServeMux(), Config{Upstream: u}, func(*http.Request) (string, bool) { return "id", true }); err == nil {
			t.Errorf("accepted %s", u)
		}
	}
}

func TestGatewayStatusDoesNotPretendToMeasureFrames(t *testing.T) {
	_, mux, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), 1)
	w := request(mux, "GET", prefix+"status", "first", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"recording":false`) || !strings.Contains(w.Body.String(), `"reachable":true`) || strings.Contains(w.Body.String(), `"fps"`) {
		t.Fatalf("misleading camera status %s", w.Body.String())
	}
}

func TestGatewayStatusReturnsConfiguredICEServers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	mux := http.NewServeMux()
	gateway, err := Register(mux, Config{
		Upstream:   server.URL + "/camera/whep",
		ICEServers: []ICEServer{{URLs: []string{"turn:[2001:db8::1]:3478?transport=udp"}, Username: "onespace", Credential: "secret"}},
	}, func(r *http.Request) (string, bool) { return "device", true })
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	w := request(mux, "GET", prefix+"status", "first", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"iceServers"`) || !strings.Contains(w.Body.String(), "2001:db8") {
		t.Fatalf("ICE server configuration missing: %s", w.Body.String())
	}
}

func TestGatewayStatusRefreshesICEProviderPerRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	var calls atomic.Int32
	mux := http.NewServeMux()
	gateway, err := Register(mux, Config{
		Upstream: server.URL + "/camera/whep",
		ICEProvider: func() []ICEServer {
			n := calls.Add(1)
			return []ICEServer{{URLs: []string{"turn:turn.example:3478?transport=tcp"}, Username: "expiry-" + strconv.FormatInt(int64(n), 10), Credential: "short-lived"}}
		},
	}, func(r *http.Request) (string, bool) { return "device", true })
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	for _, want := range []string{`"username":"expiry-1"`, `"username":"expiry-2"`} {
		w := request(mux, "GET", prefix+"status", "first", nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("ICE provider result missing (%s): %s", want, w.Body.String())
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("ICE provider called %d times, want 2", calls.Load())
	}
}
