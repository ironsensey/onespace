package coordination

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWireGuardEnrollment(t *testing.T) {
	e := NewWireGuardEnrollment()

	// Enroll a peer
	peer, err := e.Enroll("device-1", "pubkey1", "10.200.200.0/24")
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if peer.DeviceID != "device-1" {
		t.Errorf("expected device-1, got %s", peer.DeviceID)
	}
	if peer.PublicKey != "pubkey1" {
		t.Errorf("expected pubkey1, got %s", peer.PublicKey)
	}

	// Duplicate enrollment should fail
	_, err = e.Enroll("device-1", "pubkey2", "10.200.200.0/24")
	if err == nil {
		t.Error("expected error for duplicate device")
	}

	// Duplicate key should fail
	_, err = e.Enroll("device-2", "pubkey1", "10.200.200.0/24")
	if err == nil {
		t.Error("expected error for duplicate key")
	}

	// Get peer
	got, ok := e.GetPeer("device-1")
	if !ok {
		t.Fatal("GetPeer failed")
	}
	if got.PublicKey != "pubkey1" {
		t.Errorf("expected pubkey1, got %s", got.PublicKey)
	}

	// Get peer by key
	got2, ok := e.GetPeerByKey("pubkey1")
	if !ok {
		t.Fatal("GetPeerByKey failed")
	}
	if got2.DeviceID != "device-1" {
		t.Errorf("expected device-1, got %s", got2.DeviceID)
	}

	// Revoke
	err = e.Revoke("device-1")
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Verify revoked
	got3, _ := e.GetPeer("device-1")
	if !got3.Revoked {
		t.Error("peer should be revoked")
	}

	// Revoke non-existent
	err = e.Revoke("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent device")
	}
}

func TestWireGuardEnrollmentHTTP(t *testing.T) {
	e := NewWireGuardEnrollment()

	// Test enroll endpoint
	body := bytes.NewBufferString(`{"device_id":"dev1","public_key":"pk1","subnet":"10.200.200.0/24"}`)
	req := httptest.NewRequest("POST", "/enroll", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.EnrollHandler(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Test duplicate enrollment
	body2 := bytes.NewBufferString(`{"device_id":"dev1","public_key":"pk2","subnet":"10.200.200.0/24"}`)
	req2 := httptest.NewRequest("POST", "/enroll", body2)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	e.EnrollHandler(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w2.Code)
	}

	// Test revoke endpoint
	body3 := bytes.NewBufferString(`{"device_id":"dev1"}`)
	req3 := httptest.NewRequest("POST", "/revoke", body3)
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	e.RevokeHandler(w3, req3)

	if w3.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w3.Code, w3.Body.String())
	}

	// Test wrong method
	req4 := httptest.NewRequest("GET", "/enroll", nil)
	w4 := httptest.NewRecorder()
	e.EnrollHandler(w4, req4)

	if w4.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w4.Code)
	}

	// Test invalid JSON
	body5 := bytes.NewBufferString(`{invalid}`)
	req5 := httptest.NewRequest("POST", "/enroll", body5)
	req5.Header.Set("Content-Type", "application/json")
	w5 := httptest.NewRecorder()
	e.EnrollHandler(w5, req5)

	if w5.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w5.Code)
	}
}

func TestHashKey(t *testing.T) {
	h := HashKey("testkey")
	if len(h) != 16 {
		t.Errorf("expected 16 char hash, got %d", len(h))
	}
}
