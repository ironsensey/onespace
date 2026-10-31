package wireguard

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// WritePrivateFile creates, never replaces, a secret with restrictive permissions
// at creation (an explicit protected DACL on Windows).
func WritePrivateFile(path string, data []byte) error {
	f, err := createPrivateFile(path)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func LoadOrCreateKeyPair(path string) (KeyPair, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return KeyPair{}, fmt.Errorf("key path is not a regular file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return KeyPair{}, err
		}
		// Existing malformed files are left intact, including failed partial writes.
		return LoadKeyPair(strings.TrimSpace(string(data)))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return KeyPair{}, err
	}
	kp, err := GenerateKeyPair()
	if err != nil {
		return KeyPair{}, err
	}
	if err = WritePrivateFile(path, []byte(kp.PrivateKey+"\n")); err != nil {
		return KeyPair{}, err
	}
	return kp, nil
}
