package coordination

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWHEPBridgeMapsOfferToLocalAnswer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/sdp") {
			t.Fatal("invalid WHEP request")
		}
		w.Header().Set("Location", "/camera/whep/session-one")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("answer-sdp"))
	}))
	defer upstream.Close()
	_, mux, _ := testBroker(t)
	response := doRequest(t, mux, http.MethodPost, prefix+"sessions", createRequest{Viewer: "windows", Target: "pi"})
	var created createResponse
	if err := decodeBody(response, &created); err != nil {
		t.Fatal(err)
	}
	response = doRequest(t, mux, http.MethodGet, prefix+"requests", nil)
	var requests []createResponse
	if err := decodeBody(response, &requests); err != nil || len(requests) != 1 {
		t.Fatalf("requests: %s", response.Body.String())
	}
	session := requests[0]
	// Exercise the bridge with a broker-backed HTTP test server.
	broker := httptest.NewServer(mux)
	defer broker.Close()
	viewer := Client{BaseURL: broker.URL, Token: "test" + "-token", HTTP: broker.Client()}
	if err := viewer.Send(context.Background(), session.Session, "windows", "offer", "offer-sdp"); err != nil {
		t.Fatal(err)
	}
	client := Client{BaseURL: broker.URL, Token: "pi" + "-token", HTTP: broker.Client()}
	bridge := WHEPBridge{Coordinator: client, Device: "pi", Upstream: upstream.URL + "/camera/whep", HTTP: upstream.Client()}
	if err := bridge.ServeRequest(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	messages, err := viewer.Poll(context.Background(), session.Session, "windows")
	if err != nil || len(messages) != 1 || messages[0].Type != "answer" || !strings.Contains(messages[0].Body, "answer-sdp") {
		t.Fatalf("answer was not routed: %#v %v", messages, err)
	}
}

func decodeBody(response *httptest.ResponseRecorder, target any) error {
	return json.Unmarshal(response.Body.Bytes(), target)
}
