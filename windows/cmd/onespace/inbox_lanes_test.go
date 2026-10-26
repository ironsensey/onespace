package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTextLaneBypassesFilesWithoutDroppingThem(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Data.Devices = []Device{{ID: "phone", Token: randomHex(32)}}
	s.Data.Items = []Item{{ID: "photo", Kind: "file", Name: "photo.jpg"}, {ID: "text", Kind: "text"}, {ID: "pdf", Kind: "file", Name: "report.pdf"}}
	s.Data.Outbox = []Outbox{{ID: "photo-job", To: "phone", ItemID: "photo"}, {ID: "text-job", To: "phone", ItemID: "text"}, {ID: "pdf-job", To: "phone", ItemID: "pdf"}}
	a := newApp(s, "fp")
	s.Lock()
	defer s.Unlock()
	text := a.pendingOutboxLaneLocked("phone", "text")
	files := a.pendingOutboxLaneLocked("phone", "files")
	images := a.pendingOutboxLaneLocked("phone", "images")
	all := a.pendingOutboxLocked("phone")
	if len(text) != 1 || text[0]["id"] != "text-job" {
		t.Fatalf("text blocked by file: %+v", text)
	}
	if len(images) != 1 || images[0]["id"] != "photo-job" {
		t.Fatal("image-only reception lost")
	}
	if len(files) != 2 || files[0]["id"] != "photo-job" || len(all) != 3 {
		t.Fatal("file queue or legacy inbox lost")
	}
}

func TestRevocationWakesAndInvalidatesExistingLongPoll(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := randomHex(32)
	s.Data.Devices = []Device{{ID: "phone", Token: token}}
	a := newApp(s, "fp")
	remote := httptest.NewTLSServer(http.HandlerFunc(a.remote))
	defer remote.Close()
	local := httptest.NewServer(http.HandlerFunc(a.local))
	defer local.Close()
	result := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("GET", remote.URL+"/v1/outbox?wait=5", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := remote.Client().Do(req)
		if err != nil {
			result <- 0
			return
		}
		defer response.Body.Close()
		result <- response.StatusCode
	}()
	time.Sleep(120 * time.Millisecond)
	req, _ := http.NewRequest("POST", local.URL+"/local/device/revoke", bytes.NewBufferString(`{"id":"phone"}`))
	req.Header.Set("X-Local-Key", s.deskToken)
	response, err := local.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("revoke failed")
	}
	select {
	case code := <-result:
		if code != 403 && code != 401 {
			t.Fatalf("revoked request returned %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("revoked poll was not cancelled promptly")
	}
}

func TestDeniedClipboardCannotBeDownloadedThroughFileEndpoint(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := randomHex(32)
	s.Data.Devices = []Device{{ID: "phone", Token: token, Permissions: map[string]bool{"clipboard": false}}}
	a := newApp(s, "fp")
	item, err := a.saveItem(bytes.NewBufferString("private test text"), "clipboard.txt", "text", "Temporary", "Windows", 100)
	if err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewTLSServer(http.HandlerFunc(a.remote))
	defer remote.Close()
	req, _ := http.NewRequest("GET", remote.URL+"/v1/items/"+item.ID+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := remote.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode == 200 {
		t.Fatal("clipboard permission bypass via content endpoint")
	}
	var body map[string]string
	if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
}
