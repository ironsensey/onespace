package cloud

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const Prefix = "/hub/v1/cloud"
const MaxFileSize int64 = 5_000_000_000_000

type Job struct {
	ID            string    `json:"id"`
	Owner         string    `json:"owner"`
	DriveID       string    `json:"driveId"`
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	Offset        int64     `json:"offset"`
	Bucket        string    `json:"bucket"`
	TemporaryDays int       `json:"temporaryDays"`
	Kind          string    `json:"kind"`
	ExpiresAt     time.Time `json:"expiresAt"`
	CreatedAt     time.Time `json:"createdAt"`
	Session       string    `json:"session,omitempty"` // Secret: disk only, never serialize to API.
	Local         string    `json:"local,omitempty"`   // basename inside spool; never client controlled.
}
type Recording struct {
	Enabled       bool   `json:"enabled"`
	Bucket        string `json:"bucket"`
	TemporaryDays int    `json:"temporaryDays"`
}
type diskState struct {
	RetentionDay         string         `json:"retentionDay"`
	ControllerConfigured bool           `json:"controllerConfigured"`
	Schema               int            `json:"schema"`
	Root                 string         `json:"root"`
	Recording            Recording      `json:"recording"`
	Jobs                 map[string]Job `json:"jobs"`
}
type Manager struct {
	mu                                       sync.Mutex // metadata and runtime health; never held during Google HTTP
	op                                       sync.Mutex // serialize Drive mutations, upload chunks and persisted progress
	recordOp                                 sync.Mutex
	drive                                    *Drive
	state                                    diskState
	dir, spool, mediaAPI                     string
	client                                   *http.Client
	ctx                                      context.Context
	cancel                                   context.CancelFunc
	wg                                       sync.WaitGroup
	closeOnce                                sync.Once
	active                                   bool
	recordError, uploadError, retentionError string
	lastSweep                                time.Time
	freeSpace                                func(string) (uint64, error)
	bufferReady                              func(string, string) bool
	authorize                                func(*http.Request, string) (string, bool)
}

