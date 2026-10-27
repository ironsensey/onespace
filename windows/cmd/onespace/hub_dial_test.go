package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLocalHubRouteCannotReceiveCredentialWithWrongIdentity(t *testing.T) {
	var localRequests, remoteRequests atomic.Int32
	wrong := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { localRequests.Add(1) }))
	different, _, err := loadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wrong.TLS = &tls.Config{Certificates: []tls.Certificate{different}}
	wrong.Config.ErrorLog = log.New(io.Discard, "", 0)
	wrong.StartTLS()
	defer wrong.Close()
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteRequests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("missing scoped token")
		}
		w.Write([]byte(`{}`))
	}))
	defer remote.Close()
	hash := sha256.Sum256(remote.Certificate().Raw)
	c := HubConfig{Enabled: true, Address: remote.URL, LocalAddress: wrong.URL, Fingerprint: hex.EncodeToString(hash[:]), Token: strings.Repeat("a", 64), ClipboardMode: "lan"}
	client, err := newPinnedHubClient(c)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := hubRequest(context.Background(), client, c, "GET", "/hub/v1/info", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if remoteRequests.Load() != 1 || localRequests.Load() != 0 {
		t.Fatal("credential reached wrong local endpoint or fallback failed")
	}
}
func TestVerifiedLocalHubWorksWithoutPublicHairpin(t *testing.T) {
	local := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer local.Close()
	hash := sha256.Sum256(local.Certificate().Raw)
	c := HubConfig{Enabled: true, Address: "https://127.0.0.1:1", LocalAddress: local.URL, Fingerprint: hex.EncodeToString(hash[:]), Token: strings.Repeat("a", 64), ClipboardMode: "lan"}
	client, err := newPinnedHubClient(c)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := hubRequest(context.Background(), client, c, "GET", "/hub/v1/info", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}
