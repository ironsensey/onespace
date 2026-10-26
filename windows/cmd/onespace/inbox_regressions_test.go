package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Receive-path regressions found during the v0.6.2 audit.
func TestAuditQueuedClipboardHonorsChangedPermission(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := randomHex(32)
	s.Data.Devices = []Device{{ID: "phone", Token: token, Permissions: map[string]bool{"clipboard": false}}}
	s.Data.Items = []Item{{ID: "text", Kind: "text", Name: "clipboard.txt"}}
	s.Data.Outbox = []Outbox{{ID: "job", To: "phone", ItemID: "text"}}
	a := newApp(s, "fp")
	server := httptest.NewTLSServer(http.HandlerFunc(a.remote))
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/v1/outbox", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var jobs []any
	if err = json.NewDecoder(res.Body).Decode(&jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("disabled clipboard permission still delivers %d previously queued clipboard jobs", len(jobs))
	}
}

func TestAuditAckDoesNotHidePersistenceFailure(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := randomHex(32)
	s.Data.Devices = []Device{{ID: "phone", Token: token}}
	s.Data.Outbox = []Outbox{{ID: "job", To: "phone", ItemID: "item"}}
	if err = os.Mkdir(filepath.Join(s.root, "state.json.new"), 0700); err != nil {
		t.Fatal(err)
	}
	a := newApp(s, "fp")
	server := httptest.NewTLSServer(http.HandlerFunc(a.remote))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/v1/outbox/job/ack", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode < 500 {
		t.Fatalf("ack reported HTTP %d although saving state is impossible", res.StatusCode)
	}
}

func BenchmarkAuditPendingOutbox(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			s := &State{Data: persistent{Devices: []Device{{ID: "phone"}}}}
			for i := 0; i < n; i++ {
				id := fmt.Sprint(i)
				s.Data.Items = append(s.Data.Items, Item{ID: id})
				s.Data.Outbox = append(s.Data.Outbox, Outbox{ID: id, ItemID: id, To: "phone"})
			}
			a := newApp(s, "fp")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				a.s.Lock()
				_ = a.pendingOutboxLocked("phone")
				a.s.Unlock()
			}
		})
	}
}