func Open(dir, mediaAPI string) (*Manager, error) {
	if mediaAPI == "" {
		mediaAPI = "http://127.0.0.1:9997"
	}
	u, err := url.Parse(mediaAPI)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("recording control must be a fixed numeric loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("recording control must use loopback")
	}
	if err = os.MkdirAll(filepath.Join(dir, "spool", "camera"), 0700); err != nil {
		return nil, err
	}
	d, err := New(filepath.Join(dir, "google-credentials.json"))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{drive: d, dir: dir, spool: filepath.Join(dir, "spool", "camera"), mediaAPI: mediaAPI, ctx: ctx, cancel: cancel, freeSpace: diskFree, bufferReady: boundedBuffer,
		client: &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		state:  diskState{Schema: 1, RetentionDay: time.Now().UTC().Format("2006-01-02"), Recording: Recording{Bucket: "Temporary", TemporaryDays: 7}, Jobs: map[string]Job{}}}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err == nil {
		if len(b) > 4<<20 || json.Unmarshal(b, &m.state) != nil || m.state.Schema != 1 || (m.state.Root != "" && !fileID.MatchString(m.state.Root)) || m.state.Jobs == nil || !validDay(m.state.RetentionDay) || len(m.state.Jobs) > 128 || !validRecording(m.state.Recording) {
			cancel()
			return nil, errors.New("invalid cloud state; preserve and restore a backup")
		}
		for key, j := range m.state.Jobs {
			if key != j.ID || !fileID.MatchString(j.DriveID) || j.Size <= 0 || j.Size > MaxFileSize || j.Offset < 0 || j.Offset > j.Size || !validRecording(Recording{Bucket: j.Bucket, TemporaryDays: j.TemporaryDays}) || (j.Session != "" && !d.safeSession(j.Session)) || (j.Local != "" && filepath.Base(j.Local) != j.Local) {
				cancel()
				return nil, errors.New("invalid saved cloud upload")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		cancel()
		return nil, err
	}
	return m, nil
}
func validRecording(r Recording) bool {
	return (r.Bucket == "Temporary" || r.Bucket == "Permanent") && r.TemporaryDays >= 1 && r.TemporaryDays <= 365
}
func validName(name string) bool {
	if len(name) == 0 || len(name) > 240 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (m *Manager) saveLocked() error { return atomicJSON(filepath.Join(m.dir, "state.json"), m.state) }

// commitLocked rolls back metadata on failed durable writes. Callers keep m.mu.
func (m *Manager) commitLocked(change func()) error {
	b, _ := json.Marshal(m.state)
	change()
	if err := m.saveLocked(); err != nil {
		var old diskState
		_ = json.Unmarshal(b, &old)
		m.state = old
		return errors.New("cloud metadata could not be saved; progress will be recovered from Drive")
	}
	return nil
}
func (m *Manager) Register(mux *http.ServeMux, authorize func(*http.Request, string) (string, bool)) {
	m.authorize = authorize
	mux.HandleFunc(Prefix, m.handle)
	mux.HandleFunc(Prefix+"/", m.handle)
	m.wg.Add(2)
	go m.recordLoop()
	go m.uploadLoop()
}
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.cancel()
		m.wg.Wait()
		m.recordOp.Lock()
		defer m.recordOp.Unlock()
		m.mu.Lock()
		configured := m.state.ControllerConfigured
		m.mu.Unlock()
		if configured {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			_ = m.setRecord(ctx, false)
		}
		m.client.CloseIdleConnections()
		m.drive.client.CloseIdleConnections()
	})
}
func (m *Manager) root(ctx context.Context) (string, error) {
	m.mu.Lock()
	id := m.state.Root
	m.mu.Unlock()
	if id == "" {
		var err error
		id, err = m.drive.GenerateID(ctx)
		if err != nil {
			return "", err
		}
		m.mu.Lock()
		err = m.commitLocked(func() { m.state.Root = id })
		m.mu.Unlock()
		if err != nil {
			return "", err
		}
	}
	return m.drive.EnsureFolder(ctx, id)
}
func publicJob(j Job) Job { j.Session = ""; j.Local = ""; return j }
func (m *Manager) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	owner, ok := m.authorize(r, "cloud")
	if !ok {
		respond(w, 403, map[string]string{"error": "Cloud permission required"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	if path == "/status" && r.Method == "GET" {
		bytes, _, _ := m.spoolUsage()
		m.mu.Lock()
		jobs := []Job{}
		for _, j := range m.state.Jobs {
			if j.Owner == owner || j.Kind == "recording" {
				jobs = append(jobs, publicJob(j))
			}
		}
		v := map[string]any{"credentialsPresent": m.drive.Connected(), "recording": m.state.Recording, "recordingActive": m.active, "recordingError": m.recordError, "uploadError": m.uploadError, "retentionError": m.retentionError, "lastRetentionSweep": m.lastSweep, "spoolBytes": bytes, "spoolPauseBytes": 96 << 20, "jobs": jobs, "chunkBytes": ChunkSize, "temporaryDefaultDays": 7, "folderId": m.state.Root}
		m.mu.Unlock()
		respond(w, 200, v)
		return
	}
	if path == "/recording" && r.Method == "POST" {
		if _, ok = m.authorize(r, "recording"); !ok {
			respond(w, 403, map[string]string{"error": "Recording permission required"})
			return
		}
		if _, ok = m.authorize(r, "camera"); !ok {
			respond(w, 403, map[string]string{"error": "Camera permission required"})
			return
		}
		var config Recording
		if decode(w, r, &config) != nil || !validRecording(config) {
			respond(w, 400, map[string]string{"error": "Choose Temporary or Permanent and 1–365 temporary days"})
			return
		}
		if config.Enabled && !m.bufferReady(m.spool, m.dir) {
			respond(w, 409, map[string]string{"error": "Set up the bounded recording filesystem first: hub/deploy/setup-recording-buffer.sh"})
			return
		}
		if config.Enabled {
			m.op.Lock()
			defer m.op.Unlock()
			if _, err := m.root(r.Context()); err != nil {
				failure(w, err)
				return
			}
		}
		m.recordOp.Lock()
		defer m.recordOp.Unlock()
		// Stop first to finalize the old segment before switching retention.
		if err := m.setRecord(r.Context(), false); err != nil {
			failure(w, err)
			return
		}
		m.mu.Lock()
		err := m.commitLocked(func() { m.state.Recording = config; m.state.ControllerConfigured = true })
		m.active = false
		m.mu.Unlock()
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"saved": true, "recording": config, "message": "Requested policy saved. Check status for actual recording and upload health."})
		return
	}
	if path == "/account" && r.Method == "GET" {
		m.op.Lock()
		defer m.op.Unlock()
		account, err := m.drive.Account(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, account)
		return
	}
	if path == "/files" && r.Method == "GET" {
		page := r.URL.Query().Get("pageToken")
		if len(page) > 4096 {
			respond(w, 400, map[string]string{"error": "Page token too long"})
			return
		}
		m.op.Lock()
		defer m.op.Unlock()
		root, err := m.root(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		files, next, err := m.drive.List(r.Context(), root, page)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"files": files, "nextPageToken": next})
		return
	}
	if strings.HasPrefix(path, "/files/") {
		id := strings.TrimPrefix(path, "/files/")
		if !fileID.MatchString(id) {
			respond(w, 400, map[string]string{"error": "Invalid file ID"})
			return
		}
		m.op.Lock()
		defer m.op.Unlock()
		root, err := m.root(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		switch r.Method {
		case "GET":
			f, e := m.drive.Managed(r.Context(), root, id)
			if e != nil {
				failure(w, e)
				return
			}
			respond(w, 200, f)
			return
		case "DELETE":
			err = m.drive.Trash(r.Context(), root, id)
		case "PATCH":
			var v struct {
				Bucket string `json:"bucket"`
				Days   int    `json:"days"`
			}
			if decode(w, r, &v) != nil || !validRecording(Recording{Bucket: v.Bucket, TemporaryDays: v.Days}) {
				respond(w, 400, map[string]string{"error": "Choose a bucket and 1–365 days"})
				return
			}
			var f File
			f, err = m.drive.Retain(r.Context(), root, id, v.Bucket, time.Now().Add(time.Duration(v.Days)*24*time.Hour))
			if err == nil {
				respond(w, 200, f)
				return
			}
		default:
			respond(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]bool{"trashed": true})
		return
	}
	if path == "/uploads" && r.Method == "POST" {
		var in struct {
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			Bucket string `json:"bucket"`
			Days   int    `json:"days"`
		}
		if decode(w, r, &in) != nil || !validName(in.Name) || in.Size <= 0 || in.Size > MaxFileSize || !validRecording(Recording{Bucket: in.Bucket, TemporaryDays: in.Days}) {
			respond(w, 400, map[string]string{"error": "Choose a filename, non-empty file up to 5 TB, bucket and 1–365 temporary days"})
			return
		}
		m.op.Lock()
		defer m.op.Unlock()
		j, err := m.newJob(r.Context(), owner, in.Name, in.Size, in.Bucket, in.Days, "file", "")
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 201, publicJob(j))
		return
	}
	if strings.HasPrefix(path, "/uploads/") {
		id := strings.TrimPrefix(path, "/uploads/")
		m.op.Lock()
		defer m.op.Unlock()
		m.mu.Lock()
		j, exists := m.state.Jobs[id]
		m.mu.Unlock()
		if !exists || j.Owner != owner || j.Kind != "file" {
			respond(w, 404, map[string]string{"error": "Upload not found for this device"})
			return
		}
		if r.Method == "GET" {
			j, done, err := m.advance(r.Context(), j, nil)
			if err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, map[string]any{"job": publicJob(j), "complete": done})
			return
		}
		if r.Method == "PUT" {
			offset, err := strconv.ParseInt(r.Header.Get("X-Upload-Offset"), 10, 64)
			if err != nil || offset != j.Offset {
				respond(w, 409, map[string]string{"error": "Refresh upload progress before sending another chunk"})
				return
			}
			b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, ChunkSize))
			if err != nil || len(b) == 0 || j.Offset+int64(len(b)) > j.Size || (j.Offset+int64(len(b)) < j.Size && len(b)%(256<<10) != 0) {
				respond(w, 400, map[string]string{"error": "Send chunks up to 1 MiB, aligned to 256 KiB except the final chunk"})
				return
			}
			j, done, err := m.advance(r.Context(), j, b)
			if err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, map[string]any{"job": publicJob(j), "complete": done})
			return
		}
		if r.Method == "DELETE" {
			// Cancellation explicitly trashes a completed remote object if one
			// exists; incomplete sessions expire at Google without a visible file.
			root, err := m.root(r.Context())
			if err != nil {
				failure(w, err)
				return
			}
			f, e := m.drive.Get(r.Context(), j.DriveID)
			if e == nil && owned(f, root) {
				if err = m.drive.Trash(r.Context(), root, j.DriveID); err != nil {
					failure(w, err)
					return
				}
			} else if e != nil {
				var ae *apiError
				if !errors.As(e, &ae) || ae.code != 404 {
					failure(w, e)
					return
				}
			}
			m.mu.Lock()
			err = m.commitLocked(func() { delete(m.state.Jobs, id) })
			m.mu.Unlock()
			if err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, map[string]bool{"cancelled": true})
			return
		}
	}
	respond(w, 404, map[string]string{"error": "Unknown cloud operation"})
}
func respond(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, err error) {
	respond(w, 502, map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("one JSON value required")
	}
	return nil
}
func (m *Manager) newJob(ctx context.Context, owner, name string, size int64, bucket string, days int, kind, local string) (Job, error) {
	m.mu.Lock()
	count := len(m.state.Jobs)
	perOwner := 0
	for _, j := range m.state.Jobs {
		if j.Owner == owner {
			perOwner++
		}
	}
	m.mu.Unlock()
	if count >= 128 || (kind == "file" && perOwner >= 8) {
		return Job{}, errors.New("pending upload limit reached; resume or cancel existing uploads")
	}
	if _, err := m.root(ctx); err != nil {
		return Job{}, err
	}
	id, err := m.drive.GenerateID(ctx)
	if err != nil {
		return Job{}, err
	}
	j := Job{ID: randomID(), Owner: owner, DriveID: id, Name: name, Size: size, Bucket: bucket, TemporaryDays: days, Kind: kind, ExpiresAt: time.Now().Add(time.Duration(days) * 24 * time.Hour), CreatedAt: time.Now().UTC(), Local: local}
	m.mu.Lock()
	err = m.commitLocked(func() { m.state.Jobs[j.ID] = j })
	m.mu.Unlock()
	return j, err
}
func (m *Manager) advance(ctx context.Context, j Job, data []byte) (Job, bool, error) {
	root, err := m.root(ctx)
	if err != nil {
		return j, false, err
	}
	// Recover remote commits even when the last HTTP response or local save was lost.
	f, getErr := m.drive.Get(ctx, j.DriveID)
	if getErr == nil {
		if !owned(f, root) || f.Size != strconv.FormatInt(j.Size, 10) {
			return j, false, errors.New("saved upload conflicts with its Drive file")
		}
		j.Offset = j.Size
		return j, true, m.finish(j)
	}
	var ae *apiError
	if !errors.As(getErr, &ae) || ae.code != 404 {
		return j, false, getErr
	}
	if j.Session == "" && len(data) > 0 && j.Offset > 0 {
		return j, false, errors.New("refresh upload progress before resuming")
	}
	if j.Session == "" {
		mime := "application/octet-stream"
		if j.Kind == "recording" {
			mime = "video/mp4"
		}
		j.Session, err = m.drive.Start(ctx, j.DriveID, root, j.Name, mime, j.Bucket, j.Size, j.ExpiresAt, j.Kind)
		if err != nil {
			return j, false, err
		}
		m.mu.Lock()
		err = m.commitLocked(func() { m.state.Jobs[j.ID] = j })
		m.mu.Unlock()
		if err != nil {
			return j, false, err
		}
	}
	offset, done, err := m.drive.Chunk(ctx, j.Session, j.Offset, j.Size, data)
	if err != nil {
		// Expired resumable sessions restart using the same preallocated file ID.
		if errors.As(err, &ae) && (ae.code == 404 || ae.code == 410) {
			j.Session = ""
			j.Offset = 0
			m.mu.Lock()
			saveErr := m.commitLocked(func() { m.state.Jobs[j.ID] = j })
			m.mu.Unlock()
			if saveErr != nil {
				return j, false, saveErr
			}
			return j, false, errors.New("upload session expired; refresh progress and resume from the beginning")
		}
		return j, false, err
	}
	if len(data) > 0 && (offset < j.Offset || offset > j.Offset+int64(len(data))) {
		return j, false, errors.New("unexpected upload offset; refresh progress")
	}
	j.Offset = offset
	if done {
		return j, true, m.finish(j)
	}
	m.mu.Lock()
	err = m.commitLocked(func() { m.state.Jobs[j.ID] = j })
	m.mu.Unlock()
	return j, false, err
}
func (m *Manager) finish(j Job) error {
	// Verify remote size, marker and parent before deleting local footage.
	m.mu.Lock()
	root := m.state.Root
	m.mu.Unlock()
	f, err := m.drive.Managed(m.ctx, root, j.DriveID)
	if err != nil || f.Size != strconv.FormatInt(j.Size, 10) {
		return errors.New("uploaded file verification failed; local recording retained")
	}
	if j.Local != "" {
		local, e := os.Open(filepath.Join(m.spool, j.Local))
		if e == nil {
			h := md5.New()
			_, e = io.Copy(h, local)
			local.Close()
			if e != nil || f.MD5Checksum == "" || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), f.MD5Checksum) {
				return errors.New("recording checksum verification failed; local clip retained")
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if _, err = m.drive.Retain(m.ctx, root, j.DriveID, j.Bucket, time.Now().Add(time.Duration(j.TemporaryDays)*24*time.Hour)); err != nil {
			return err
		}
		if err = os.Remove(filepath.Join(m.spool, j.Local)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = os.Remove(filepath.Join(m.spool, j.Local+".ready"))
	} else {
		if _, err = m.drive.Retain(m.ctx, root, j.DriveID, j.Bucket, time.Now().Add(time.Duration(j.TemporaryDays)*24*time.Hour)); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.commitLocked(func() { delete(m.state.Jobs, j.ID) })
}

func (m *Manager) uploadLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	backoff := time.Duration(0)
	next := time.Time{}
	nextRetention := time.Now()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		if !m.drive.Connected() || time.Now().Before(next) {
			continue
		}
		m.op.Lock()
		worked, err := m.uploadOne(m.ctx)
		m.op.Unlock()
		m.mu.Lock()
		if err != nil {
			m.uploadError = err.Error()
		} else {
			m.uploadError = ""
		}
		m.mu.Unlock()
		if err != nil {
			if backoff == 0 {
				backoff = 3 * time.Second
			} else {
				backoff *= 2
			}
			if backoff > time.Minute {
				backoff = time.Minute
			}
			next = time.Now().Add(backoff)
			continue
		}
		backoff = 0
		if !worked {
			next = time.Now().Add(3 * time.Second)
		}
		if time.Now().After(nextRetention) {
			m.op.Lock()
			sweepCtx, sweepCancel := context.WithTimeout(m.ctx, 25*time.Second)
			err = m.retention(sweepCtx)
			sweepCancel()
			m.op.Unlock()
			m.mu.Lock()
			if err != nil {
				m.retentionError = err.Error()
			} else {
				m.retentionError = ""
				m.lastSweep = time.Now().UTC()
			}
			m.mu.Unlock()
			if err != nil {
				nextRetention = time.Now().Add(time.Minute)
			} else {
				nextRetention = time.Now().Add(10 * time.Minute)
			}
		}
	}
}
func (m *Manager) uploadOne(ctx context.Context) (bool, error) {
	entries, err := os.ReadDir(m.spool)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	jobs := make([]Job, 0, len(m.state.Jobs))
	known := map[string]bool{}
	for _, j := range m.state.Jobs {
		jobs = append(jobs, j)
		if j.Local != "" {
			known[j.Local] = true
		}
	}
	m.mu.Unlock()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".mp4.ready") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".ready")
		if known[name] {
			continue
		}
		path := filepath.Join(m.spool, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(filepath.Join(m.spool, e.Name()))
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return false, errors.New("invalid completed recording; inspect spool locally")
		}
		var policy Recording
		b, err := os.ReadFile(filepath.Join(m.spool, e.Name()))
		if err != nil || len(b) > 4096 || json.Unmarshal(b, &policy) != nil || !validRecording(policy) {
			return false, errors.New("recording completion policy missing or invalid")
		}
		j, err := m.newJob(ctx, "recorder", name, info.Size(), policy.Bucket, policy.TemporaryDays, "recording", name)
		if err != nil {
			return false, err
		}
		jobs = append(jobs, j)
	}
	for _, j := range jobs {
		if j.Kind != "recording" {
			continue
		}
		// Probe first: a network timeout may have committed the preceding chunk.
		j, done, err := m.advance(ctx, j, nil)
		if err != nil {
			return false, err
		}
		if done {
			continue
		}
		f, err := os.Open(filepath.Join(m.spool, j.Local))
		if err != nil {
			return false, errors.New("pending recording is missing; restore it before retrying")
		}
		_, err = f.Seek(j.Offset, io.SeekStart)
		var b []byte
		if err == nil {
			b = make([]byte, min(int64(ChunkSize), j.Size-j.Offset))
			_, err = io.ReadFull(f, b)
		}
		f.Close()
		if err != nil {
			return false, err
		}
		_, _, err = m.advance(ctx, j, b)
		return true, err // One bounded chunk per pass.
	}
	return false, nil
}
func validDay(s string) bool { _, err := time.Parse("2006-01-02", s); return err == nil }

