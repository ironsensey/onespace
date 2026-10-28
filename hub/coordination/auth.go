package coordination

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
)

type TokenAuthorizer struct {
	hashes       map[string][32]byte
	cameraScopes map[string]map[string]struct{}
}

// LoadTokenAuthorizer loads device token hashes, never plaintext tokens.
// The file format is {"devices":{"device-id":"64 hex SHA-256 hash"}}.
func LoadTokenAuthorizer(path string) (*TokenAuthorizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var input struct {
		Devices map[string]string   `json:"devices"`
		Camera  map[string][]string `json:"camera"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, errors.New("invalid coordinator device hash file")
	}
	if len(input.Devices) == 0 || len(input.Devices) > maxSessions {
		return nil, errors.New("coordinator device hash file has invalid device count")
	}
	hashes := make(map[string][32]byte, len(input.Devices))
	scopes := make(map[string]map[string]struct{}, len(input.Camera))
	for id, encoded := range input.Devices {
		if !validDevice(id) || len(encoded) != sha256.Size*2 {
			return nil, errors.New("coordinator device hash file has invalid entry")
		}
		decoded, err := hex.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("coordinator device hash file has invalid hash")
		}
		var hash [32]byte
		copy(hash[:], decoded)
		hashes[id] = hash
	}
	for target, viewers := range input.Camera {
		if !validDevice(target) || len(viewers) == 0 {
			return nil, errors.New("coordinator camera registry has invalid entry")
		}
		scopes[target] = make(map[string]struct{}, len(viewers))
		for _, viewer := range viewers {
			if !validDevice(viewer) {
				return nil, errors.New("coordinator camera registry has invalid viewer")
			}
			scopes[target][viewer] = struct{}{}
		}
	}
	return &TokenAuthorizer{hashes: hashes, cameraScopes: scopes}, nil
}

func (a *TokenAuthorizer) AllowCamera(viewer, target string) bool {
	if a == nil {
		return false
	}
	_, ok := a.cameraScopes[target][viewer]
	return ok
}

func (a *TokenAuthorizer) Authenticate(r *http.Request) (string, bool) {
	if a == nil {
		return "", false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token == r.Header.Get("Authorization") {
		return "", false
	}
	actual := sha256.Sum256([]byte(token))
	for device, expected := range a.hashes {
		if subtle.ConstantTimeCompare(actual[:], expected[:]) == 1 {
			return device, true
		}
	}
	return "", false
}
