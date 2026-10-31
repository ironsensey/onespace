package wireguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	a, err := LoadOrCreateKeyPair(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateKeyPair(path)
	if err != nil || a.PublicKey != b.PublicKey {
		t.Fatal("identity changed", err)
	}
	if err := WritePrivateFile(path, []byte("replacement")); err == nil {
		t.Fatal("secret overwritten")
	}
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKeyPair(path); err == nil {
		t.Fatal("invalid key accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "invalid" {
		t.Fatal("invalid file replaced")
	}
	if _, err := LoadOrCreateKeyPair(filepath.Dir(path)); err == nil {
		t.Fatal("directory accepted as key")
	}
}
