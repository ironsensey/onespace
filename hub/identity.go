package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Identity keeps an existing certificate and refuses to overwrite any partial or
// damaged identity, because clients pin its exact SHA-256 fingerprint.
func Identity(dir string) (tls.Certificate, string, error) {
	certPath := filepath.Join(dir, "identity.crt")
	keyPath := filepath.Join(dir, "identity.key")
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		for _, path := range []string{certPath, keyPath} {
			if _, statErr := os.Lstat(path); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
				return tls.Certificate{}, "", fmt.Errorf("existing identity is incomplete or unreadable; preserve it and restore a backup: %w", err)
			}
		}
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return tls.Certificate{}, "", err
		}
		template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "OneSpace Pi Hub"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}}
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		if err = writeExclusive(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
			return tls.Certificate{}, "", err
		}
		if err = writeExclusive(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
			return tls.Certificate{}, "", err
		}
		cert, err = tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, "", err
		}
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now()
	if now.Before(parsed.NotBefore) || now.After(parsed.NotAfter) {
		return tls.Certificate{}, "", errors.New("hub certificate is not currently valid; correct the clock or perform an authenticated renewal")
	}
	sum := sha256.Sum256(cert.Certificate[0])
	return cert, hex.EncodeToString(sum[:]), nil
}

func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
