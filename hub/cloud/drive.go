// Package cloud implements the opt-in Google Drive storage layer. Credentials
// never reach clients; only app-created files in the verified app folder appear.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const Scope = "https://www.googleapis.com/auth/drive.file"
const ChunkSize = 1 << 20
const managed = "onespace-v1"

var fileID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,200}$`)

type Credentials struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
}
type File struct {
	MD5Checksum   string            `json:"md5Checksum,omitempty"`
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Size          string            `json:"size,omitempty"`
	MimeType      string            `json:"mimeType"`
	CreatedTime   string            `json:"createdTime,omitempty"`
	Trashed       bool              `json:"trashed"`
	Parents       []string          `json:"parents,omitempty"`
	AppProperties map[string]string `json:"appProperties"`
}
type apiError struct{ code int }

func (e *apiError) Error() string {
	return fmt.Sprintf("Google Drive request failed (HTTP %d)", e.code)
}

type Drive struct {
	mu                    sync.Mutex
	hasCredential         atomic.Bool
	credentials           Credentials
	credentialPath        string
	access                string
	expires               time.Time
	client                *http.Client
	api, upload, tokenURL string
}

// New uses only fixed Google endpoints. Callers cannot redirect credentials to
// an arbitrary URL. Test fixtures replace these private fields in-package.
func New(path string) (*Drive, error) {
	d := &Drive{credentialPath: path, api: "https://www.googleapis.com/drive/v3", upload: "https://www.googleapis.com/upload/drive/v3", tokenURL: "https://oauth2.googleapis.com/token",
		client: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 || len(b) > 32768 {
		return nil, errors.New("Drive credentials must be an owner-only file under 32 KiB")
	}
	if json.Unmarshal(b, &d.credentials) != nil || d.credentials.ClientID == "" || d.credentials.RefreshToken == "" {
		return nil, errors.New("invalid Drive credentials; reconnect locally")
	}
	d.hasCredential.Store(true)
	return d, nil
}
func (d *Drive) Connected() bool { return d.hasCredential.Load() }
func atomicJSON(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".save-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if dir, e := os.Open(filepath.Dir(path)); e == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
func (d *Drive) bearer(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.credentials.RefreshToken == "" {
		return "", errors.New("Google Drive is not connected; run drive-connect on your Pi")
	}
	if d.access != "" && time.Now().Add(time.Minute).Before(d.expires) {
		return d.access, nil
	}
	v := url.Values{"client_id": {d.credentials.ClientID}, "client_secret": {d.credentials.ClientSecret}, "refresh_token": {d.credentials.RefreshToken}, "grant_type": {"refresh_token"}}
	req, _ := http.NewRequestWithContext(ctx, "POST", d.tokenURL, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := d.client.Do(req)
	if err != nil {
		return "", errors.New("Google sign-in refresh unavailable")
	}
	defer res.Body.Close()
	var t struct {
		Access  string `json:"access_token"`
		Expires int    `json:"expires_in"`
		Refresh string `json:"refresh_token"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 32768)).Decode(&t) != nil || t.Access == "" {
		return "", errors.New("Google sign-in expired or was revoked; reconnect Drive locally")
	}
	if t.Refresh != "" && t.Refresh != d.credentials.RefreshToken {
		next := d.credentials
		next.RefreshToken = t.Refresh
		if err = atomicJSON(d.credentialPath, next); err != nil {
			return "", errors.New("cannot save renewed Google credential")
		}
		d.credentials = next
	}
	d.access = t.Access
	d.expires = time.Now().Add(time.Duration(t.Expires) * time.Second)
	return t.Access, nil
}
func (d *Drive) request(ctx context.Context, method, endpoint string, body io.Reader, headers map[string]string) (*http.Response, error) {
	token, err := d.bearer(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, errors.New("invalid Drive request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := d.client.Do(req)
	if err != nil {
		return nil, errors.New("Google Drive is unreachable; transfer can be resumed")
	}
	if res.StatusCode == 401 {
		d.mu.Lock()
		d.access = ""
		d.mu.Unlock()
	}
	return res, nil
}
func (d *Drive) json(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	res, err := d.request(ctx, method, d.api+path, body, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &apiError{res.StatusCode}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(out)
	}
	return nil
}

const fields = "id,name,size,md5Checksum,mimeType,createdTime,trashed,parents,appProperties"

func (d *Drive) Get(ctx context.Context, id string) (File, error) {
	if !fileID.MatchString(id) {
		return File{}, errors.New("invalid Drive file ID")
	}
	var f File
	err := d.json(ctx, "GET", "/files/"+id+"?fields="+url.QueryEscape(fields), nil, &f)
	return f, err
}
func (d *Drive) GenerateID(ctx context.Context) (string, error) {
	var r struct {
		IDs []string `json:"ids"`
	}
	err := d.json(ctx, "GET", "/files/generateIds?count=1&space=drive&type=files", nil, &r)
	if err != nil {
		return "", err
	}
	if len(r.IDs) != 1 || !fileID.MatchString(r.IDs[0]) {
		return "", errors.New("invalid Drive allocation")
	}
	return r.IDs[0], nil
}
func (d *Drive) EnsureFolder(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", errors.New("Drive root ID has not been allocated")
	}
	f, err := d.Get(ctx, id)
	var ae *apiError
	if errors.As(err, &ae) && ae.code == 404 {
		f = File{ID: id, Name: "OneSpace", MimeType: "application/vnd.google-apps.folder", AppProperties: map[string]string{"managedBy": managed, "role": "root"}}
		err = d.json(ctx, "POST", "/files?fields="+url.QueryEscape(fields), f, &f)
		// A previous POST may have committed before losing its response.
		if err != nil {
			f, err = d.Get(ctx, id)
		}
	}
	if err != nil {
		return "", err
	}
	if f.Trashed || f.MimeType != "application/vnd.google-apps.folder" || f.AppProperties["managedBy"] != managed || f.AppProperties["role"] != "root" {
		return "", errors.New("OneSpace Drive folder was moved to Trash or its ownership marker changed; restore it in Drive")
	}
	return f.ID, nil
}
func (d *Drive) List(ctx context.Context, root, page string) ([]File, string, error) {
	return d.list(ctx, root, page, "")
}
func (d *Drive) list(ctx context.Context, root, page, day string) ([]File, string, error) {
	q := "'" + root + "' in parents and trashed = false and appProperties has { key='managedBy' and value='" + managed + "' }"
	if day != "" {
		q += " and appProperties has { key='bucket' and value='Temporary' } and appProperties has { key='expiresOn' and value='" + day + "' }"
	}
	v := url.Values{"q": {q}, "spaces": {"drive"}, "pageSize": {"100"}, "fields": {"nextPageToken,files(" + fields + ")"}, "orderBy": {"createdTime desc"}}
	if day != "" {
		v.Set("orderBy", "createdTime")
	}
	if page != "" {
		v.Set("pageToken", page)
	}
	var result struct {
		Files []File `json:"files"`
		Next  string `json:"nextPageToken"`
	}
	err := d.json(ctx, "GET", "/files?"+v.Encode(), nil, &result)
	if result.Files == nil {
		result.Files = []File{}
	}
	return result.Files, result.Next, err
}
func owned(f File, root string) bool {
	if f.Trashed || f.AppProperties["managedBy"] != managed || f.MimeType == "application/vnd.google-apps.folder" {
		return false
	}
	for _, p := range f.Parents {
		if p == root {
			return true
		}
	}
	return false
}
func (d *Drive) Managed(ctx context.Context, root, id string) (File, error) {
	f, err := d.Get(ctx, id)
	if err != nil {
		return f, err
	}
	if !owned(f, root) {
		return f, errors.New("only OneSpace-created files in its Drive folder can be managed")
	}
	return f, nil
}
func (d *Drive) Trash(ctx context.Context, root, id string) error {
	if _, err := d.Managed(ctx, root, id); err != nil {
		return err
	}
	return d.json(ctx, "PATCH", "/files/"+id, map[string]any{"trashed": true}, nil)
}
func (d *Drive) Retain(ctx context.Context, root, id, bucket string, expires time.Time) (File, error) {
	f, err := d.Managed(ctx, root, id)
	if err != nil {
		return f, err
	}
	if bucket != "Temporary" && bucket != "Permanent" {
		return f, errors.New("invalid retention bucket")
	}
	f.AppProperties["bucket"] = bucket
	f.AppProperties["uploadState"] = "complete"
	f.AppProperties["expiresAt"] = ""
	f.AppProperties["expiresOn"] = ""
	if bucket == "Temporary" {
		f.AppProperties["expiresAt"] = expires.UTC().Format(time.RFC3339)
		f.AppProperties["expiresOn"] = expires.UTC().Format("2006-01-02")
	}
	err = d.json(ctx, "PATCH", "/files/"+id+"?fields="+url.QueryEscape(fields), map[string]any{"appProperties": f.AppProperties}, &f)
	return f, err
}

// Session URLs are bearer-like secrets and are accepted only from Google.
func (d *Drive) safeSession(s string) bool {
	u, e := url.Parse(s)
	base, _ := url.Parse(d.upload)
	return e == nil && u.Scheme == base.Scheme && u.Host == base.Host && u.User == nil && u.Fragment == "" && u.RawPath == "" && u.Path == base.Path+"/files"
}
func (d *Drive) Start(ctx context.Context, id, root, name, mime, bucket string, size int64, expires time.Time, kind string) (string, error) {
	props := map[string]string{"managedBy": managed, "bucket": bucket, "kind": kind, "uploadState": "pending"}
	b, _ := json.Marshal(map[string]any{"id": id, "name": name, "mimeType": mime, "parents": []string{root}, "appProperties": props})
	res, err := d.request(ctx, "POST", d.upload+"/files?uploadType=resumable&fields="+url.QueryEscape(fields), bytes.NewReader(b), map[string]string{"Content-Type": "application/json", "X-Upload-Content-Type": mime, "X-Upload-Content-Length": strconv.FormatInt(size, 10)})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", &apiError{res.StatusCode}
	}
	s := res.Header.Get("Location")
	if !d.safeSession(s) {
		return "", errors.New("Google returned an invalid upload location")
	}
	return s, nil
}

