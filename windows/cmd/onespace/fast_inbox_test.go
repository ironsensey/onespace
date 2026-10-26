package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLongPollWakesOnlyWithAuthenticatedItems(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := randomHex(32)
	s.Lock()
	s.Data.Devices = []Device{{ID: "phone", Token: token}}
	s.Data.Items = []Item{{ID: "newitem", Kind: "text", Name: "clipboard.txt", Bucket: "Temporary"}}
	s.Unlock()
	app := newApp(s, "fingerprint")
	remote := httptest.NewTLSServer(http.HandlerFunc(app.remote))
	defer remote.Close()
	client := remote.Client()

	req, _ := http.NewRequest("GET", remote.URL+"/v1/outbox?wait=4", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	output := make(chan struct {
		status  int
		length  int
		err     error
		elapsed time.Duration
	}, 1)
	start := time.Now()
	go func() {
		resp, e := client.Do(req)
		if e != nil {
			output <- struct {
				status  int
				length  int
				err     error
				elapsed time.Duration
			}{err: e}
			return
		}
		defer resp.Body.Close()
		var arr []any
		e = json.NewDecoder(resp.Body).Decode(&arr)
		output <- struct {
			status  int
			length  int
			err     error
			elapsed time.Duration
		}{resp.StatusCode, len(arr), e, time.Since(start)}
	}()
	time.Sleep(120 * time.Millisecond)
	app.enqueueToOtherDevices(Item{ID: "newitem", Kind: "text"}, "Windows")
	select {
	case result := <-output:
		if result.err != nil || result.status != 200 || result.length != 1 {
			t.Fatalf("longpoll receive: %+v", result)
		}
		if result.elapsed > 2*time.Second {
			t.Fatalf("inbox was not woken promptly: %s", result.elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("longpoll didn't wake after enqueue")
	}
	badReq, _ := http.NewRequest("GET", remote.URL+"/v1/outbox?wait=5", nil)
	resp, err := client.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired caller could read: %d", resp.StatusCode)
	}
}

func TestLongPollTimeoutReturnsEmptyWithoutBusyPoll(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := randomHex(32)
	s.Lock()
	s.Data.Devices = []Device{{ID: "phone", Token: token}}
	s.Unlock()
	a := newApp(s, "fp")
	remote := httptest.NewTLSServer(http.HandlerFunc(a.remote))
	defer remote.Close()
	req, _ := http.NewRequest("GET", remote.URL+"/v1/outbox?wait=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	started := time.Now()
	resp, e := remote.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	var arr []any
	if e = json.NewDecoder(resp.Body).Decode(&arr); e != nil {
		t.Fatal(e)
	}
	if len(arr) != 0 || resp.StatusCode != 200 || time.Since(started) < 850*time.Millisecond {
		t.Fatalf("longpoll did not wait: status %d length %d elapsed %s", resp.StatusCode, len(arr), time.Since(started))
	}
}
