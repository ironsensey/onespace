package wireguard

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// KeyPair holds a WireGuard public/private key pair.
// PrivateKey and PublicKey are hex-encoded for wireguard-go IPC compatibility.
// Use Base64PrivateKey/Base64PublicKey for standard WireGuard format.
type KeyPair struct {
	PrivateKey       string // hex-encoded (wireguard-go IPC format)
	PublicKey        string // hex-encoded (wireguard-go IPC format)
	Base64PrivateKey string // base64-encoded (standard WireGuard format)
	Base64PublicKey  string // base64-encoded (standard WireGuard format)
}

// GenerateKeyPair generates a new WireGuard key pair using crypto/ecdh X25519.
// Returns hex-encoded keys for wireguard-go IPC and base64 for standard WireGuard.
func GenerateKeyPair() (KeyPair, error) {
	curve := ecdh.X25519()
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate key: %w", err)
	}
	privBytes := priv.Bytes()
	pubBytes := priv.PublicKey().Bytes()

	return KeyPair{
		PrivateKey:       hex.EncodeToString(privBytes),
		PublicKey:        hex.EncodeToString(pubBytes),
		Base64PrivateKey: base64.StdEncoding.EncodeToString(privBytes),
		Base64PublicKey:  base64.StdEncoding.EncodeToString(pubBytes),
	}, nil
}

// LoadKeyPair loads a WireGuard key pair from a hex-encoded private key.
func LoadKeyPair(privHex string) (KeyPair, error) {
	privBytes, err := hex.DecodeString(privHex)
	if err != nil {
		return KeyPair{}, fmt.Errorf("decode key: %w", err)
	}
	if len(privBytes) != 32 {
		return KeyPair{}, fmt.Errorf("invalid key length: %d", len(privBytes))
	}
	curve := ecdh.X25519()
	priv, err := curve.NewPrivateKey(privBytes)
	if err != nil {
		return KeyPair{}, fmt.Errorf("load private key: %w", err)
	}
	pubBytes := priv.PublicKey().Bytes()
	return KeyPair{
		PrivateKey:       hex.EncodeToString(privBytes),
		PublicKey:        hex.EncodeToString(pubBytes),
		Base64PrivateKey: base64.StdEncoding.EncodeToString(privBytes),
		Base64PublicKey:  base64.StdEncoding.EncodeToString(pubBytes),
	}, nil
}

// Base64Encode encodes bytes to base64.
func Base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// NormalizePublicKey accepts standard Base64 or IPC hex without accepting IPC delimiters.
func NormalizePublicKey(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n\t ") {
		return "", fmt.Errorf("public key contains whitespace")
	}
	var raw []byte
	var err error
	if len(value) == 64 {
		raw, err = hex.DecodeString(value)
	} else {
		raw, err = base64.StdEncoding.Strict().DecodeString(value)
	}
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("public key must encode exactly 32 bytes as hex or Base64")
	}
	var nonzero byte
	for _, b := range raw {
		nonzero |= b
	}
	if nonzero == 0 {
		return "", fmt.Errorf("zero public key is not a peer")
	}
	return hex.EncodeToString(raw), nil
}
