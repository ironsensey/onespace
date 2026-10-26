package cloud

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSession struct {
	digest       hash.Hash
	file         File
	size, offset int64
}
type googleFixture struct {
	mu               sync.Mutex
	files            map[string]File
	sessions         map[string]*fakeSession
	generated        int
	requests         int
	lostCommit       bool
	foreignLocation  bool
	permissionBypass bool
	record           bool
	patches          int
}

func (g *googleFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests++
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/v3/config/paths/patch/camera" {
		var v struct {
			Record bool `json:"record"`
		}
		_ = json.NewDecoder(r.Body).Decode(&v)
		g.record = v.Record
		g.patches++
		fmt.Fprint(w, "{}")
		return
	}
	if r.Header.Get("Authorization") != "Bearer fixture-access" {
		http.Error(w, "auth", 401)
		return
	}
	path := r.URL.Path
	if path == "/drive/v3/files/generateIds" {
		g.generated++
		_ = json.NewEncoder(w).Encode(map[string]any{"ids": []string{fmt.Sprintf("drive_id_%08d", g.generated)}})
		return
	}
	if path == "/drive/v3/files" && r.Method == "POST" {
		var f File
		_ = json.NewDecoder(r.Body).Decode(&f)
		g.files[f.ID] = f
		_ = json.NewEncoder(w).Encode(f)
		return
	}
	if path == "/drive/v3/files" && r.Method == "GET" {
		files := []File{}
		for _, f := range g.files {
			if !f.Trashed && f.MimeType != "application/vnd.google-apps.folder" {
				files = append(files, f)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
		return
	}
	if strings.HasPrefix(path, "/drive/v3/files/") {
		id := strings.TrimPrefix(path, "/drive/v3/files/")
		f, ok := g.files[id]
		if !ok {
			http.Error(w, "missing", 404)
			return
		}
		if r.Method == "PATCH" {
			var p struct {
				Trashed    bool              `json:"trashed"`
				Properties map[string]string `json:"appProperties"`
			}
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p.Trashed {
				f.Trashed = true
			}
			if p.Properties != nil {
				f.AppProperties = p.Properties
			}
			g.files[id] = f
		}
		_ = json.NewEncoder(w).Encode(f)
		return
	}
	if path == "/upload/drive/v3/files" && r.Method == "POST" {
		var f File
		_ = json.NewDecoder(r.Body).Decode(&f)
		size, _ := strconv.ParseInt(r.Header.Get("X-Upload-Content-Length"), 10, 64)
		g.sessions[f.ID] = &fakeSession{file: f, size: size, digest: md5.New()}
		location := "https://www.googleapis.com/upload/drive/v3/files?upload_id=" + f.ID
		if g.foreignLocation {
			location = "https://attacker.invalid/upload"
		}
		w.Header().Set("Location", location)
		w.WriteHeader(200)
		return
	}
	if path == "/upload/drive/v3/files" && r.Method == "PUT" {
		s, ok := g.sessions[r.URL.Query().Get("upload_id")]
		if !ok {
			http.Error(w, "expired", 404)
			return
		}
		b, _ := io.ReadAll(io.LimitReader(r.Body, ChunkSize+1))
		if len(b) > 0 {
			var start, end, total int64
			_, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total)
			if err != nil || start != s.offset || total != s.size || end-start+1 != int64(len(b)) {
				http.Error(w, "range", 400)
				return
			}
			s.offset += int64(len(b))
			_, _ = s.digest.Write(b)
		}
		if s.offset == s.size {
			f := s.file
			f.Size = strconv.FormatInt(s.size, 10)
			f.MD5Checksum = hex.EncodeToString(s.digest.Sum(nil))
			g.files[f.ID] = f
			if g.lostCommit {
				g.lostCommit = false
				http.Error(w, "lost response", 503)
				return
			}
			_ = json.NewEncoder(w).Encode(f)
			return
		}
		if s.offset > 0 {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", s.offset-1))
		}
		w.WriteHeader(308)
		return
	}
	http.NotFound(w, r)
}

type reroute struct {
	target *url.URL
	base   http.RoundTripper
}

