package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFullWorkflowAndRestart(t *testing.T) {
	root := t.TempDir()
	s, err := NewState(root)
	if err != nil {
		t.Fatal(err)
	}
	const password = "test-ecosystem-passphrase-with-length"
	if err = s.SetPassword(password); err != nil {
		t.Fatal(err)
	}
	a := newApp(s, "testfingerprint")
	remote := httptest.NewTLSServer(http.HandlerFunc(a.remote))
	defer remote.Close()
	local := httptest.NewServer(http.HandlerFunc(a.local))
	defer local.Close()
	cli := remote.Client()
	call := func(client *http.Client, base, path, method, token string, request any) (int, map[string]any) {
		t.Helper()
		var body io.Reader
		if request != nil {
			b, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, base+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if request != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			if base == local.URL {
				req.Header.Set("X-Local-Key", token)
			} else {
				req.Header.Set("Authorization", "Bearer "+token)
			}
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	status, _ := call(cli, remote.URL, "/v1/clipboard/pull", "GET", "", nil)
	if status != 401 {
		t.Fatalf("unpaired could read clipboard: %d", status)
	}
	status, _ = call(cli, remote.URL, "/v1/pair/request", "POST", "", map[string]string{"name": "Test Android", "password": "wrong"})
	if status != 403 {
		t.Fatalf("incorrect password: %d", status)
	}
	status, pair := call(cli, remote.URL, "/v1/pair/request", "POST", "", map[string]string{"name": "Test Android", "password": password})
	if status != 202 {
		t.Fatalf("pair: %d %+v", status, pair)
	}
	id, ticket := pair["id"].(string), pair["ticket"].(string)
	status, _ = call(cli, remote.URL, "/v1/pair/status?id="+id+"&ticket=bad", "GET", "", nil)
	if status != 404 {
		t.Fatalf("ticket bypass %d", status)
	}
	status, _ = call(nilAsDefault(), local.URL, "/local/pair/decision", "POST", "wrong", map[string]any{"id": id, "approve": true})
	if status != 403 {
		t.Fatalf("local key bypass %d", status)
	}
	status, _ = call(nilAsDefault(), local.URL, "/local/pair/decision", "POST", s.deskToken, map[string]any{"id": id, "approve": true})
	if status != 200 {
		t.Fatalf("approval failed %d", status)
	}
	status, auth := call(cli, remote.URL, "/v1/pair/status?id="+id+"&ticket="+ticket, "GET", "", nil)
	if status != 200 || auth["state"] != "approved" {
		t.Fatalf("status failed %+v", auth)
	}
	token := auth["token"].(string)
	status, textItem := call(cli, remote.URL, "/v1/items/text", "POST", token, map[string]any{"text": "Hello from Android", "bucket": "Temporary", "clipboard": true})
	if status != 201 {
		t.Fatalf("text upload: %d %+v", status, textItem)
	}
	itemid := textItem["id"].(string)
	if _, err := os.Stat(filepath.Join(root, "Temporary", itemid+"_clipboard.txt")); err != nil {
		t.Fatal(err)
	}
	status, _ = call(cli, remote.URL, "/v1/items/"+itemid+"/pin", "POST", token, map[string]any{})
	if status != 200 {
		t.Fatalf("pin %d", status)
	}
	if _, err := os.Stat(filepath.Join(root, "Permanent", itemid+"_clipboard.txt")); err != nil {
		t.Fatal(err)
	}
	status, _ = call(cli, remote.URL, "/v1/tasks", "POST", token, map[string]any{"text": "Shared test task", "done": false})
	if status != 201 {
		t.Fatalf("task %d", status)
	}
	status, _ = call(cli, remote.URL, "/v1/notes", "POST", token, map[string]any{"title": "Note", "body": "Body"})
	if status != 201 {
		t.Fatalf("note %d", status)
	}
	// Verify credentials AND file paths survive a restart.
	reloaded, err := NewState(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.deviceByToken(token); !ok {
		t.Fatal("pairing token lost across restart")
	}
	if len(reloaded.Data.Items) != 1 || reloaded.Data.Items[0].RelPath == "" {
		t.Fatal("file path not persistent")
	}
	reloaded.Lock()
	reloaded.Data.Items[0].Bucket = "Temporary"
	reloaded.Data.Items[0].Created = time.Now().Add(-25 * time.Hour)
	reloaded.Unlock()
	reloaded.cleanup()
	if len(reloaded.Data.Items) != 0 {
		t.Fatal("24 hour expiry didn't remove item")
	}
	status, _ = call(nilAsDefault(), local.URL, "/local/device/revoke", "POST", s.deskToken, map[string]string{"id": id})
	if status != 200 {
		t.Fatalf("revoke %d", status)
	}
	status, _ = call(cli, remote.URL, "/v1/items", "GET", token, nil)
	if status != 401 {
		t.Fatalf("revoked token still works %d", status)
	}
}
func nilAsDefault() *http.Client { return http.DefaultClient }
func TestCertificatePersistenceAndSanitize(t *testing.T) {
	root := t.TempDir()
	_, fp1, err := loadOrCreateCert(root)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, err := loadOrCreateCert(root)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 || len(fp1) != 64 {
		t.Fatal("certificate not persistent")
	}
	for _, v := range []string{"../../windows/system32/a.exe", "..\\..\\note.txt"} {
		name := safeName(v)
		if strings.ContainsAny(name, "/\\") {
			t.Fatalf("unsafe name: %q", name)
		}
	}
}
