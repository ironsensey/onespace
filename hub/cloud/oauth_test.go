package cloud

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthUsesPKCEOfflineScopeAndRejectsWrongState(t *testing.T) {
	dir := t.TempDir()
	clientFile := filepath.Join(dir, "client.json")
	if err := os.WriteFile(clientFile, []byte(`{"installed":{"client_id":"fixture.apps.googleusercontent.com","client_secret":"fixture-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	links := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Connect(ctx, clientFile, filepath.Join(dir, "saved.json"), port, func(link string) { links <- link })
	}()
	var link string
	select {
	case link = <-links:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("OAuth did not start")
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "accounts.google.com" || q.Get("scope") != Scope || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 40 || q.Get("access_type") != "offline" || strings.Contains(link, "fixture-secret") {
		t.Fatal("OAuth policy invalid")
	}
	callback, _ := url.Parse(q.Get("redirect_uri"))
	callback.RawQuery = url.Values{"code": {"unused"}, "state": {"wrong-state"}}.Encode()
	res, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("wrong callback state accepted")
	}
	cancel()
	if err = <-done; err == nil {
		t.Fatal("cancelled OAuth succeeded")
	}
	if _, err = os.Stat(filepath.Join(dir, "saved.json")); !os.IsNotExist(err) {
		t.Fatal("failed OAuth saved credential")
	}
}
func TestTokenRefreshIsSerializedAndCredentialsOwnerOnly(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "fixture-refresh" {
			t.Error("invalid refresh request")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "expires_in": 3600})
	}))
	defer s.Close()
	d, err := New(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	d.credentials = Credentials{ClientID: "fixture", RefreshToken: "fixture-refresh"}
	d.tokenURL = s.URL
	d.client = s.Client()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := d.bearer(context.Background())
			if err != nil || token != "new-access" {
				t.Error("refresh failed")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent callers refreshed repeatedly")
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err = atomicJSON(path, d.credentials); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential file permissions")
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = New(path); err == nil {
		t.Fatal("world-readable credentials accepted")
	}
}