func (t reroute) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	u := *r.URL
	u.Host = t.target.Host
	u.Scheme = t.target.Scheme
	c.URL = &u
	return t.base.RoundTrip(c)
}
func fixture(t testing.TB) (*Manager, *googleFixture) {
	t.Helper()
	g := &googleFixture{files: map[string]File{}, sessions: map[string]*fakeSession{}}
	server := httptest.NewServer(g)
	t.Cleanup(server.Close)
	m, err := Open(filepath.Join(t.TempDir(), "cloud"), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(server.URL)
	m.drive.client = &http.Client{Transport: reroute{u, http.DefaultTransport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: time.Second}
	m.drive.hasCredential.Store(true)
	m.drive.credentials = Credentials{ClientID: "fixture", RefreshToken: "fixture-refresh"}
	m.drive.access = "fixture-access"
	m.drive.expires = time.Now().Add(time.Hour)
	m.bufferReady = func(string, string) bool { return true }
	m.authorize = func(r *http.Request, scope string) (string, bool) {
		return r.Header.Get("X-Owner"), strings.Contains(" "+r.Header.Get("X-Scopes")+" ", " "+scope+" ")
	}
	t.Cleanup(m.Close)
	return m, g
}
func call(m *Manager, method, path, owner, scopes string, body []byte, offset string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, Prefix+path, bytes.NewReader(body))
	r.Header.Set("X-Owner", owner)
	r.Header.Set("X-Scopes", scopes)
	r.Header.Set("X-Upload-Offset", offset)
	w := httptest.NewRecorder()
	m.handle(w, r)
	return w
}
func TestUploadRecoveryAndIsolation(t *testing.T) {
	m, g := fixture(t)
	ctx := context.Background()
	j, err := m.newJob(ctx, "phone", "large.bin", ChunkSize+5, "Permanent", 7, "file", "")
	if err != nil {
		t.Fatal(err)
	}
	j, done, err := m.advance(ctx, j, bytes.Repeat([]byte{1}, ChunkSize))
	if err != nil || done || j.Offset != ChunkSize {
		t.Fatalf("first chunk: %+v %v %v", j, done, err)
	}
	if w := call(m, "GET", "/uploads/"+j.ID, "other", "cloud", nil, ""); w.Code != 404 {
		t.Fatalf("cross-device upload visible: %d", w.Code)
	}
	if w := call(m, "PUT", "/uploads/"+j.ID, "phone", "cloud", []byte{2}, "0"); w.Code != 409 {
		t.Fatalf("stale offset accepted: %d", w.Code)
	}
	status := call(m, "GET", "/status", "phone", "cloud", nil, "").Body.String()
	if strings.Contains(status, "upload_id") || strings.Contains(status, "fixture-refresh") {
		t.Fatal("credential/session leaked")
	}
	g.mu.Lock()
	g.lostCommit = true
	g.mu.Unlock()
	_, _, err = m.advance(ctx, j, []byte("final"))
	if err == nil {
		t.Fatal("fixture did not lose final response")
	}
	j, done, err = m.advance(ctx, j, nil)
	if err != nil || !done || j.Offset != j.Size {
		t.Fatalf("lost commit recovery: %v %v", done, err)
	}
	m.mu.Lock()
	remaining := len(m.state.Jobs)
	m.mu.Unlock()
	if remaining != 0 {
		t.Fatal("completed job not removed")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.files) != 2 {
		t.Fatalf("duplicate remote file: %d", len(g.files))
	}
}
func TestExpiredSessionAndResumeProbe(t *testing.T) {
	m, g := fixture(t)
	j, err := m.newJob(context.Background(), "pc", "resume.bin", ChunkSize+1, "Temporary", 7, "file", "")
	if err != nil {
		t.Fatal(err)
	}
	j, _, err = m.advance(context.Background(), j, make([]byte, ChunkSize))
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	delete(g.sessions, j.DriveID)
	g.mu.Unlock()
	j, _, err = m.advance(context.Background(), j, nil)
	if err == nil || j.Offset != 0 || j.Session != "" {
		t.Fatal("expired session not reset")
	}
	j, _, err = m.advance(context.Background(), j, nil)
	if err != nil || j.Offset != 0 {
		t.Fatal(err)
	}
	j, _, err = m.advance(context.Background(), j, make([]byte, ChunkSize))
	if err != nil || j.Offset != ChunkSize {
		t.Fatal(err)
	}
}
func TestRetentionAndManagedFolderBoundary(t *testing.T) {
	m, g := fixture(t)
	root, err := m.root(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	for _, bucket := range []string{"Temporary", "Permanent"} {
		id := "fixture_" + bucket
		g.files[id] = File{ID: id, Name: id, Parents: []string{root}, AppProperties: map[string]string{"managedBy": managed, "bucket": bucket, "expiresAt": time.Now().Add(-time.Hour).Format(time.RFC3339)}}
	}
	g.files["outside_123"] = File{ID: "outside_123", Parents: []string{"another_folder"}, AppProperties: map[string]string{"managedBy": managed}}
	g.mu.Unlock()
	if err = m.retention(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	temp := g.files["fixture_Temporary"].Trashed
	permanent := g.files["fixture_Permanent"].Trashed
	g.mu.Unlock()
	if !temp || permanent {
		t.Fatal("retention touched wrong bucket")
	}
	if err = m.drive.Trash(context.Background(), root, "outside_123"); err == nil {
		t.Fatal("file outside app root accepted")
	}
	w := call(m, "DELETE", "/files/outside_123", "phone", "cloud", nil, "")
	if w.Code != 502 {
		t.Fatal("unmanaged file deletion accepted")
	}
}
func TestAuthorizationAndInputLimits(t *testing.T) {
	m, _ := fixture(t)
	for _, path := range []string{"/status", "/files", "/uploads"} {
		if w := call(m, "GET", path, "device", "camera", nil, ""); w.Code != 403 {
			t.Fatal("cloud scope bypass")
		}
	}
	for _, scopes := range []string{"cloud", "cloud camera", "cloud recording"} {
		if w := call(m, "POST", "/recording", "device", scopes, []byte(`{"enabled":true,"bucket":"Temporary","temporaryDays":7}`), ""); w.Code != 403 {
			t.Fatal("recording scope bypass")
		}
	}
	for _, name := range []string{"../clip.mp4", "bad\nname", "", " padded "} {
		if validName(name) {
			t.Fatalf("unsafe name accepted: %q", name)
		}
	}
	w := call(m, "POST", "/uploads", "device", "cloud", []byte(`{"name":"a","size":0,"bucket":"Permanent","days":7}`), "")
	if w.Code != 400 {
		t.Fatal("zero size accepted")
	}
	w = call(m, "POST", "/uploads", "device", "cloud", []byte(`{"name":"a","size":1,"bucket":"Permanent","days":7,"url":"http://attacker"}`), "")
	if w.Code != 400 {
		t.Fatal("unknown field accepted")
	}
}
func TestSessionSSRFAndSafeErrors(t *testing.T) {
	m, g := fixture(t)
	for _, s := range []string{"https://attacker.invalid/upload", "https://www.googleapis.com.evil/upload/drive/v3/files", "https://user@www.googleapis.com/upload/drive/v3/files", "http://www.googleapis.com/upload/drive/v3/files"} {
		if m.drive.safeSession(s) {
			t.Fatal("unsafe session accepted")
		}
	}
	g.mu.Lock()
	g.foreignLocation = true
	g.mu.Unlock()
	_, err := m.drive.Start(context.Background(), "fixture_id", "root_id_1", "file", "application/octet-stream", "Permanent", 1, time.Now(), "file")
	if err == nil {
		t.Fatal("foreign upload location accepted")
	}
	if strings.Contains(err.Error(), "attacker") {
		t.Fatal("secret endpoint included in error")
	}
	if _, err = Open(t.TempDir(), "http://192.168.1.1:9997"); err == nil {
		t.Fatal("non-loopback camera API accepted")
	}
}
func TestMetadataFailureRollsBackInsertAndDelete(t *testing.T) {
	m, _ := fixture(t)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dir = filepath.Join(m.dir, "not-a-dir")
	if err := os.WriteFile(m.dir, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.commitLocked(func() { m.state.Jobs["new"] = Job{ID: "new"} }); err == nil || len(m.state.Jobs) != 0 {
		t.Fatal("failed insert did not roll back")
	}
	m.state.Jobs["old"] = Job{ID: "old"}
	if err := m.commitLocked(func() { delete(m.state.Jobs, "old") }); err == nil || len(m.state.Jobs) != 1 {
		t.Fatal("failed deletion did not roll back")
	}
}
func TestRecordingCompletionAndVerifiedCleanup(t *testing.T) {
	m, g := fixture(t)
	name := "t7_2026-10-26_10-20-30-123456.mp4"
	path := filepath.Join(m.spool, name)
	if err := os.WriteFile(path, []byte("fixture-fmp4-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CompleteSegment(m.spool, path); err != nil {
		t.Fatal(err)
	}
	if err := CompleteSegment(m.spool, filepath.Join(m.spool, "..", name)); err == nil {
		t.Fatal("outside segment accepted")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("hook removed local footage")
	}
	worked, err := m.uploadOne(context.Background())
	if err != nil || !worked {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("verified remote recording left local clip")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, f := range g.files {
		if f.MimeType == "video/mp4" && f.AppProperties["bucket"] != "Temporary" {
			t.Fatal("recording lost retention")
		}
	}
}

func TestRemoteChecksumMismatchPreservesLocalRecording(t *testing.T) {
	m, g := fixture(t)
	name := "p7_2026-10-26_11-22-33-123456.mp4"
	path := filepath.Join(m.spool, name)
	if err := os.WriteFile(path, []byte("local-original"), 0600); err != nil {
		t.Fatal(err)
	}
	j, err := m.newJob(context.Background(), "recorder", name, 14, "Permanent", 7, "recording", name)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	root := m.state.Root
	m.mu.Unlock()
	g.mu.Lock()
	g.files[j.DriveID] = File{ID: j.DriveID, Size: "14", MD5Checksum: "wrong", Parents: []string{root}, AppProperties: map[string]string{"managedBy": managed}}
	g.mu.Unlock()
	if err = m.finish(j); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("local recording removed before integrity verification")
	}
}

func TestRetentionUsesIndexedExpiryDay(t *testing.T) {
	m, _ := fixture(t)
	root, err := m.root(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old := m.drive.client.Transport
	m.drive.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/drive/v3/files" && r.Method == "GET" {
			q := r.URL.Query().Get("q")
			if !strings.Contains(q, "expiresOn") || !strings.Contains(q, "Temporary") {
				t.Error("retention scanned all video history")
			}
		}
		return old.RoundTrip(r)
	})
	_, _, err = m.drive.list(context.Background(), root, "", time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSpoolPauseAndResume(t *testing.T) {
	m, g := fixture(t)
	m.state.ControllerConfigured = true
	m.state.Recording.Enabled = true
	m.freeSpace = func(string) (uint64, error) { return 1 << 30, nil }
	path := filepath.Join(m.spool, "p7_2026-10-26_10-20-30-123456.mp4")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(97 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	m.wg.Add(1)
	go m.recordLoop()
	time.Sleep(2200 * time.Millisecond)
	g.mu.Lock()
	record := g.record
	g.mu.Unlock()
	m.mu.Lock()
	reason := m.recordError
	m.mu.Unlock()
	if record || !strings.Contains(reason, "buffer is full") {
		t.Fatal("quota did not pause recording")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2200 * time.Millisecond)
	g.mu.Lock()
	record = g.record
	g.mu.Unlock()
	if !record {
		t.Fatal("recording did not resume after buffer drained")
	}
}
func BenchmarkUploadChunk(b *testing.B) {
	m, _ := fixture(b)
	data := make([]byte, ChunkSize)
	b.SetBytes(ChunkSize)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j, err := m.newJob(context.Background(), "recorder", fmt.Sprintf("b%d", i), ChunkSize, "Permanent", 7, "recording", "")
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err = m.advance(context.Background(), j, data); err != nil {
			b.Fatal(err)
		}
	}
}
