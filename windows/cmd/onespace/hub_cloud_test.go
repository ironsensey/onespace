package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCloudProxyPreservesChunkAndErrorsWithPinnedAuthentication(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("hub credential missing")
		}
		if r.URL.Path != "/hub/v1/cloud/uploads/fixture_upload" || r.Header.Get("X-Upload-Offset") != "1048576" {
			t.Error("upload routing/progress header changed")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "chunk" {
			t.Error("file bytes changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		io.WriteString(w, `{"error":"Refresh progress"}`)
	}))
	defer server.Close()
	c := testHubConfig(server)
	client, err := newPinnedHubClient(c)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	var app App
	r := httptest.NewRequest("PUT", "/local/hub/cloud/uploads/fixture_upload", strings.NewReader("chunk"))
	r.Header.Set("X-Upload-Offset", "1048576")
	w := httptest.NewRecorder()
	app.proxyHubCloud(w, r, client, c, "/cloud/uploads/fixture_upload")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Refresh progress") || calls.Load() != 1 {
		t.Fatal("proxy hid resumable-upload error")
	}
}

func TestCloudProxyRejectsUnboundedPathsQueriesAndBodies(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid cloud request reached hub") }))
	defer server.Close()
	c := testHubConfig(server)
	client, err := newPinnedHubClient(c)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	var app App
	for _, path := range []string{"/cloud/files/../status", "/cloud/unknown", "/cloud/uploads/id/extra"} {
		r := httptest.NewRequest("GET", "/local/hub"+path, nil)
		w := httptest.NewRecorder()
		app.proxyHubCloud(w, r, client, c, path)
		if w.Code != 404 {
			t.Fatal("invalid path accepted")
		}
	}
	r := httptest.NewRequest("GET", "/local/hub/cloud/files?url=http://attacker.invalid", nil)
	w := httptest.NewRecorder()
	app.proxyHubCloud(w, r, client, c, "/cloud/files")
	if w.Code != 400 {
		t.Fatal("unknown query accepted")
	}
	r = httptest.NewRequest("PUT", "/local/hub/cloud/uploads/fixture_upload", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	w = httptest.NewRecorder()
	app.proxyHubCloud(w, r, client, c, "/cloud/uploads/fixture_upload")
	if w.Code != 413 {
		t.Fatal("oversized chunk accepted")
	}
}
