package updates

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPublicationAuthorizationMonotonicityAndRestart(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	_, err := Register(mux, dir, func(r *http.Request, scope string) bool { return r.Header.Get("Authorization") == "Bearer "+scope })
	if err != nil {
		t.Fatal(err)
	}
	call := func(handler http.Handler, method, path, scope string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		if scope != "" {
			r.Header.Set("Authorization", "Bearer "+scope)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call(mux, "PUT", "/hub/v1/updates/apk", "updates", fixtureAPK(t)); w.Code != 403 {
		t.Fatal("reader could publish")
	}
	if w := call(mux, "GET", "/hub/v1/updates/manifest", "", nil); w.Code != 403 {
		t.Fatal("unauthenticated manifest")
	}
	if w := call(mux, "PUT", "/hub/v1/updates/apk", "updates.publish", []byte("broken")); w.Code != 400 {
		t.Fatal("invalid APK published")
	}
	apk := fixtureAPK(t)
	w := call(mux, "PUT", "/hub/v1/updates/apk", "updates.publish", apk)
	if w.Code != 201 {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}
	var m Manifest
	if err = json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(apk)
	if m.VersionCode != 6 || m.SHA256 != hex.EncodeToString(hash[:]) || m.DownloadPath != "/v1/update/apk" {
		t.Fatal("metadata changed")
	}
	if w = call(mux, "PUT", "/hub/v1/updates/apk", "updates.publish", apk); w.Code != 409 {
		t.Fatal("duplicate code accepted")
	}
	if w = call(mux, "GET", "/hub/v1/updates/apk", "updates", nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), apk) {
		t.Fatal("APK bytes corrupt")
	}
	again := http.NewServeMux()
	_, err = Register(again, dir, func(*http.Request, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	w = call(again, "GET", "/hub/v1/updates/manifest", "", nil)
	if w.Code != 200 {
		t.Fatal("manifest lost after restart")
	}
	// Preserve invalid metadata for repair rather than silently downgrading it.
	if err = os.WriteFile(filepath.Join(dir, "android.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if w = call(mux, "PUT", "/hub/v1/updates/apk", "updates.publish", apk); w.Code != 500 {
		t.Fatal("corrupt metadata overwritten")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if len(entry.Name()) > 0 && entry.Name()[0] == '.' {
			t.Fatal("temporary update left behind")
		}
	}
}
func TestUpdateStreamMatchesManifest(t *testing.T) {
	mux := http.NewServeMux()
	_, err := Register(mux, t.TempDir(), func(*http.Request, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	req, _ := http.NewRequest("PUT", server.URL+"/hub/v1/updates/apk", bytes.NewReader(fixtureAPK(t)))
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatal(res.Status)
	}
	res, err = server.Client().Get(server.URL + "/hub/v1/updates/apk")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	content, err := io.ReadAll(res.Body)
	if err != nil || int64(len(content)) != res.ContentLength {
		t.Fatal("truncated APK")
	}
}

func TestVersionReservationsAreSharedAndDurable(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	_, err := Register(mux, dir, func(r *http.Request, scope string) bool { return r.Header.Get("Authorization") == scope })
	if err != nil {
		t.Fatal(err)
	}
	reserve := func(handler http.Handler, scope string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/hub/v1/updates/reserve", bytes.NewBufferString(`{"minimumCode":24}`))
		r.Header.Set("Authorization", scope)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := reserve(mux, "updates"); w.Code != 403 {
		t.Fatal("reader reserved publisher code")
	}
	results := make(chan *httptest.ResponseRecorder, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- reserve(mux, "updates.publish") }()
	}
	seen := map[int64]bool{}
	for i := 0; i < 8; i++ {
		w := <-results
		if w.Code != 201 {
			t.Fatalf("reserve: %d %s", w.Code, w.Body.String())
		}
		var value map[string]int64
		if json.Unmarshal(w.Body.Bytes(), &value) != nil {
			t.Fatal("invalid allocation")
		}
		code := value["versionCode"]
		if code < 24 || code > 31 || seen[code] {
			t.Fatal("duplicate/regressed code")
		}
		seen[code] = true
	}
	again := http.NewServeMux()
	_, err = Register(again, dir, func(*http.Request, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	w := reserve(again, "")
	var next map[string]int64
	_ = json.Unmarshal(w.Body.Bytes(), &next)
	if w.Code != 201 || next["versionCode"] != 32 {
		t.Fatal("allocation regressed after restart")
	}
	if err = os.WriteFile(filepath.Join(dir, "version-counter"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if w = reserve(again, ""); w.Code != 500 {
		t.Fatal("corrupted counter silently reset")
	}
}