// Exact expiry-day queries avoid rescanning tens of thousands of video files.
// A durable day cursor catches up after outages; a failed pass is safe to retry.
func (m *Manager) retention(ctx context.Context) error {
	root, err := m.root(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	day := m.state.RetentionDay
	m.mu.Unlock()
	today := time.Now().UTC().Format("2006-01-02")
	for day <= today {
		page := ""
		for {
			files, next, err := m.drive.list(ctx, root, page, day)
			if err != nil {
				return err
			}
			for _, f := range files {
				if f.AppProperties["bucket"] != "Temporary" {
					continue
				}
				expires, e := time.Parse(time.RFC3339, f.AppProperties["expiresAt"])
				if e == nil && !time.Now().Before(expires) {
					if err = m.drive.Trash(ctx, root, f.ID); err != nil {
						return err
					}
				}
			}
			if next == "" {
				break
			}
			page = next
		}
		if day == today {
			return nil
		}
		parsed, _ := time.Parse("2006-01-02", day)
		day = parsed.AddDate(0, 0, 1).Format("2006-01-02")
		m.mu.Lock()
		err = m.commitLocked(func() { m.state.RetentionDay = day })
		m.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// RecordingEnabled reports the MediaMTX control setting, not proof of frames.
func (m *Manager) RecordingEnabled() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active }
