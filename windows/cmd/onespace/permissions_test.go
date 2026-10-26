package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPermissionsActuallyBlockAuthenticatedDevice(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := randomHex(32)
	s.Lock()
	s.Data.Devices = []Device{{ID: "phone-1", Name: "Phone", Token: token}}
	s.Unlock()
	a := newApp(s, "fingerprint")
	remote := httptest.NewTLSServer(http.HandlerFunc(a.remote))
	defer remote.Close()
	local := httptest.NewServer(http.HandlerFunc(a.local))
	defer local.Close()
	req := func(base, path, method, key string, body any, https bool) int {
		t.Helper()
		var b *bytes.Reader
		if body == nil {
			b = bytes.NewReader(nil)
		} else {
			v, _ := json.Marshal(body)
			b = bytes.NewReader(v)
		}
		r, e := http.NewRequest(method, base+path, b)
		if e != nil {
			t.Fatal(e)
		}
		if https {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if !https {
			r.Header.Set("X-Local-Key", key)
		}
		response, e := remote.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if got := req(remote.URL, "/v1/tasks", "GET", token, nil, true); got != 200 {
		t.Fatalf("existing phone blocked: %d", got)
	}
	if got := req(local.URL, "/local/device/permission", "POST", s.deskToken, map[string]any{"id": "phone-1", "feature": "tasks", "enabled": false}, false); got != 200 {
		t.Fatalf("deny rule not saved: %d", got)
	}
	if got := req(remote.URL, "/v1/tasks", "GET", token, nil, true); got != 403 {
		t.Fatalf("deny bypass: %d", got)
	}
	if got := req(remote.URL, "/v1/notes", "GET", token, nil, true); got != 200 {
		t.Fatalf("unrelated feature blocked: %d", got)
	}
	reloaded, e := NewState(s.root)
	if e != nil {
		t.Fatal(e)
	}
	if d, ok := reloaded.deviceByToken(token); !ok || d.Permissions["tasks"] {
		t.Fatal("permission lost after restart")
	}
	// A public, unauthenticated request cannot change permissions.
	if got := req(local.URL, "/local/device/permission", "POST", "not-key", map[string]any{"id": "phone-1", "feature": "tasks", "enabled": true}, false); got != 403 {
		t.Fatalf("unauthenticated permission change %d", got)
	}
	if got := req(local.URL, "/local/device/permission", "POST", s.deskToken, map[string]any{"id": "phone-1", "feature": "unknown", "enabled": true}, false); got != 400 {
		t.Fatalf("unknown permission accepted %d", got)
	}
}

func TestFanoutQueueSkipsSourceAndBlockedRecipients(t *testing.T) {
	s, e := NewState(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.Lock()
	s.Data.Devices = []Device{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C", Permissions: map[string]bool{"files": false}}}
	s.Unlock()
	a := newApp(s, "fingerprint")
	a.enqueueToOtherDevices(Item{ID: "item-one"}, "a")
	s.Lock()
	defer s.Unlock()
	if len(s.Data.Outbox) != 1 || s.Data.Outbox[0].To != "b" {
		t.Fatalf("wrong peer fanout: %+v", s.Data.Outbox)
	}
}

func TestClipboardFanoutRespectsDeviceClipboardPermission(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Lock()
	s.Data.Devices = []Device{
		{ID: "origin", Name: "Phone"},
		{ID: "texts-blocked", Name: "Private phone", Permissions: map[string]bool{"clipboard": false}},
		{ID: "photos-blocked", Name: "Restricted tablet", Permissions: map[string]bool{"camera": false}},
		{ID: "files-blocked", Name: "No share", Permissions: map[string]bool{"files": false}},
		{ID: "normal", Name: "Trusted tablet"},
	}
	s.Unlock()
	a := newApp(s, "fingerprint")
	a.enqueueToOtherDevices(Item{ID: "txt", Kind: "text"}, "origin")
	a.enqueueToOtherDevices(Item{ID: "pic", Kind: "file"}, "origin")
	s.Lock()
	defer s.Unlock()
	seen := map[string][]string{}
	for _, q := range s.Data.Outbox {
		seen[q.ItemID] = append(seen[q.ItemID], q.To)
	}
	for _, id := range seen["txt"] {
		if id == "texts-blocked" || id == "files-blocked" || id == "origin" {
			t.Fatalf("Unauthorized text recipient %s", id)
		}
	}
	for _, id := range seen["pic"] {
		if id == "photos-blocked" || id == "files-blocked" || id == "origin" {
			t.Fatalf("Unauthorized photo recipient %s", id)
		}
	}
	if len(seen["txt"]) != 2 || len(seen["pic"]) != 2 {
		t.Fatalf("Missing expected recipients: %+v", seen)
	}
}

func TestAutoClipboardRequiresConsentAndPersists(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.Data.AutoClipboard {
		t.Fatal("Cannot enable private clipboard sync by default")
	}
	a := newApp(s, "fingerprint")
	local := httptest.NewServer(http.HandlerFunc(a.local))
	defer local.Close()
	req, err := http.NewRequest("POST", local.URL+"/local/clipboard", bytes.NewBufferString(`{"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Local-Key", s.deskToken)
	response, err := local.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("Toggle refused: %d", response.StatusCode)
	}
	reloaded, err := NewState(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Data.AutoClipboard {
		t.Fatal("Auto-clipboard choice lost on restart")
	}
}
