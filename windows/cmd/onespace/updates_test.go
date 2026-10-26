package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignedReleaseDistributionPermissionsAndPersistence(t *testing.T) {
	root := t.TempDir()
	s, err := NewState(root)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(s, "test-fingerprint")
	pc := httptest.NewServer(http.HandlerFunc(a.local))
	defer pc.Close()
	phone := httptest.NewTLSServer(http.HandlerFunc(a.remote))
	defer phone.Close()
	req := func(client *http.Client, url, path, method, key string, data []byte) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, url+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(path, "/local/") {
			r.Header.Set("X-Local-Key", key)
		} else if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	updateFile := fixtureAPK(t)
	path := "/local/updates/upload"
	resp := req(http.DefaultClient, pc.URL, path, "POST", "wrong", updateFile)
	if resp.StatusCode != 403 {
		t.Fatalf("update upload accepted wrong key: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = req(http.DefaultClient, pc.URL, path, "POST", s.deskToken, []byte("not an apk"))
	if resp.StatusCode != 400 {
		t.Fatalf("invalid archive accepted: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = req(http.DefaultClient, pc.URL, path, "POST", s.deskToken, updateFile)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("valid upload: %d %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	resp = req(http.DefaultClient, pc.URL, path, "POST", s.deskToken, updateFile)
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate version accepted: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = req(phone.Client(), phone.URL, "/v1/update/manifest", "GET", "", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("unpaired phone could fetch update: %d", resp.StatusCode)
	}
	resp.Body.Close()
	token := randomHex(32)
	s.Lock()
	s.Data.Devices = append(s.Data.Devices, Device{ID: "phone1", Name: "Phone", Token: token})
	s.Unlock()
	resp = req(phone.Client(), phone.URL, "/v1/update/manifest", "GET", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("paired manifest: %d", resp.StatusCode)
	}
	var manifest UpdateManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if manifest.VersionCode != 6 || manifest.PackageName != updatePackage || manifest.Size != int64(len(updateFile)) {
		t.Fatalf("manifest mismatch: %+v", manifest)
	}
	actualHash := sha256.Sum256(updateFile)
	if manifest.SHA256 != hex.EncodeToString(actualHash[:]) {
		t.Fatal("hash mismatch")
	}
	resp = req(phone.Client(), phone.URL, "/v1/update/apk", "GET", token, nil)
	blob, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !bytes.Equal(blob, updateFile) {
		t.Fatal("download was corrupted")
	}
	// Metadata and APK survive desktop service restart.
	reloaded, err := NewState(root)
	if err != nil {
		t.Fatal(err)
	}
	a2 := newApp(reloaded, "new-ephemeral-object")
	again, err := a2.readUpdate()
	if err != nil || again.SHA256 != manifest.SHA256 {
		t.Fatalf("update did not persist: %v", err)
	}
}
