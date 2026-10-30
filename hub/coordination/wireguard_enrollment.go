package coordination

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// WireGuardPeer holds enrollment data for a WireGuard peer.
type WireGuardPeer struct {
	DeviceID   string `json:"device_id"`
	PublicKey  string `json:"public_key"`
	AssignedIP string `json:"assigned_ip"`
	AllowedIPs []string `json:"allowed_ips"`
	Endpoint   string `json:"endpoint,omitempty"`
	Revoked    bool   `json:"revoked"`
}

// WireGuardEnrollment manages WireGuard peer enrollment and revocation.
type WireGuardEnrollment struct {
	mu    sync.RWMutex
	peers map[string]*WireGuardPeer // deviceID -> peer
	byKey map[string]string         // publicKey -> deviceID
}

// NewWireGuardEnrollment creates a new enrollment manager.
func NewWireGuardEnrollment() *WireGuardEnrollment {
	return &WireGuardEnrollment{
		peers: make(map[string]*WireGuardPeer),
		byKey: make(map[string]string),
	}
}

// Enroll registers a WireGuard public key for a device.
func (e *WireGuardEnrollment) Enroll(deviceID, publicKey string, subnet string) (*WireGuardPeer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.peers[deviceID]; exists {
		return nil, fmt.Errorf("device already enrolled: %s", deviceID)
	}
	if _, exists := e.byKey[publicKey]; exists {
		return nil, fmt.Errorf("public key already enrolled")
	}

	// Assign IP
	usedIPs := make([]string, 0, len(e.peers))
	for _, p := range e.peers {
		usedIPs = append(usedIPs, p.AssignedIP)
	}

	peer := &WireGuardPeer{
		DeviceID:   deviceID,
		PublicKey:  publicKey,
		AllowedIPs: []string{subnet},
	}

	e.peers[deviceID] = peer
	e.byKey[publicKey] = deviceID

	return peer, nil
}

// Revoke marks a peer as revoked.
func (e *WireGuardEnrollment) Revoke(deviceID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	peer, exists := e.peers[deviceID]
	if !exists {
		return fmt.Errorf("device not enrolled: %s", deviceID)
	}
	peer.Revoked = true
	return nil
}

// GetPeer returns peer info by device ID.
func (e *WireGuardEnrollment) GetPeer(deviceID string) (*WireGuardPeer, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	peer, exists := e.peers[deviceID]
	if !exists {
		return nil, false
	}
	cp := *peer
	return &cp, true
}

// GetPeerByKey returns peer info by public key.
func (e *WireGuardEnrollment) GetPeerByKey(publicKey string) (*WireGuardPeer, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	deviceID, exists := e.byKey[publicKey]
	if !exists {
		return nil, false
	}
	peer, exists := e.peers[deviceID]
	if !exists {
		return nil, false
	}
	cp := *peer
	return &cp, true
}

// ListPeers returns all enrolled peers.
func (e *WireGuardEnrollment) ListPeers() []WireGuardPeer {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]WireGuardPeer, 0, len(e.peers))
	for _, p := range e.peers {
		result = append(result, *p)
	}
	return result
}

// HashKey returns a hash of a public key for identification.
func HashKey(publicKey string) string {
	h := sha256.Sum256([]byte(publicKey))
	return hex.EncodeToString(h[:8])
}

// EnrollHandler handles HTTP enrollment requests.
func (e *WireGuardEnrollment) EnrollHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DeviceID  string `json:"device_id"`
		PublicKey string `json:"public_key"`
		Subnet    string `json:"subnet"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	if req.Subnet == "" {
		req.Subnet = "10.200.200.0/24"
	}

	peer, err := e.Enroll(req.DeviceID, req.PublicKey, req.Subnet)
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(peer)
}

// RevokeHandler handles HTTP revocation requests.
func (e *WireGuardEnrollment) RevokeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	if err := e.Revoke(req.DeviceID); err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked"})
}
// RegisterEndpoint registers or updates a peer's network endpoint.
func (e *WireGuardEnrollment) RegisterEndpoint(deviceID, endpoint string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	peer, exists := e.peers[deviceID]
	if !exists {
		return fmt.Errorf("device not enrolled: %s", deviceID)
	}
	if peer.Revoked {
		return fmt.Errorf("device revoked: %s", deviceID)
	}
	peer.Endpoint = endpoint
	return nil
}

// GetEndpoint returns a peer's endpoint by public key.
func (e *WireGuardEnrollment) GetEndpoint(publicKey string) (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	deviceID, exists := e.byKey[publicKey]
	if !exists {
		return "", false
	}
	peer, exists := e.peers[deviceID]
	if !exists {
		return "", false
	}
	if peer.Revoked {
		return "", false
	}
	return peer.Endpoint, true
}

// EndpointHandler handles HTTP endpoint registration and lookup.
func (e *WireGuardEnrollment) EndpointHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			DeviceID  string `json:"device_id"`
			Endpoint  string `json:"endpoint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
			return
		}
		if err := e.RegisterEndpoint(req.DeviceID, req.Endpoint); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	if r.Method == http.MethodGet {
		publicKey := r.URL.Query().Get("public_key")
		if publicKey == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "public_key required"})
			return
		}
		endpoint, ok := e.GetEndpoint(publicKey)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "peer not found or not revoked"})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"endpoint": endpoint})
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}
