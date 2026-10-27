package updates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const maxAPKSize int64 = 150 << 20
const updatePackage = "com.onespace.companion"

type Manifest struct {
	PackageName  string `json:"packageName"`
	VersionCode  int64  `json:"versionCode"`
	VersionName  string `json:"versionName"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	DownloadPath string `json:"downloadPath"`
}
type Store struct {
	mu        sync.RWMutex
	dir       string
	authorize func(*http.Request, string) bool
}

func Register(mux *http.ServeMux, dir string, authorize func(*http.Request, string) bool) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, authorize: authorize}
	mux.HandleFunc("/hub/v1/updates/", s.serve)
	return s, nil
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}
func (s *Store) manifest() (Manifest, error) {
	var m Manifest
	b, e := os.ReadFile(filepath.Join(s.dir, "android.json"))
	if e != nil {
		return m, e
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	if m.PackageName != updatePackage || m.VersionCode < 1 || m.Size < 1 || m.Size > maxAPKSize || len(m.SHA256) != 64 || m.DownloadPath != "/v1/update/apk" {
		return m, errors.New("invalid stored manifest")
	}
	if _, e = hex.DecodeString(m.SHA256); e != nil {
		return m, e
	}
	return m, nil
}
func (s *Store) serve(w http.ResponseWriter, r *http.Request) {
	reserve := r.Method == "POST" && r.URL.Path == "/hub/v1/updates/reserve"
	publish := r.Method == "PUT" && r.URL.Path == "/hub/v1/updates/apk"
	scope := "updates"
	if publish || reserve {
		scope = "updates.publish"
	}
	if !s.authorize(r, scope) {
		failure(w, 403, "update permission required")
		return
	}
	if reserve {
		s.reserve(w, r)
		return
	}
	if publish {
		s.publish(w, r)
		return
	}
	if r.Method != "GET" || (r.URL.Path != "/hub/v1/updates/manifest" && r.URL.Path != "/hub/v1/updates/apk") {
		failure(w, 405, "unsupported update operation")
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.manifest()
	if err != nil {
		failure(w, 404, "No Android update has been published yet")
		return
	}
	if r.URL.Path == "/hub/v1/updates/manifest" {
		respond(w, 200, m)
		return
	}
	name := filepath.Join(s.dir, strconv.FormatInt(m.VersionCode, 10)+"-"+m.SHA256+".apk")
	f, err := os.Open(name)
	if err != nil {
		failure(w, 500, "published APK missing")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != m.Size {
		failure(w, 500, "published APK size mismatch")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Length", strconv.FormatInt(m.Size, 10))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}
func (s *Store) publish(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > maxAPKSize || r.ContentLength == 0 {
		failure(w, 413, "APK must be 1 byte to 150 MiB")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(10 * time.Minute))
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Minute))
	f, err := os.CreateTemp(s.dir, ".pending-*.apk")
	if err != nil {
		failure(w, 500, "cannot stage APK")
		return
	}
	defer os.Remove(f.Name())
	_ = f.Chmod(0600)
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(&syncingFile{File: f}, hash), io.LimitReader(r.Body, maxAPKSize+1))
	syncErr := f.Sync()
	closeErr := f.Close()
	if err != nil || syncErr != nil || closeErr != nil || r.Context().Err() != nil {
		failure(w, 500, "APK staging interrupted")
		return
	}
	if size == 0 || size > maxAPKSize {
		failure(w, 413, "invalid APK size")
		return
	}
	info, err := readAPKInfo(f.Name())
	if err != nil || info.Package != updatePackage || info.VersionCode < 1 {
		failure(w, 400, "APK package/version invalid")
		return
	}
	previous, err := s.manifest()
	if err == nil && info.VersionCode <= previous.VersionCode {
		failure(w, 409, "version code must exceed the published Pi version")
		return
	}
	if err != nil && !os.IsNotExist(err) {
		failure(w, 500, "stored manifest needs repair; not overwritten")
		return
	}
	m := Manifest{PackageName: updatePackage, VersionCode: info.VersionCode, VersionName: info.VersionName, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), DownloadPath: "/v1/update/apk"}
	name := strconv.FormatInt(m.VersionCode, 10) + "-" + m.SHA256 + ".apk"
	if err = os.Rename(f.Name(), filepath.Join(s.dir, name)); err != nil {
		failure(w, 500, "cannot retain APK")
		return
	}
	b, _ := json.Marshal(m)
	manifest, err := os.CreateTemp(s.dir, ".manifest-*")
	if err != nil {
		failure(w, 500, "cannot stage metadata")
		return
	}
	defer os.Remove(manifest.Name())
	_ = manifest.Chmod(0600)
	_, err = manifest.Write(b)
	if err == nil {
		err = manifest.Sync()
	}
	closeErr = manifest.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(manifest.Name(), filepath.Join(s.dir, "android.json"))
	}
	if err != nil {
		failure(w, 500, "cannot publish metadata; previous release retained")
		return
	}
	// Keep only the active APK; already-open Linux readers retain their file handle.
	entries, _ := os.ReadDir(s.dir)
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".apk" && entry.Name() != name {
			_ = os.Remove(filepath.Join(s.dir, entry.Name()))
		}
	}
	if err = syncDirectory(s.dir); err != nil {
		failure(w, 500, "cannot sync publication directory")
		return
	}
	respond(w, 201, m)
}

// reserve is a single shared release-code authority for all builders using this
// Pi. Allocation is durable before success; failed builds leave harmless gaps.
func (s *Store) reserve(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MinimumCode int64 `json:"minimumCode"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || in.MinimumCode < 1 || in.MinimumCode > 2100000000 {
		failure(w, 400, "minimumCode must be 1–2100000000")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := in.MinimumCode - 1
	if manifest, err := s.manifest(); err == nil {
		previous = max(previous, manifest.VersionCode)
	} else if !os.IsNotExist(err) {
		failure(w, 500, "published metadata needs repair")
		return
	}
	path := filepath.Join(s.dir, "version-counter")
	if b, err := os.ReadFile(path); err == nil {
		value, e := strconv.ParseInt(string(b), 10, 64)
		if e != nil || value < 0 {
			failure(w, 500, "version counter needs repair")
			return
		}
		previous = max(previous, value)
	} else if !os.IsNotExist(err) {
		failure(w, 500, "cannot read version counter")
		return
	}
	if previous >= 2100000000 {
		failure(w, 409, "Android version code limit reached")
		return
	}
	next := previous + 1
	file, err := os.CreateTemp(s.dir, ".counter-*")
	if err != nil {
		failure(w, 500, "cannot reserve code")
		return
	}
	defer os.Remove(file.Name())
	_ = file.Chmod(0600)
	_, err = file.WriteString(strconv.FormatInt(next, 10))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		failure(w, 500, "cannot retain reserved code")
		return
	}
	if err = syncDirectory(s.dir); err != nil {
		failure(w, 500, "cannot sync version reservation")
		return
	}
	respond(w, 201, map[string]int64{"versionCode": next})
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Keep dirty page-cache pressure bounded under the Pi hub's memory ceiling.
type syncingFile struct {
	*os.File
	dirty int
}

func (f *syncingFile) Write(data []byte) (int, error) {
	n, err := f.File.Write(data)
	f.dirty += n
	if err == nil && f.dirty >= 4<<20 {
		err = f.File.Sync()
		f.dirty = 0
	}
	return n, err
}
