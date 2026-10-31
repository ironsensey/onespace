package main

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
	"net"
	"onespace.local/hub/wireguard"
	"os"
	"time"
)

func readCertificate(path string) (*x509.Certificate, error) {
	if path == "" {
		return nil, fmt.Errorf("certificate path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read certificate: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("CA certificate file is empty")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("malformed PEM or invalid X.509 certificate")
	}
	if len(rest) > 0 {
		for _, b := range rest {
			if b != '\n' && b != '\r' && b != ' ' && b != '\t' {
				return nil, fmt.Errorf("exactly one certificate required")
			}
		}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) {
		return nil, fmt.Errorf("certificate is expired or not yet valid")
	}
	return cert, nil
}
func LoadCACertificate(path string) (*x509.CertPool, string, error) {
	cert, err := readCertificate(path)
	if err != nil {
		return nil, "", err
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	sum := sha256.Sum256(cert.Raw)
	return pool, hex.EncodeToString(sum[:]), nil
}
func validateServerCertificate(path, ip string) error {
	cert, err := readCertificate(path)
	if err != nil {
		return err
	}
	if err = cert.VerifyHostname("test-receiver"); err != nil {
		return err
	}
	if err = cert.VerifyHostname(ip); err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	_, err = cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: "test-receiver", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}
func loadOrCreateCertificate(certPath, keyPath string) (tls.Certificate, error) {
	_, ce := os.Lstat(certPath)
	_, ke := os.Lstat(keyPath)
	if ce == nil && ke == nil {
		return tls.LoadX509KeyPair(certPath, keyPath)
	}
	// A partial pair, invalid file, or permission error never triggers replacement.
	if !errors.Is(ce, os.ErrNotExist) || !errors.Is(ke, os.ErrNotExist) {
		return tls.Certificate{}, fmt.Errorf("certificate/key pair incomplete or inaccessible; preserve and repair explicitly: %v / %v", ce, ke)
	}
	certPEM, keyPEM, err := newTestCertificate()
	if err != nil {
		return tls.Certificate{}, err
	}
	if err = wireguard.WritePrivateFile(keyPath, keyPEM); err != nil {
		return tls.Certificate{}, err
	}
	// Public export errors are fatal. A partial pair remains visible for explicit repair.
	f, err := os.OpenFile(certPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return tls.Certificate{}, err
	}
	_, we := f.Write(certPEM)
	se := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(we, se, closeErr); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}
func newTestCertificate() ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "test-receiver"}, NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: time.Now().Add(7 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"test-receiver"}, IPAddresses: []net.IP{net.ParseIP("10.200.200.2")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), nil
}