// Chunk sends bounded bytes. A 308 is progress, not a redirect. Never follow a
// server redirect with Authorization or an upload-session secret.
func (d *Drive) Chunk(ctx context.Context, session string, offset, total int64, data []byte) (int64, bool, error) {
	if !d.safeSession(session) {
		return 0, false, errors.New("invalid saved upload session")
	}
	rangeHeader := "bytes */" + strconv.FormatInt(total, 10)
	if len(data) > 0 {
		rangeHeader = fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(len(data))-1, total)
	}
	res, err := d.request(ctx, "PUT", session, bytes.NewReader(data), map[string]string{"Content-Type": "application/octet-stream", "Content-Range": rangeHeader})
	if err != nil {
		return 0, false, err
	}
	defer res.Body.Close()
	if res.StatusCode == 200 || res.StatusCode == 201 {
		return total, true, nil
	}
	if res.StatusCode != 308 {
		return 0, false, &apiError{res.StatusCode}
	}
	r := res.Header.Get("Range")
	if r == "" {
		return 0, false, nil
	}
	if !strings.HasPrefix(r, "bytes=0-") {
		return 0, false, errors.New("invalid upload progress")
	}
	n, e := strconv.ParseInt(strings.TrimPrefix(r, "bytes=0-"), 10, 64)
	if e != nil || n < 0 || n >= total {
		return 0, false, errors.New("invalid upload progress")
	}
	return n + 1, false, nil
}

type Account struct {
	User struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"emailAddress"`
	} `json:"user"`
	StorageQuota struct {
		Limit string `json:"limit"`
		Usage string `json:"usage"`
		Trash string `json:"usageInDriveTrash"`
	} `json:"storageQuota"`
}

func (d *Drive) Account(ctx context.Context) (Account, error) {
	var a Account
	err := d.json(ctx, "GET", "/about?fields="+url.QueryEscape("user(displayName,emailAddress),storageQuota(limit,usage,usageInDriveTrash)"), nil, &a)
	return a, err
}
