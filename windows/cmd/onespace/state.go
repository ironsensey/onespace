package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Device struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Token       string          `json:"token"`
	Permissions map[string]bool `json:"permissions,omitempty"`
	PairedAt    time.Time       `json:"paired_at"`
}
type Item struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Bucket  string    `json:"bucket"`
	Source  string    `json:"source"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
	RelPath string    `json:"rel_path"`
}
type Task struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	Done    bool      `json:"done"`
	Updated time.Time `json:"updated"`
}
type Note struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	Updated time.Time `json:"updated"`
}
type Event struct {
	At   time.Time `json:"at"`
	What string    `json:"what"`
}
type Pending struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Ticket   string    `json:"-"`
	At       time.Time `json:"at"`
	Approved bool      `json:"-"`
	Rejected bool      `json:"-"`
	Token    string    `json:"token"`
}
type Outbox struct {
	ID       string    `json:"id"`
	To       string    `json:"to"`
	ItemID   string    `json:"item_id"`
	At       time.Time `json:"at"`
	Received bool      `json:"received"`
}
type persistent struct {
	Salt          string   `json:"salt"`
	PassHash      string   `json:"pass_hash"`
	Devices       []Device `json:"devices"`
	Items         []Item   `json:"items"`
	Tasks         []Task   `json:"tasks"`
	Notes         []Note   `json:"notes"`
	Events        []Event  `json:"events"`
	Outbox        []Outbox `json:"outbox"`
	RemotePower   bool     `json:"remote_power"`
	AutoClipboard bool     `json:"auto_clipboard"`
}
type State struct {
	sync.Mutex
	root      string
	Data      persistent
	Pending   map[string]*Pending
	clip      string
	deskToken string
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// PBKDF2-HMAC-SHA256: password verification only, with independent per-install salt.
func passwordKey(password string, salt []byte) []byte { // one block, 200000 iterations
	h := func(k, data []byte) []byte { // inline HMAC for portability without external deps
		// HMAC(SHA256), block-size 64
		kb := make([]byte, 64)
		if len(k) > 64 {
			t := sha256.Sum256(k)
			copy(kb, t[:])
		} else {
			copy(kb, k)
		}
		ipad := make([]byte, 64)
		opad := make([]byte, 64)
		for i := 0; i < 64; i++ {
			ipad[i] = kb[i] ^ 0x36
			opad[i] = kb[i] ^ 0x5c
		}
		inner := sha256.New()
		inner.Write(ipad)
		inner.Write(data)
		v := inner.Sum(nil)
		outer := sha256.New()
		outer.Write(opad)
		outer.Write(v)
		return outer.Sum(nil)
	}
	msg := make([]byte, len(salt)+4)
	copy(msg, salt)
	msg[len(msg)-1] = 1
	u := h([]byte(password), msg)
	res := append([]byte(nil), u...)
	for i := 1; i < 200000; i++ {
		u = h([]byte(password), u)
		for j := range res {
			res[j] ^= u[j]
		}
	}
	return res
}
func NewState(root string) (*State, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	for _, name := range []string{"Permanent", "Temporary", "Outbox"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			return nil, err
		}
	}
	s := &State{root: root, Pending: map[string]*Pending{}, deskToken: randomHex(24)}
	bytes, err := os.ReadFile(filepath.Join(root, "state.json"))
	if err == nil {
		if err = json.Unmarshal(bytes, &s.Data); err != nil {
			return nil, fmt.Errorf("state invalid: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}
func (s *State) saveLocked() error {
	b, err := json.Marshal(s.Data)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.root, "state.json.new")
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.root, "state.json"))
}
func (s *State) SetPassword(p string) error {
	if len([]rune(p)) < 12 || len([]rune(p)) > 256 {
		return errors.New("Password must contain 12–256 characters")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	hash := passwordKey(p, salt)
	s.Lock()
	defer s.Unlock()
	s.Data.Salt = hex.EncodeToString(salt)
	s.Data.PassHash = hex.EncodeToString(hash)
	return s.saveLocked()
}
func (s *State) hasPassword() bool { s.Lock(); defer s.Unlock(); return s.Data.PassHash != "" }
func (s *State) checkPassword(p string) bool {
	s.Lock()
	saltStr, hashStr := s.Data.Salt, s.Data.PassHash
	s.Unlock()
	if hashStr == "" {
		return false
	}
	salt, e1 := hex.DecodeString(saltStr)
	hash, e2 := hex.DecodeString(hashStr)
	if e1 != nil || e2 != nil {
		return false
	}
	got := passwordKey(p, salt)
	return subtle.ConstantTimeCompare(hash, got) == 1
}
func (s *State) deviceByToken(token string) (Device, bool) {
	s.Lock()
	defer s.Unlock()
	for _, d := range s.Data.Devices {
		if len(token) == len(d.Token) && token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(d.Token)) == 1 {
			return d, true
		}
	}
	return Device{}, false
}
func (s *State) eventLocked(v string) {
	s.Data.Events = append([]Event{{At: time.Now().UTC(), What: v}}, s.Data.Events...)
	if len(s.Data.Events) > 100 {
		s.Data.Events = s.Data.Events[:100]
	}
}
func (s *State) cleanupLocked(now time.Time) error {
	kept := s.Data.Items[:0]
	changed := false
	for _, it := range s.Data.Items {
		if it.Bucket == "Temporary" && now.Sub(it.Created) >= 24*time.Hour {
			_ = os.Remove(filepath.Join(s.root, it.RelPath))
			changed = true
			s.eventLocked("Temporary item expired: " + it.Name)
		} else {
			kept = append(kept, it)
		}
	}
	s.Data.Items = kept
	// Acknowledged deliveries are no longer needed for recovery. Cap the
	// persistent outbox over time so frequent clipboard use stays lightweight.
	keptJobs := s.Data.Outbox[:0]
	for _, job := range s.Data.Outbox {
		if job.Received && now.Sub(job.At) >= time.Hour {
			changed = true
			continue
		}
		keptJobs = append(keptJobs, job)
	}
	s.Data.Outbox = keptJobs
	for k, p := range s.Pending {
		if now.Sub(p.At) > 5*time.Minute {
			delete(s.Pending, k)
		}
	}
	if changed {
		return s.saveLocked()
	}
	return nil
}
func (s *State) cleanup() { s.Lock(); defer s.Unlock(); _ = s.cleanupLocked(time.Now().UTC()) }
func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(strings.ReplaceAll(name, "\\", "/"), "\x00", ""))
	name = strings.TrimSpace(name)
	if name == "." || name == ".." || name == "" {
		return "file"
	}
	runes := []rune(name)
	if len(runes) > 120 {
		name = string(runes[:120])
	}
	return name
}
