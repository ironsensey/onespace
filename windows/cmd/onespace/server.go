package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"onespace.local/desktop/internal/clipboard"
)

const maxUpload int64 = 512 << 20
const appVersion = "0.8.0-preview"

type powerRequest struct {
	Action  string
	Created time.Time
}

type App struct {
	hub          *HubBridge
	s            *State
	fingerprint  string
	attempts     sync.Mutex
	failed       map[string][]time.Time
	powerMu      sync.Mutex
	powers       map[string]powerRequest
	clipMu       sync.Mutex
	clipSeq      uint32
	clipFailures int
	clipReady    bool
	// Closed and replaced after each outbox change. Waiters sleep without polling.
	outboxWake chan struct{}
}

func newApp(s *State, fingerprint string) *App {
	a := &App{s: s, fingerprint: fingerprint, failed: map[string][]time.Time{}, powers: map[string]powerRequest{}, outboxWake: make(chan struct{})}
	a.hub = newHubBridge(a)
	return a
}
func jsonReply(w http.ResponseWriter, status int, anything any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(anything)
}
func bad(w http.ResponseWriter, status int, message string) {
	jsonReply(w, status, map[string]string{"error": message})
}
func readJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
func clientIP(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e == nil {
		return host
	}
	return r.RemoteAddr
}
func (a *App) throttle(ip string) bool {
	a.attempts.Lock()
	defer a.attempts.Unlock()
	now := time.Now()
	old := a.failed[ip]
	recent := old[:0]
	for _, t := range old {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 8 {
		a.failed[ip] = recent
		return true
	}
	a.failed[ip] = append(recent, now)
	return false
}
func (a *App) remote(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil {
		bad(w, 403, "TLS required")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/pair/") {
		a.pair(w, r)
		return
	}
	if r.URL.Path == "/v1/info" && r.Method == http.MethodGet {
		jsonReply(w, 200, map[string]any{"app": "OneSpace", "version": appVersion, "paired": a.s.hasPassword()})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	device, ok := a.s.deviceByToken(token)
	if !ok {
		bad(w, 401, "Device is not paired or has been revoked")
		return
	}
	if scope := operationScope(r); scope != "" && device.Permissions != nil && func() bool { v, exists := device.Permissions[scope]; return exists && !v }() {
		bad(w, 403, "Administrator has disabled "+scope+" for this device")
		return
	}
	a.api(w, r, device.ID, false)
}

// Server-side feature gates. A missing permission means enabled for older paired devices.
var permissionScopes = map[string]bool{
	"files": true, "clipboard": true, "camera": true, "tasks": true, "notes": true,
	"volume": true, "power": true, "updates": true,
}

func operationScope(r *http.Request) string {
	p := r.URL.Path
	switch {
	case p == "/v1/clipboard/pull" || p == "/v1/items/text":
		return "clipboard"
	case p == "/v1/items/file" && r.URL.Query().Get("clipboard") == "1":
		return "camera"
	case strings.HasPrefix(p, "/v1/items") || strings.HasPrefix(p, "/v1/outbox"):
		return "files"
	case strings.HasPrefix(p, "/v1/tasks"):
		return "tasks"
	case strings.HasPrefix(p, "/v1/notes"):
		return "notes"
	case p == "/v1/actions/volume":
		return "volume"
	case p == "/v1/actions/power":
		return "power"
	case strings.HasPrefix(p, "/v1/update/"):
		return "updates"
	}
	return ""
}
func (a *App) pair(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/pair/request":
		if r.Method != "POST" {
			bad(w, 405, "Method not allowed")
			return
		}
		var p struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if e := readJSON(r, &p); e != nil {
			bad(w, 400, "Invalid request")
			return
		}
		if a.throttle(clientIP(r)) {
			bad(w, 429, "Too many pairing attempts; try again later")
			return
		}
		if !a.s.checkPassword(p.Password) {
			bad(w, 403, "Password incorrect")
			return
		}
		if len(p.Name) > 64 || strings.TrimSpace(p.Name) == "" {
			bad(w, 400, "Invalid device name")
			return
		}
		pnd := &Pending{ID: randomHex(12), Ticket: randomHex(24), Name: strings.TrimSpace(p.Name), At: time.Now().UTC()}
		a.s.Lock()
		a.s.Pending[pnd.ID] = pnd
		a.s.eventLocked("Pairing requested by " + pnd.Name)
		_ = a.s.saveLocked()
		a.s.Unlock()
		jsonReply(w, 202, map[string]string{"id": pnd.ID, "ticket": pnd.Ticket, "state": "pending"})
	case "/v1/pair/status":
		if r.Method != "GET" {
			bad(w, 405, "Method not allowed")
			return
		}
		id, ticket := r.URL.Query().Get("id"), r.URL.Query().Get("ticket")
		a.s.Lock()
		p, ok := a.s.Pending[id]
		if !ok || len(ticket) != len(p.Ticket) || subtle.ConstantTimeCompare([]byte(ticket), []byte(p.Ticket)) != 1 || time.Since(p.At) > 5*time.Minute {
			a.s.Unlock()
			bad(w, 404, "Pair request expired")
			return
		}
		state := "pending"
		if p.Rejected {
			state = "rejected"
		}
		if p.Approved {
			state = "approved"
		}
		token := p.Token
		a.s.Unlock()
		jsonReply(w, 200, map[string]string{"state": state, "token": token})
	default:
		bad(w, 404, "Not found")
	}
}
func (a *App) local(w http.ResponseWriter, r *http.Request) {
	if host := r.Host; !strings.HasPrefix(host, "127.0.0.1:") && !strings.HasPrefix(host, "localhost:") {
		bad(w, 403, "Invalid local host")
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		a.serveUI(w)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/local/") {
		bad(w, 404, "Not found")
		return
	}
	if r.Header.Get("X-Local-Key") != a.s.deskToken {
		bad(w, 403, "Local access denied")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/local/hub/") {
		a.hubLocal(w, r)
		return
	}

	switch r.URL.Path {
	case "/local/updates/upload":
		a.uploadUpdate(w, r)
		return
	case "/local/overview":
		if r.Method != "GET" {
			bad(w, 405, "Method not allowed")
			return
		}
		a.s.cleanup()
		a.s.Lock()
		devices := append([]Device{}, a.s.Data.Devices...)
		for i := range devices {
			devices[i].Token = ""
		}
		items := append([]Item{}, a.s.Data.Items...)
		tasks := append([]Task{}, a.s.Data.Tasks...)
		notes := append([]Note{}, a.s.Data.Notes...)
		events := append([]Event{}, a.s.Data.Events...)
		pending := []Pending{}
		for _, p := range a.s.Pending {
			if !p.Approved && !p.Rejected {
				pending = append(pending, *p)
			}
		}
		outbound := append([]Outbox{}, a.s.Data.Outbox...)
		remotePower := a.s.Data.RemotePower
		autoClipboard := a.s.Data.AutoClipboard
		set := a.s.Data.PassHash != ""
		a.s.Unlock()
		jsonReply(w, 200, map[string]any{"devices": devices, "items": items, "tasks": tasks, "notes": notes, "events": events, "pending": pending, "outbox": outbound, "fingerprint": a.fingerprint, "configured": set, "ips": lanAddresses(), "remote_power": remotePower, "auto_clipboard": autoClipboard, "data_path": a.s.root, "start_on_login": startupEnabled()})
		return
	case "/local/password":
		if r.Method != "POST" {
			bad(w, 405, "Method not allowed")
			return
		}
		var p struct {
			Password string `json:"password"`
		}
		if readJSON(r, &p) != nil {
			bad(w, 400, "Bad JSON")
			return
		}
		if a.s.hasPassword() {
			bad(w, 409, "Password already set; rotate from a trusted workflow later")
			return
		}
		if err := a.s.SetPassword(p.Password); err != nil {
			bad(w, 400, err.Error())
			return
		}
		jsonReply(w, 200, map[string]string{"status": "ok"})
		return
	case "/local/pair/decision":
		if r.Method != "POST" {
			bad(w, 405, "Method not allowed")
			return
		}
		var q struct {
			ID      string `json:"id"`
			Approve bool   `json:"approve"`
		}
		if readJSON(r, &q) != nil {
			bad(w, 400, "Bad JSON")
			return
		}
		a.s.Lock()
		p, ok := a.s.Pending[q.ID]
		if !ok || time.Since(p.At) > 5*time.Minute || p.Approved || p.Rejected {
			a.s.Unlock()
			bad(w, 404, "Pairing request not available")
			return
		}
		if q.Approve {
			p.Approved = true
			p.Token = randomHex(32)
			a.s.Data.Devices = append(a.s.Data.Devices, Device{ID: p.ID, Name: p.Name, Token: p.Token, PairedAt: time.Now().UTC()})
			a.s.eventLocked("Device paired: " + p.Name)
		} else {
			p.Rejected = true
			a.s.eventLocked("Pairing rejected: " + p.Name)
		}
		err := a.s.saveLocked()
		a.s.Unlock()
		if err != nil {
			bad(w, 500, "Could not save credentials")
			return
		}
		jsonReply(w, 200, map[string]string{"status": "ok"})
		return
	case "/local/clipboard":
		if r.Method != http.MethodPost {
			bad(w, 405, "Method not allowed")
			return
		}
		var q struct {
			Enabled bool `json:"enabled"`
		}
		if readJSON(r, &q) != nil {
			bad(w, 400, "Invalid JSON")
			return
		}
		a.s.Lock()
		a.s.Data.AutoClipboard = q.Enabled
		err := a.s.saveLocked()
		a.s.Unlock()
		if err != nil {
			bad(w, 500, "Cannot save clipboard preference")
			return
		}
		jsonReply(w, 200, map[string]bool{"enabled": q.Enabled})
		return
	case "/local/startup":
		if r.Method != "POST" {
			bad(w, 405, "Method not allowed")
			return
		}
		var q struct {
			Enabled bool `json:"enabled"`
		}
		if readJSON(r, &q) != nil {
			bad(w, 400, "Invalid JSON")
			return
		}
		if err := setStartup(q.Enabled); err != nil {
			bad(w, 500, err.Error())
			return
		}
		jsonReply(w, 200, map[string]bool{"enabled": startupEnabled()})
		return
	case "/local/device/permission":
		if r.Method != "POST" {
			bad(w, 405, "Method not allowed")
			return
		}
		var q struct {
			ID      string `json:"id"`
			Feature string `json:"feature"`
			Enabled bool   `json:"enabled"`
		}
		if readJSON(r, &q) != nil || !permissionScopes[q.Feature] {
			bad(w, 400, "Invalid device or feature")
			return
		}
		a.s.Lock()
		found := false
		for i := range a.s.Data.Devices {
			d := &a.s.Data.Devices[i]
			if d.ID != q.ID {
				continue
			}
			if d.Permissions == nil {
				d.Permissions = make(map[string]bool)
			}
			d.Permissions[q.Feature] = q.Enabled
			a.s.eventLocked(fmt.Sprintf("Permission %s=%t for %s", q.Feature, q.Enabled, d.Name))
			a.signalOutboxLocked()
			found = true
			break
		}
		if !found {
			a.s.Unlock()
			bad(w, 404, "Device not found")
			return
		}
		err := a.s.saveLocked()
		a.s.Unlock()
		if err != nil {
			bad(w, 500, "Cannot save permissions")
			return
		}
		jsonReply(w, 200, map[string]bool{"enabled": q.Enabled})
		return
	case "/local/device/revoke":
		if r.Method != "POST" {
			bad(w, 405, "Method not allowed")
			return
		}
		var q struct {
			ID string `json:"id"`
		}
		if readJSON(r, &q) != nil {
			bad(w, 400, "Bad JSON")
			return
		}
		a.s.Lock()
		devices := a.s.Data.Devices[:0]
		for _, d := range a.s.Data.Devices {
			if d.ID != q.ID {
				devices = append(devices, d)
			} else {
				a.s.eventLocked("Revoked device: " + d.Name)
			}
		}
		a.s.Data.Devices = devices
		a.signalOutboxLocked()
		err := a.s.saveLocked()
		a.s.Unlock()
		if err != nil {
			bad(w, 500, "Failed to save")
			return
		}
		jsonReply(w, 200, map[string]string{"status": "ok"})
		return
	case "/local/power/decision":
		if r.Method != "POST" {
			bad(w, 405, "Method not allowed")
			return
		}
		var q struct {
			ID      string `json:"id"`
			Approve bool   `json:"approve"`
		}
		if readJSON(r, &q) != nil {
			bad(w, 400, "Bad JSON")
			return
		}
		a.powerMu.Lock()
		request, ok := a.powers[q.ID]
		if ok {
			delete(a.powers, q.ID)
		}
		a.powerMu.Unlock()
		if !ok || time.Since(request.Created) > 2*time.Minute {
			bad(w, 404, "Command expired")
			return
		}
		if q.Approve {
			go func() {
				if err := systemPower(request.Action); err != nil {
					log.Println("power action:", err)
				}
			}()
		}
		jsonReply(w, 200, map[string]string{"status": "ok"})
		return
	case "/local/commands":
		if r.Method != "GET" {
			bad(w, 405, "Method not allowed")
			return
		}
		a.powerMu.Lock()
		out := map[string]string{}
		for id, request := range a.powers {
			if time.Since(request.Created) > 2*time.Minute {
				delete(a.powers, id)
				continue
			}
			out[id] = request.Action
		}
		a.powerMu.Unlock()
		jsonReply(w, 200, out)
		return
	case "/local/open-folder":
		if r.Method != "POST" {
			bad(w, 405, "Method not allowed")
			return
		}
		var q struct {
			Bucket string `json:"bucket"`
		}
		if readJSON(r, &q) != nil {
			bad(w, 400, "Bad JSON")
			return
		}
		if q.Bucket != "Permanent" && q.Bucket != "Temporary" {
			bad(w, 400, "Invalid folder")
			return
		}
		if err := openFolder(filepath.Join(a.s.root, q.Bucket)); err != nil {
			bad(w, 500, err.Error())
			return
		}
		jsonReply(w, 200, map[string]string{"status": "ok"})
		return
	}
	a.api(w, r, "Windows", true)
}

// signalOutboxLocked broadcasts that new messages are available. Must hold a.s.Lock.
func (a *App) signalOutboxLocked() {
	if a.outboxWake == nil {
		a.outboxWake = make(chan struct{})
	}
	close(a.outboxWake)
	a.outboxWake = make(chan struct{})
}

// pendingOutboxLocked returns only messages for the authenticated device. Must hold a.s.Lock.
func itemScope(it Item) string {
	if it.Kind == "text" {
		return "clipboard"
	}
	switch strings.ToLower(filepath.Ext(it.Name)) {
	case ".png", ".jpg", ".jpeg", ".webp":
		return "camera"
	}
	return "files"
}

// Must hold the state lock. Recheck permissions at delivery time, not just enqueue.
func (a *App) itemAllowedLocked(id string, it Item) bool {
	if id == "Windows" {
		return true
	}
	for _, d := range a.s.Data.Devices {
		if d.ID != id {
			continue
		}
		if allowed, exists := d.Permissions["files"]; exists && !allowed {
			return false
		}
		allowed, exists := d.Permissions[itemScope(it)]
		return !exists || allowed
	}
	return false
}

func (a *App) pendingOutboxLocked(id string) []map[string]any {
	return a.pendingOutboxLaneLocked(id, "")
}

func (a *App) pendingOutboxLaneLocked(id, lane string) []map[string]any {
	result := []map[string]any{}
	var items map[string]Item
	for _, job := range a.s.Data.Outbox {
		if job.To != id || job.Received {
			continue
		}
		// Idle long polls do not allocate an index for the full history.
		if items == nil {
			items = make(map[string]Item, len(a.s.Data.Items))
			for _, it := range a.s.Data.Items {
				items[it.ID] = it
			}
		}
		it, exists := items[job.ItemID]
		if !exists || !a.itemAllowedLocked(id, it) {
			continue
		}
		if lane == "text" && it.Kind != "text" {
			continue
		}
		if lane == "images" && (it.Kind == "text" || itemScope(it) != "camera") {
			continue
		}
		if lane == "files" && it.Kind == "text" {
			continue
		}
		result = append(result, map[string]any{"id": job.ID, "item": it})
	}
	return result
}

// A newly shared clipboard item is queued for other trusted Android devices.
// Delivery is device-specific and retried when the receiver fetches its inbox.
func (a *App) enqueueToOtherDevices(it Item, source string) {
	a.s.Lock()
	defer a.s.Unlock()
	added := false
	for _, d := range a.s.Data.Devices {
		if d.ID == source {
			continue
		}
		if allowed, exists := d.Permissions["files"]; exists && !allowed {
			continue
		}
		// Clipboard content must never bypass device-specific clipboard consent.
		scope := "camera"
		if it.Kind == "text" {
			scope = "clipboard"
		}
		if allowed, exists := d.Permissions[scope]; exists && !allowed {
			continue
		}
		a.s.Data.Outbox = append(a.s.Data.Outbox, Outbox{
			ID: randomHex(12), To: d.ID, ItemID: it.ID, At: time.Now().UTC(),
		})
		added = true
	}
	if added {
		if err := a.s.saveLocked(); err != nil {
			log.Printf("failed to persist outgoing share queue: %v", err)
		} else {
			a.signalOutboxLocked()
		}
	}
}

func (a *App) api(w http.ResponseWriter, r *http.Request, from string, local bool) {
	path := r.URL.Path
	if local {
		path = strings.TrimPrefix(path, "/local")
	}
	if path == "/v1/update/manifest" || path == "/v1/update/apk" {
		a.getUpdate(w, r, local)
		return
	}
	if r.Method == "GET" && path == "/v1/items" {
		a.s.cleanup()
		a.s.Lock()
		result := []Item{}
		for _, it := range a.s.Data.Items {
			if local || a.itemAllowedLocked(from, it) {
				result = append(result, it)
			}
		}
		a.s.Unlock()
		jsonReply(w, 200, result)
		return
	}
	if r.Method == "GET" && strings.HasPrefix(path, "/v1/items/") && strings.HasSuffix(path, "/content") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/items/"), "/content")
		a.s.Lock()
		var found Item
		ok := false
		for _, v := range a.s.Data.Items {
			if v.ID == id {
				found = v
				ok = local || a.itemAllowedLocked(from, v)
				break
			}
		}
		a.s.Unlock()
		if !ok {
			bad(w, 404, "Item missing")
			return
		}
		full := filepath.Join(a.s.root, found.RelPath)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": found.Name}))
		http.ServeFile(w, r, full)
		return
	}
	if r.Method == "POST" && path == "/v1/items/text" {
		var q struct {
			Text      string `json:"text"`
			Bucket    string `json:"bucket"`
			Clipboard bool   `json:"clipboard"`
		}
		if readJSON(r, &q) != nil || len(q.Text) > 2<<20 || q.Text == "" {
			bad(w, 400, "Text must be 1 byte–2 MB")
			return
		}
		if q.Bucket == "" {
			q.Bucket = "Temporary"
		}
		if q.Bucket != "Permanent" && q.Bucket != "Temporary" {
			bad(w, 400, "Unknown bucket")
			return
		}
		it, err := a.saveItem(strings.NewReader(q.Text), "clipboard.txt", "text", q.Bucket, from, int64(len(q.Text)))
		if err != nil {
			bad(w, 500, err.Error())
			return
		}
		applied := false
		if q.Clipboard {
			a.enqueueToOtherDevices(it, from)
			a.clipMu.Lock()
			clipboardErr := clipboard.SetText(q.Text)
			applied = clipboardErr == nil
			if err := clipboardErr; err != nil {
				log.Printf("clipboard text error: %v", err)
			}
			a.clipSeq = clipboard.Sequence()
			a.clipReady = true
			a.clipFailures = 0
			a.clipMu.Unlock()
		}
		if q.Clipboard {
			jsonReply(w, 201, struct {
				Item
				ClipboardApplied bool `json:"clipboardApplied"`
			}{it, applied})
		} else {
			jsonReply(w, 201, it)
		}
		return
	}
	if r.Method == "POST" && path == "/v1/items/file" {
		name := safeName(r.URL.Query().Get("name"))
		bucket := r.URL.Query().Get("bucket")
		if bucket == "" {
			bucket = "Temporary"
		}
		if bucket != "Temporary" && bucket != "Permanent" {
			bad(w, 400, "Unknown bucket")
			return
		}
		if r.ContentLength > maxUpload {
			bad(w, 413, "File exceeds 512 MB limit")
			return
		}
		it, err := a.saveItem(io.LimitReader(r.Body, maxUpload+1), name, "file", bucket, from, maxUpload)
		if err != nil {
			if strings.Contains(err.Error(), "too large") {
				bad(w, 413, err.Error())
			} else {
				bad(w, 500, err.Error())
			}
			return
		}
		applied := false
		if r.URL.Query().Get("clipboard") == "1" {
			a.enqueueToOtherDevices(it, from)
			a.clipMu.Lock()
			clipboardErr := clipboard.SetImage(filepath.Join(a.s.root, it.RelPath))
			applied = clipboardErr == nil
			if err := clipboardErr; err != nil {
				log.Printf("image clipboard unavailable: %v", err)
			}
			a.clipSeq = clipboard.Sequence()
			a.clipReady = true
			a.clipFailures = 0
			a.clipMu.Unlock()
		}
		if r.URL.Query().Get("clipboard") == "1" {
			jsonReply(w, 201, struct {
				Item
				ClipboardApplied bool `json:"clipboardApplied"`
			}{it, applied})
		} else {
			jsonReply(w, 201, it)
		}
		return
	}
	if r.Method == "POST" && strings.HasPrefix(path, "/v1/items/") && strings.HasSuffix(path, "/pin") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/items/"), "/pin")
		a.s.Lock()
		var out Item
		found := false
		for i := range a.s.Data.Items {
			it := &a.s.Data.Items[i]
			if it.ID == id {
				found = true
				if it.Bucket == "Temporary" {
					old := filepath.Join(a.s.root, it.RelPath)
					rel := filepath.Join("Permanent", filepath.Base(it.RelPath))
					if err := os.Rename(old, filepath.Join(a.s.root, rel)); err != nil {
						a.s.Unlock()
						bad(w, 500, err.Error())
						return
					}
					it.Bucket = "Permanent"
					it.RelPath = rel
				}
				out = *it
				break
			}
		}
		if found {
			_ = a.s.saveLocked()
		}
		a.s.Unlock()
		if !found {
			bad(w, 404, "No such item")
			return
		}
		jsonReply(w, 200, out)
		return
	}
	if r.Method == "DELETE" && strings.HasPrefix(path, "/v1/items/") {
		id := strings.TrimPrefix(path, "/v1/items/")
		a.s.Lock()
		items := a.s.Data.Items[:0]
		found := false
		for _, it := range a.s.Data.Items {
			if it.ID == id {
				found = true
				_ = os.Remove(filepath.Join(a.s.root, it.RelPath))
			} else {
				items = append(items, it)
			}
		}
		a.s.Data.Items = items
		_ = a.s.saveLocked()
		a.s.Unlock()
		if !found {
			bad(w, 404, "No such item")
			return
		}
		jsonReply(w, 200, map[string]bool{"deleted": true})
		return
	}
	if path == "/v1/clipboard/pull" && r.Method == "GET" {
		txt, err := clipboard.GetText()
		if err != nil {
			bad(w, 404, "No readable text in Windows clipboard")
			return
		}
		if len(txt) > 2<<20 {
			bad(w, 413, "Clipboard too large")
			return
		}
		jsonReply(w, 200, map[string]string{"text": txt})
		return
	}
	if path == "/v1/tasks" {
		a.recordTask(w, r)
		return
	}
	if path == "/v1/notes" {
		a.recordNote(w, r)
		return
	}
	if path == "/v1/outbox" && r.Method == "GET" {
		// A single long poll delivers changes promptly while avoiding frequent
		// Android radio wakeups. 20-25s server timeout is beneath client read timeout.
		wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
		if wait < 0 {
			wait = 0
		}
		if wait > 55 {
			wait = 55
		}
		a.s.Lock()
		lane := r.URL.Query().Get("lane")
		if lane != "" && lane != "text" && lane != "files" && lane != "images" {
			a.s.Unlock()
			bad(w, 400, "Unknown inbox lane")
			return
		}
		pending := a.pendingOutboxLaneLocked(from, lane)
		if len(pending) > 0 || wait == 0 {
			a.s.Unlock()
			jsonReply(w, 200, pending)
			return
		}
		wake := a.outboxWake
		a.s.Unlock()
		timer := time.NewTimer(time.Duration(wait) * time.Second)
		defer timer.Stop()
		select {
		case <-wake:
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		a.s.Lock()
		// Authorization may have changed while this request was asleep.
		if !local {
			valid := false
			for _, d := range a.s.Data.Devices {
				if d.ID == from && r.Header.Get("Authorization") == "Bearer "+d.Token {
					allowed, exists := d.Permissions["files"]
					valid = !exists || allowed
					break
				}
			}
			if !valid {
				a.s.Unlock()
				bad(w, 403, "Device access changed")
				return
			}
		}
		pending = a.pendingOutboxLaneLocked(from, lane)
		a.s.Unlock()
		jsonReply(w, 200, pending)
		return
	}
	if path == "/v1/outbox" && r.Method == "POST" && local {
		var q struct {
			To     string `json:"to"`
			ItemID string `json:"item_id"`
		}
		if readJSON(r, &q) != nil {
			bad(w, 400, "Invalid JSON")
			return
		}
		a.s.Lock()
		deviceOK, itemOK := false, false
		for _, d := range a.s.Data.Devices {
			if d.ID == q.To {
				deviceOK = true
			}
		}
		for _, it := range a.s.Data.Items {
			if it.ID == q.ItemID {
				itemOK = true
			}
		}
		if !deviceOK || !itemOK {
			a.s.Unlock()
			bad(w, 400, "Unknown device or file")
			return
		}
		o := Outbox{ID: randomHex(12), To: q.To, ItemID: q.ItemID, At: time.Now().UTC()}
		a.s.Data.Outbox = append(a.s.Data.Outbox, o)
		err := a.s.saveLocked()
		if err == nil {
			a.signalOutboxLocked()
		}
		a.s.Unlock()
		if err != nil {
			bad(w, 500, "Cannot queue item")
			return
		}
		jsonReply(w, 201, o)
		return
	}
	if strings.HasPrefix(path, "/v1/outbox/") && strings.HasSuffix(path, "/ack") && r.Method == "POST" {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/outbox/"), "/ack")
		a.s.Lock()
		index := -1
		previous := false
		for i := range a.s.Data.Outbox {
			if a.s.Data.Outbox[i].ID == id && a.s.Data.Outbox[i].To == from {
				index = i
				previous = a.s.Data.Outbox[i].Received
				a.s.Data.Outbox[i].Received = true
				break
			}
		}
		if index < 0 {
			a.s.Unlock()
			bad(w, 404, "Queue item missing")
			return
		}
		err := a.s.saveLocked()
		if err != nil {
			a.s.Data.Outbox[index].Received = previous
		}
		a.s.Unlock()
		if err != nil {
			bad(w, 500, "Cannot persist acknowledgement")
			return
		}
		jsonReply(w, 200, map[string]bool{"received": true})
		return
	}
	if path == "/v1/actions/volume" && r.Method == "POST" {
		var q struct {
			Percent int `json:"percent"`
		}
		if readJSON(r, &q) != nil || q.Percent < 0 || q.Percent > 100 {
			bad(w, 400, "Percent must be 0–100")
			return
		}
		if err := setSystemVolume(q.Percent); err != nil {
			bad(w, 501, err.Error())
			return
		}
		jsonReply(w, 200, map[string]int{"percent": q.Percent})
		return
	}
	if path == "/v1/actions/power" && r.Method == "POST" {
		var q struct {
			Action string `json:"action"`
		}
		if readJSON(r, &q) != nil {
			bad(w, 400, "Bad JSON")
			return
		}
		if q.Action != "sleep" && q.Action != "shutdown" && q.Action != "restart" && q.Action != "lock" {
			bad(w, 400, "Unsupported action")
			return
		}
		if !local {
			a.s.Lock()
			enabled := a.s.Data.RemotePower
			a.s.Unlock()
			if !enabled {
				bad(w, 403, "Remote power operations disabled")
				return
			}
		}
		// Always require explicit confirmation on the Windows dashboard, even for local commands.
		id := randomHex(8)
		a.powerMu.Lock()
		a.powers[id] = powerRequest{Action: q.Action, Created: time.Now()}
		a.powerMu.Unlock()
		jsonReply(w, 202, map[string]string{"id": id, "status": "pending local approval"})
		return
	}
	if path == "/v1/settings/power" && r.Method == "POST" && local {
		var q struct {
			Enabled bool `json:"enabled"`
		}
		if readJSON(r, &q) != nil {
			bad(w, 400, "Bad JSON")
			return
		}
		a.s.Lock()
		a.s.Data.RemotePower = q.Enabled
		_ = a.s.saveLocked()
		a.s.Unlock()
		jsonReply(w, 200, map[string]bool{"enabled": q.Enabled})
		return
	}
	bad(w, 404, "Not found")
}
func (a *App) saveItem(in io.Reader, name, kind, bucket, from string, limit int64) (Item, error) {
	id := randomHex(12)
	name = safeName(name)
	rel := filepath.Join(bucket, id+"_"+name)
	dest := filepath.Join(a.s.root, rel)
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Item{}, err
	}
	written, err := io.Copy(file, io.LimitReader(in, limit+1))
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil || written > limit {
		_ = os.Remove(dest)
		if written > limit {
			return Item{}, errors.New("file too large")
		}
		return Item{}, err
	}
	it := Item{ID: id, Name: name, Kind: kind, Bucket: bucket, Source: from, Size: written, Created: time.Now().UTC(), RelPath: rel}
	a.s.Lock()
	a.s.Data.Items = append([]Item{it}, a.s.Data.Items...)
	a.s.eventLocked("Received " + name + " from " + from)
	err = a.s.saveLocked()
	a.s.Unlock()
	if err != nil {
		_ = os.Remove(dest)
		return Item{}, err
	}
	return it, nil
}
func (a *App) recordTask(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.s.Lock()
		v := append([]Task{}, a.s.Data.Tasks...)
		a.s.Unlock()
		jsonReply(w, 200, v)
		return
	}
	if r.Method != "POST" {
		bad(w, 405, "Unsupported method")
		return
	}
	var q struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Done   bool   `json:"done"`
		Delete bool   `json:"delete"`
	}
	if readJSON(r, &q) != nil || len(q.Text) > 1000 {
		bad(w, 400, "Bad task")
		return
	}
	a.s.Lock()
	defer a.s.Unlock()
	if q.ID != "" {
		for i, t := range a.s.Data.Tasks {
			if t.ID == q.ID {
				if q.Delete {
					a.s.Data.Tasks = append(a.s.Data.Tasks[:i], a.s.Data.Tasks[i+1:]...)
					_ = a.s.saveLocked()
					jsonReply(w, 200, map[string]bool{"deleted": true})
					return
				}
				a.s.Data.Tasks[i].Text = q.Text
				a.s.Data.Tasks[i].Done = q.Done
				a.s.Data.Tasks[i].Updated = time.Now().UTC()
				_ = a.s.saveLocked()
				jsonReply(w, 200, a.s.Data.Tasks[i])
				return
			}
		}
		bad(w, 404, "Unknown task")
		return
	}
	if strings.TrimSpace(q.Text) == "" {
		bad(w, 400, "Task text required")
		return
	}
	t := Task{ID: randomHex(10), Text: q.Text, Done: q.Done, Updated: time.Now().UTC()}
	a.s.Data.Tasks = append([]Task{t}, a.s.Data.Tasks...)
	_ = a.s.saveLocked()
	jsonReply(w, 201, t)
}
func (a *App) recordNote(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.s.Lock()
		v := append([]Note{}, a.s.Data.Notes...)
		a.s.Unlock()
		jsonReply(w, 200, v)
		return
	}
	if r.Method != "POST" {
		bad(w, 405, "Unsupported method")
		return
	}
	var q struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Delete bool   `json:"delete"`
	}
	if readJSON(r, &q) != nil || len(q.Title) > 200 || len(q.Body) > 200000 {
		bad(w, 400, "Note too large")
		return
	}
	a.s.Lock()
	defer a.s.Unlock()
	if q.ID != "" {
		for i, n := range a.s.Data.Notes {
			if n.ID == q.ID {
				if q.Delete {
					a.s.Data.Notes = append(a.s.Data.Notes[:i], a.s.Data.Notes[i+1:]...)
					_ = a.s.saveLocked()
					jsonReply(w, 200, map[string]bool{"deleted": true})
					return
				}
				a.s.Data.Notes[i].Title = q.Title
				a.s.Data.Notes[i].Body = q.Body
				a.s.Data.Notes[i].Updated = time.Now().UTC()
				_ = a.s.saveLocked()
				jsonReply(w, 200, a.s.Data.Notes[i])
				return
			}
		}
		bad(w, 404, "Unknown note")
		return
	}
	if strings.TrimSpace(q.Title) == "" {
		bad(w, 400, "Title required")
		return
	}
	n := Note{ID: randomHex(10), Title: q.Title, Body: q.Body, Updated: time.Now().UTC()}
	a.s.Data.Notes = append([]Note{n}, a.s.Data.Notes...)
	_ = a.s.saveLocked()
	jsonReply(w, 201, n)
}
func lanAddresses() []string {
	out := []string{}
	ifs, e := net.Interfaces()
	if e != nil {
		return out
	}
	for _, it := range ifs {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := it.Addrs()
		for _, v := range addrs {
			ip, _, e := net.ParseCIDR(v.String())
			if e == nil && ip.To4() != nil && !ip.IsLoopback() {
				out = append(out, ip.String())
			}
		}
	}
	return out
}
func systemPower(action string) error {
	switch action {
	case "sleep":
		return exec.Command("rundll32.exe", "powrprof.dll,SetSuspendState", "0,1,0").Run()
	case "restart":
		return exec.Command("shutdown.exe", "/r", "/t", "10").Run()
	case "shutdown":
		return exec.Command("shutdown.exe", "/s", "/t", "10").Run()
	case "lock":
		return exec.Command("rundll32.exe", "user32.dll,LockWorkStation").Run()
	default:
		return fmt.Errorf("unsupported action: %s", action)
	}
}
func openFolder(path string) error {
	if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") || filepath.Separator == '\\' {
		return exec.Command("explorer.exe", path).Start()
	}
	return exec.Command("xdg-open", path).Start()
}
