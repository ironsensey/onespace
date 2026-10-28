package coordination

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testBroker(t *testing.T) (*Broker, *http.ServeMux, *atomic.Bool) {
	t.Helper()
	allowed := &atomic.Bool{}
	allowed.Store(true)
	windowToken := "test" + "-token"
	piToken := "pi" + "-token"
	mux := http.NewServeMux()
	broker, err := New(mux, func(r *http.Request) (string, bool) {
		if !allowed.Load() {
			return "", false
		}
		switch r.Header.Get("Authorization") {
		case "Bearer " + windowToken:
			return "windows", true
		case "Bearer " + piToken:
			return "pi", true
		default:
			return "", false
		}
	}, func(viewer, target string) bool {
		return viewer == "windows" && target == "pi"
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.Close)
	return broker, mux, allowed
}

func doRequest(t *testing.T, mux http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request := httptest.NewRequest(method, path, reader)
	token := "Bearer " + "test" + "-token"
	if strings.Contains(path, "for=pi") || strings.Contains(path, "from=pi") || strings.Contains(path, "by=pi") || strings.HasPrefix(path, prefix+"requests") {
		token = "Bearer " + "pi" + "-token"
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestBrokerRoutesAuthorizedSignaling(t *testing.T) {
	_, mux, _ := testBroker(t)
	response := doRequest(t, mux, http.MethodPost, prefix+"sessions", createRequest{Viewer: "windows", Target: "pi"})
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}

	var created createResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	post := prefix + "sessions/" + created.Session + "/messages?from=windows"
	response = doRequest(t, mux, http.MethodPost, post, messageRequest{Type: "offer", Body: "bounded-sdp"})
	if response.Code != http.StatusAccepted {
		t.Fatalf("post: %d %s", response.Code, response.Body.String())
	}
	response = doRequest(t, mux, http.MethodGet, prefix+"sessions/"+created.Session+"/poll?for=pi", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "bounded-sdp") {
		t.Fatalf("poll: %d %s", response.Code, response.Body.String())
	}
}

func TestBrokerRejectsUnauthorizedAndUnboundedMessages(t *testing.T) {
	_, mux, allowed := testBroker(t)
	response := doRequest(t, mux, http.MethodPost, prefix+"sessions", createRequest{Viewer: "windows", Target: "pi"})
	var created createResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, prefix+"sessions/"+created.Session+"/messages?from=other", bytes.NewReader([]byte(`{"type":"offer","body":"x"}`)))
	request.Header.Set("Authorization", "Bearer "+"test"+"-token")
	responseRecorder := httptest.NewRecorder()
	mux.ServeHTTP(responseRecorder, request)
	if responseRecorder.Code != http.StatusForbidden {
		t.Fatalf("mismatched sender accepted: %d", responseRecorder.Code)
	}
	large := strings.Repeat("x", maxMessage+1)
	response = doRequest(t, mux, http.MethodPost, prefix+"sessions/"+created.Session+"/messages?from=windows", messageRequest{Type: "offer", Body: large})
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large message: %d", response.Code)
	}
	allowed.Store(false)
	response = doRequest(t, mux, http.MethodPost, prefix+"sessions", createRequest{Viewer: "windows", Target: "pi"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("revoked creator accepted: %d", response.Code)
	}
}

func TestBrokerCloseRemovesSession(t *testing.T) {
	_, mux, _ := testBroker(t)
	response := doRequest(t, mux, http.MethodPost, prefix+"sessions", createRequest{Viewer: "windows", Target: "pi"})
	var created createResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	response = doRequest(t, mux, http.MethodDelete, prefix+"sessions/"+created.Session+"/close?by=pi", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("close: %d", response.Code)
	}
	response = doRequest(t, mux, http.MethodGet, prefix+"sessions/"+created.Session+"/poll?for=windows", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("closed session still available: %d", response.Code)
	}
}

func TestBrokerPollTimesOutBoundedly(t *testing.T) {
	_, mux, _ := testBroker(t)
	response := doRequest(t, mux, http.MethodPost, prefix+"sessions", createRequest{Viewer: "windows", Target: "pi"})
	var created createResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, prefix+"sessions/"+created.Session+"/poll?for=pi", nil)
	request.Header.Set("Authorization", "Bearer "+"pi"+"-token")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	request = request.WithContext(ctx)
	responseRecorder := httptest.NewRecorder()
	mux.ServeHTTP(responseRecorder, request)
	if responseRecorder.Code != 0 && responseRecorder.Code != http.StatusOK {
		t.Fatalf("poll cancellation: %d", responseRecorder.Code)
	}
}

func TestBrokerQueuesAndAuthenticatesPiSessionRequests(t *testing.T) {
	_, mux, _ := testBroker(t)
	response := doRequest(t, mux, http.MethodPost, prefix+"sessions", createRequest{Viewer: "windows", Target: "pi"})
	if response.Code != http.StatusCreated {
		t.Fatal(response.Code)
	}

	response = doRequest(t, mux, http.MethodGet, prefix+"requests", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"target":"pi"`) {
		t.Fatalf("request inbox: %d %s", response.Code, response.Body.String())
	}

	response = doRequest(t, mux, http.MethodGet, prefix+"requests", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"session"`) {
		t.Fatalf("request was not retryable: %s", response.Body.String())
	}
	var requests []createResponse
	if err := json.Unmarshal(response.Body.Bytes(), &requests); err != nil || len(requests) != 1 {
		t.Fatalf("retry request: %s", response.Body.String())
	}
	if response = doRequest(t, mux, http.MethodPost, prefix+"requests/"+requests[0].Session, nil); response.Code != http.StatusNoContent {
		t.Fatalf("acknowledge: %d %s", response.Code, response.Body.String())
	}
	if response = doRequest(t, mux, http.MethodGet, prefix+"requests", nil); response.Code != http.StatusOK || response.Body.String() != "[]\n" {
		t.Fatalf("acknowledged request remained: %s", response.Body.String())
	}
}

func TestBrokerDeduplicatesRepeatedSignalingMessage(t *testing.T) {
	_, mux, _ := testBroker(t)
	response := doRequest(t, mux, http.MethodPost, prefix+"sessions", createRequest{Viewer: "windows", Target: "pi"})
	var created createResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	path := prefix + "sessions/" + created.Session + "/messages?from=windows"
	input := messageRequest{ID: "a-1", Type: "offer", Body: "same-offer"}
	if response = doRequest(t, mux, http.MethodPost, path, input); response.Code != http.StatusAccepted {
		t.Fatal(response.Code)
	}
	if response = doRequest(t, mux, http.MethodPost, path, input); response.Code != http.StatusAccepted {
		t.Fatal(response.Code)
	}
	response = doRequest(t, mux, http.MethodGet, prefix+"sessions/"+created.Session+"/poll?for=pi", nil)
	if response.Code != http.StatusOK || strings.Count(response.Body.String(), "same-offer") != 1 {
		t.Fatalf("duplicate message was delivered: %s", response.Body.String())
	}
}
