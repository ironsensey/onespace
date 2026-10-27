package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type nativeField struct {
	Label, Value         string
	Secret, Check, Multi bool
}
type nativeButton struct {
	Name        string
	Destructive bool
	Run         func(context.Context, []string) (int, string)
}
type nativePage struct {
	Name, Help string
	Fields     []nativeField
	Buttons    []nativeButton
	Load       func(context.Context) (int, string)
}

func (a *App) nativeRequest(ctx context.Context, method, path string, body io.Reader, headers map[string]string) (int, string) {
	if !strings.HasPrefix(path, "/local/") || strings.ContainsAny(path, "\r\n") {
		return 400, "Invalid local action"
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:47600"+path, nativeContextReader{ctx, body})
	if err != nil {
		return 400, "Invalid request"
	}
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Local-Key", a.s.deskToken)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if f, ok := body.(*os.File); ok {
		if info, e := f.Stat(); e == nil {
			req.ContentLength = info.Size()
		}
	}
	rec := httptest.NewRecorder()
	a.local(rec, req)
	return rec.Code, rec.Body.String()
}
func (a *App) nativeJSON(ctx context.Context, method, path string, value any) (int, string) {
	data, err := json.Marshal(value)
	if err != nil {
		return 400, "Invalid form"
	}
	return a.nativeRequest(ctx, method, path, bytes.NewReader(data), map[string]string{"Content-Type": "application/json"})
}
func nativeReadable(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return raw
	}
	var b strings.Builder
	var render func(any, string)
	render = func(value any, indent string) {
		switch x := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				if (strings.Contains(strings.ToLower(k), "token") && k != "nextPageToken") || k == "ticket" {
					continue
				}
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				switch x[k].(type) {
				case map[string]any, []any:
					b.WriteString(indent + k + ":\r\n")
					render(x[k], indent+"  ")
				default:
					fmt.Fprintf(&b, "%s%s: %v\r\n", indent, k, x[k])
				}
			}
		case []any:
			for _, item := range x {
				render(item, indent)
				b.WriteString("\r\n")
			}
		default:
			fmt.Fprintf(&b, "%s%v\r\n", indent, x)
		}
	}
	render(v, "")
	if b.Len() == 0 {
		return "No items."
	}
	return b.String()
}
func (a *App) nativeUpload(ctx context.Context, path, endpoint string) (int, string) {
	f, err := os.Open(path)
	if err != nil {
		return 400, "Cannot open selected file"
	}
	defer f.Close()
	return a.nativeRequest(ctx, "POST", endpoint, f, map[string]string{"Content-Type": "application/octet-stream"})
}
func (a *App) nativeCloudUpload(ctx context.Context, path, bucket string, days int, resume string) (int, string) {
	f, err := os.Open(path)
	if err != nil {
		return 400, "Cannot open source file"
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return 400, "Choose a non-empty regular file"
	}

	var job struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Offset int64  `json:"offset"`
	}
	var code int
	var result string
	if resume != "" {
		if len(resume) != 32 || !validNativeCloudID(resume) {
			return 400, "Invalid upload job ID"
		}
		code, result = a.nativeRequest(ctx, "GET", "/local/hub/cloud/uploads/"+resume, nil, nil)
		if code >= 300 {
			return code, result
		}
		var progress struct {
			Job      json.RawMessage `json:"job"`
			Complete bool            `json:"complete"`
		}
		if json.Unmarshal([]byte(result), &progress) != nil {
			return 502, "Invalid upload state"
		}
		if progress.Complete {
			return 200, "File was already uploaded"
		}
		if json.Unmarshal(progress.Job, &job) != nil || job.Name != filepath.Base(path) || job.Size != info.Size() {
			return 400, "Resume requires the same original file name, size and contents"
		}
	} else {
		code, result = a.nativeJSON(ctx, "POST", "/local/hub/cloud/uploads", map[string]any{"name": filepath.Base(path), "size": info.Size(), "bucket": bucket, "days": days})
		if code >= 300 {
			return code, result
		}
		if json.Unmarshal([]byte(result), &job) != nil || len(job.ID) != 32 {
			return 502, "Invalid upload response"
		}
	}

	for job.Offset < info.Size() {
		if ctx.Err() != nil {
			return 499, "Upload paused. Job ID: " + job.ID + ". Keep the same source file and select this job to resume."
		}
		chunk := make([]byte, min(int64(1<<20), info.Size()-job.Offset))
		if _, err = f.ReadAt(chunk, job.Offset); err != nil {
			return 400, "Source file changed or could not be read"
		}
		code, result = a.nativeRequest(ctx, "PUT", "/local/hub/cloud/uploads/"+job.ID, bytes.NewReader(chunk), map[string]string{"Content-Type": "application/octet-stream", "X-Upload-Offset": strconv.FormatInt(job.Offset, 10)})
		if code >= 300 {
			return code, result
		}
		var next struct {
			Job struct {
				Offset int64 `json:"offset"`
			} `json:"job"`
			Complete bool `json:"complete"`
		}
		if json.Unmarshal([]byte(result), &next) != nil {
			return 502, "Invalid upload progress"
		}
		if next.Complete {
			return 200, "File saved to Google Drive"
		}
		if next.Job.Offset <= job.Offset {
			return 502, "Upload did not advance"
		}
		job.Offset = next.Job.Offset
	}
	return 200, "Upload complete"
}
func (a *App) nativePages() []nativePage {
	get := func(path string) func(context.Context) (int, string) {
		return func(ctx context.Context) (int, string) { return a.nativeRequest(ctx, "GET", path, nil, nil) }
	}
	button := func(name, method, path string, body func([]string) any) nativeButton {
		return nativeButton{Name: name, Run: func(ctx context.Context, v []string) (int, string) { return a.nativeJSON(ctx, method, path, body(v)) }}
	}
	yes := func(v string) bool { return v == "true" }
	pages := []nativePage{
		{Name: "Home", Help: "Your PC certificate identifies this machine. Keep the data directory and original Android signing key when upgrading.", Fields: []nativeField{{Label: "Initial ecosystem password (12+ characters)", Secret: true}}, Load: get("/local/overview"), Buttons: []nativeButton{button("Set initial password", "POST", "/local/password", func(v []string) any { return map[string]any{"password": v[0]} })}},
		{Name: "Devices", Help: "Select a pending request ID from the list to approve pairing. Device permissions remain enforced by the PC.", Fields: []nativeField{{Label: "Device ID or pending request ID"}, {Label: "Permission: files / clipboard / camera / tasks / notes / volume / power / updates", Value: "clipboard"}, {Label: "Enable permission", Check: true, Value: "true"}}, Load: get("/local/overview"), Buttons: []nativeButton{
			button("Approve pairing", "POST", "/local/pair/decision", func(v []string) any { return map[string]any{"id": v[0], "approve": true} }),
			button("Reject pairing", "POST", "/local/pair/decision", func(v []string) any { return map[string]any{"id": v[0], "approve": false} }),
			button("Apply permission", "POST", "/local/device/permission", func(v []string) any { return map[string]any{"id": v[0], "feature": v[1], "enabled": yes(v[2])} }),
			button("Revoke device", "POST", "/local/device/revoke", func(v []string) any { return map[string]any{"id": v[0]} })}},
		{Name: "Files & clipboard", Help: "Choose a local file or type text. Save creates an item; Queue item sends an existing item to a paired device. IDs are shown below.", Fields: []nativeField{{Label: "Local file path"}, {Label: "Text", Multi: true}, {Label: "Bucket: Temporary / Permanent", Value: "Temporary"}, {Label: "Item ID"}, {Label: "Recipient device ID"}}, Load: get("/local/v1/items"), Buttons: []nativeButton{
			{Name: "Import file", Run: func(ctx context.Context, v []string) (int, string) {
				return a.nativeUpload(ctx, v[0], "/local/v1/items/file?name="+url.QueryEscape(filepath.Base(v[0]))+"&bucket="+url.QueryEscape(v[2]))
			}},
			button("Save text", "POST", "/local/v1/items/text", func(v []string) any { return map[string]any{"text": v[1], "bucket": v[2]} }),
			button("Queue item", "POST", "/local/v1/outbox", func(v []string) any { return map[string]any{"to": v[4], "item_id": v[3]} }),
			{Name: "Keep permanently", Run: func(ctx context.Context, v []string) (int, string) {
				if !validNativeItem(v[3]) {
					return 400, "Choose a valid item ID"
				}
				return a.nativeJSON(ctx, "POST", "/local/v1/items/"+v[3]+"/pin", map[string]any{})
			}},
			{Name: "Open storage folder", Run: func(ctx context.Context, v []string) (int, string) {
				return a.nativeJSON(ctx, "POST", "/local/open-folder", map[string]string{"bucket": v[2]})
			}},
			{Name: "Delete item", Destructive: true, Run: func(ctx context.Context, v []string) (int, string) {
				if !validNativeItem(v[3]) {
					return 400, "Choose a valid item ID"
				}
				return a.nativeJSON(ctx, "DELETE", "/local/v1/items/"+v[3], nil)
			}}}},
		{Name: "Tasks", Help: "Leave ID empty to create a task. Use an existing ID to update or delete it.", Fields: []nativeField{{Label: "Task ID (blank for new)"}, {Label: "Task text"}, {Label: "Completed", Check: true}}, Load: get("/local/v1/tasks"), Buttons: []nativeButton{
			button("Save task", "POST", "/local/v1/tasks", func(v []string) any { return map[string]any{"id": v[0], "text": v[1], "done": yes(v[2])} }),
			button("Delete task", "POST", "/local/v1/tasks", func(v []string) any { return map[string]any{"id": v[0], "delete": true} })}},
		{Name: "Notes", Help: "Leave ID empty to create a note. Saved notes use the existing shared workspace.", Fields: []nativeField{{Label: "Note ID (blank for new)"}, {Label: "Title"}, {Label: "Body", Multi: true}}, Load: get("/local/v1/notes"), Buttons: []nativeButton{
			button("Save note", "POST", "/local/v1/notes", func(v []string) any { return map[string]any{"id": v[0], "title": v[1], "body": v[2]} }),
			button("Delete note", "POST", "/local/v1/notes", func(v []string) any { return map[string]any{"id": v[0], "delete": true} })}},
	}
	c := a.hub.snapshot()
	pages = append(pages, nativePage{Name: "Pi connection", Help: "Verify the Pi fingerprint through a trusted console. OneSpace Connect provides encrypted PC access through the Pi; both devices need the connect scope.", Fields: []nativeField{
		{Label: "Hub HTTPS address", Value: c.Address}, {Label: "Hub SHA-256 fingerprint", Value: c.Fingerprint}, {Label: "New device token (blank keeps current)", Secret: true}, {Label: "Recipient hub device ID", Value: c.Target},
		{Label: "Optional Pi LAN HTTPS address", Value: c.LocalAddress}, {Label: "Enable hub", Check: true, Value: strconv.FormatBool(c.Enabled)}, {Label: "Enable built-in OneSpace Connect", Check: true, Value: strconv.FormatBool(c.Connect)},
		{Label: "Receive hub clipboard", Check: true, Value: strconv.FormatBool(c.ReceiveClipboard)}, {Label: "Allow media keys", Check: true, Value: strconv.FormatBool(c.AllowMedia)}, {Label: "Copied-text route: lan / hub", Value: c.ClipboardMode},
	}, Load: get("/local/hub/config"), Buttons: []nativeButton{{Name: "Save Pi connection", Run: func(ctx context.Context, v []string) (int, string) {
		current := a.hub.snapshot()
		current.Address = strings.TrimSpace(v[0])
		current.Fingerprint = strings.TrimSpace(v[1])
		current.Token = strings.TrimSpace(v[2])
		current.Target = strings.TrimSpace(v[3])
		current.LocalAddress = strings.TrimSpace(v[4])
		current.Enabled = yes(v[5])
		current.Connect = yes(v[6])
		current.ReceiveClipboard = yes(v[7])
		current.AllowMedia = yes(v[8])
		current.ClipboardMode = strings.TrimSpace(v[9])
		return a.nativeJSON(ctx, "POST", "/local/hub/config", current)
	}}, {Name: "Refresh devices", Run: func(ctx context.Context, _ []string) (int, string) {
		return a.nativeRequest(ctx, "GET", "/local/hub/devices", nil, nil)
	}}}})
	pages = append(pages, nativePage{Name: "Cloud & recordings", Help: "Connect Google Drive on the Pi first. Recording creates short MP4 clips; check actual uploaded clips. Delete moves a Drive file to Trash, which still consumes quota.", Fields: []nativeField{{Label: "Local file path"}, {Label: "Bucket: Temporary / Permanent", Value: "Temporary"}, {Label: "Temporary days (1–365)", Value: "7"}, {Label: "Drive file ID"}, {Label: "Next page token (from list, optional)"}, {Label: "Upload job ID to resume (optional)"}}, Load: get("/local/hub/cloud/status"), Buttons: []nativeButton{
		{Name: "Account & quota", Run: func(ctx context.Context, _ []string) (int, string) {
			return a.nativeRequest(ctx, "GET", "/local/hub/cloud/account", nil, nil)
		}},
		{Name: "List files", Run: func(ctx context.Context, v []string) (int, string) {
			path := "/local/hub/cloud/files"
			if v[4] != "" {
				path += "?pageToken=" + url.QueryEscape(v[4])
			}
			return a.nativeRequest(ctx, "GET", path, nil, nil)
		}},
		{Name: "Upload file", Run: func(ctx context.Context, v []string) (int, string) {
			days, _ := strconv.Atoi(v[2])
			return a.nativeCloudUpload(ctx, v[0], v[1], days, v[5])
		}},
		button("Start recording", "POST", "/local/hub/cloud/recording", func(v []string) any {
			days, _ := strconv.Atoi(v[2])
			return map[string]any{"enabled": true, "bucket": v[1], "temporaryDays": days}
		}),
		button("Stop recording", "POST", "/local/hub/cloud/recording", func(v []string) any {
			return map[string]any{"enabled": false, "bucket": "Temporary", "temporaryDays": 7}
		}),
		{Name: "Change retention", Run: func(ctx context.Context, v []string) (int, string) {
			if !validNativeCloudID(v[3]) {
				return 400, "Choose a Drive file ID"
			}
			days, _ := strconv.Atoi(v[2])
			return a.nativeJSON(ctx, "PATCH", "/local/hub/cloud/files/"+v[3], map[string]any{"bucket": v[1], "days": days})
		}},
		{Name: "Open in Drive", Run: func(ctx context.Context, v []string) (int, string) {
			if !validNativeCloudID(v[3]) {
				return 400, "Choose a Drive file ID"
			}
			openBrowser("https://drive.google.com/file/d/" + v[3] + "/view")
			return 200, "Opened Google Drive"
		}},
		{Name: "Move to Trash", Destructive: true, Run: func(ctx context.Context, v []string) (int, string) {
			if !validNativeCloudID(v[3]) {
				return 400, "Choose a Drive file ID"
			}
			return a.nativeJSON(ctx, "DELETE", "/local/hub/cloud/files/"+v[3], nil)
		}},
	}})
	pages = append(pages, nativePage{Name: "Media controls", Help: "These are OS media keys. The receiving Windows device must enable media control; this is not a Spotify account integration.", Fields: []nativeField{{Label: "Recipient hub device ID", Value: c.Target}}, Load: get("/local/hub/devices")})
	for _, entry := range []struct{ name, action string }{{"Play / Pause", "play_pause"}, {"Next", "next"}, {"Previous", "previous"}, {"Stop", "stop"}} {
		e := entry
		pages[len(pages)-1].Buttons = append(pages[len(pages)-1].Buttons, button(e.name, "POST", "/local/hub/send", func(v []string) any { return map[string]any{"to": v[0], "kind": "media", "action": e.action} }))
	}
	pages = append(pages, nativePage{Name: "Updates", Help: "Choose an APK signed with your ORIGINAL Android key. Publishing does not sign it. Android checks package, version, hash and signer before installation.", Fields: []nativeField{{Label: "Signed APK path"}}, Load: get("/local/v1/update/manifest"), Buttons: []nativeButton{{Name: "Publish signed APK", Run: func(ctx context.Context, v []string) (int, string) {
		return a.nativeUpload(ctx, v[0], "/local/updates/upload")
	}}}})
	pages = append(pages, nativePage{Name: "Settings", Help: "Changes apply to this PC. Power requests still need approval. Do not enable permissions for devices you do not trust.", Fields: []nativeField{{Label: "Automatic clipboard sharing", Check: true}, {Label: "Start on Windows login", Check: true}, {Label: "Allow remote power requests", Check: true}, {Label: "Volume (0–100)", Value: "50"}, {Label: "Pending power request ID"}}, Load: get("/local/overview"), Buttons: []nativeButton{
		button("Apply clipboard", "POST", "/local/clipboard", func(v []string) any { return map[string]any{"enabled": yes(v[0])} }),
		button("Apply startup", "POST", "/local/startup", func(v []string) any { return map[string]any{"enabled": yes(v[1])} }),
		button("Apply power policy", "POST", "/local/v1/settings/power", func(v []string) any { return map[string]any{"enabled": yes(v[2])} }),
		button("Set volume", "POST", "/local/v1/actions/volume", func(v []string) any { n, _ := strconv.Atoi(v[3]); return map[string]any{"percent": n} }),
		{Name: "Pending power requests", Run: func(ctx context.Context, _ []string) (int, string) {
			return a.nativeRequest(ctx, "GET", "/local/commands", nil, nil)
		}},
		button("Approve power request", "POST", "/local/power/decision", func(v []string) any { return map[string]any{"id": v[4], "approve": true} }),
		button("Reject power request", "POST", "/local/power/decision", func(v []string) any { return map[string]any{"id": v[4], "approve": false} }),
	}})
	pages = append(pages, nativePage{Name: "Cloud notes", Help: "Create a text file in Google Drive. Temporary expiry moves it to Trash; Trash still uses quota.", Fields: []nativeField{{Label: "File name", Value: "Note.txt"}, {Label: "Text", Multi: true}, {Label: "Bucket: Temporary / Permanent", Value: "Temporary"}, {Label: "Temporary days", Value: "7"}}, Buttons: []nativeButton{{Name: "Create text file", Run: func(ctx context.Context, v []string) (int, string) {
		if len(v[1]) > 65536 || strings.TrimSpace(v[0]) == "" {
			return 400, "Choose a name and text up to 64 KiB"
		}
		dir, err := os.MkdirTemp("", "onespace-note-")
		if err != nil {
			return 500, "Cannot prepare note"
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, safeName(v[0]))
		if err = os.WriteFile(path, []byte(v[1]), 0600); err != nil {
			return 500, "Cannot prepare note"
		}
		days, _ := strconv.Atoi(v[3])
		return a.nativeCloudUpload(ctx, path, v[2], days, "")
	}}}})

	for i := range pages {
		if pages[i].Name == "Updates" {
			pages[i].Buttons = append(pages[i].Buttons, nativeButton{Name: "Reserve Android version code", Run: func(ctx context.Context, _ []string) (int, string) {
				minimum := int64(29)
				updateMu.RLock()
				if published, err := a.readUpdate(); err == nil {
					minimum = max(minimum, published.VersionCode+1)
				}
				updateMu.RUnlock()
				return a.nativeJSON(ctx, "POST", "/local/hub/updates/reserve", map[string]int64{"minimumCode": minimum})
			}})
			pages[i].Buttons = append(pages[i].Buttons,
				nativeButton{Name: "Publish to Pi", Run: func(ctx context.Context, v []string) (int, string) {
					return a.nativeUpload(ctx, v[0], "/local/hub/updates/publish")
				}},
				nativeButton{Name: "Pi published release", Run: func(ctx context.Context, _ []string) (int, string) {
					return a.nativeRequest(ctx, "GET", "/local/hub/updates/manifest", nil, nil)
				}})
		}
		if pages[i].Name == "Settings" {
			a.s.Lock()
			pages[i].Fields[0].Value = strconv.FormatBool(a.s.Data.AutoClipboard)
			pages[i].Fields[2].Value = strconv.FormatBool(a.s.Data.RemotePower)
			a.s.Unlock()
			pages[i].Fields[1].Value = strconv.FormatBool(startupEnabled())
		}
	}

	for i := range pages {
		for j := range pages[i].Buttons {
			name := pages[i].Buttons[j].Name
			if strings.Contains(name, "Delete") || strings.Contains(name, "Revoke") || strings.Contains(name, "Approve power") {
				pages[i].Buttons[j].Destructive = true
			}
		}
	}
	return pages
}
func validNativeItem(s string) bool {
	if len(s) != 24 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func validNativeCloudID(s string) bool {
	if len(s) < 8 || len(s) > 200 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

type nativeContextReader struct {
	context context.Context
	input   io.Reader
}

func (r nativeContextReader) Read(b []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	if r.input == nil {
		return 0, io.EOF
	}
	return r.input.Read(b)
}
