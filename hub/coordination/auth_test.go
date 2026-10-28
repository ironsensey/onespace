package coordination

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTokenAuthorizerDoesNotAcceptWrongToken(t *testing.T) {
	token := "device-token"
	sum := sha256.Sum256([]byte(token))
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.json")
	data := []byte(`{"devices":{"pi":"` + hex.EncodeToString(sum[:]) + `"}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	authorizer, err := LoadTokenAuthorizer(path)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if device, ok := authorizer.Authenticate(request); !ok || device != "pi" {
		t.Fatal("valid token rejected")
	}
	request.Header.Set("Authorization", "Bearer wrong")
	if _, ok := authorizer.Authenticate(request); ok {
		t.Fatal("wrong token accepted")
	}
}
