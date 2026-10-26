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
	"math/big"
	"os"
	"path/filepath"
	"time"
)

func loadOrCreateCert(dir string) (tls.Certificate, string, error) {
	keypath := filepath.Join(dir, "identity.key")
	crtpath := filepath.Join(dir, "identity.crt")
	cert, err := tls.LoadX509KeyPair(crtpath, keypath)
	if err != nil {
		// Don't overwrite a damaged existing identity: paired devices rely on its fingerprint.
		if _, e := os.Stat(crtpath); e == nil {
			return tls.Certificate{}, "", err
		}
		if _, e := os.Stat(keypath); e == nil {
			return tls.Certificate{}, "", err
		}
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return tls.Certificate{}, "", e
		}
		serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if e != nil {
			return tls.Certificate{}, "", e
		}
		template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "OneSpace Desktop"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}}
		der, e := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if e != nil {
			return tls.Certificate{}, "", e
		}
		keyDer, e := x509.MarshalECPrivateKey(key)
		if e != nil {
			return tls.Certificate{}, "", e
		}
		if e = os.WriteFile(keypath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), 0600); e != nil {
			return tls.Certificate{}, "", e
		}
		if e = os.WriteFile(crtpath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
			return tls.Certificate{}, "", e
		}
		cert, e = tls.LoadX509KeyPair(crtpath, keypath)
		if e != nil {
			return tls.Certificate{}, "", e
		}
	}
	hash := sha256.Sum256(cert.Certificate[0])
	return cert, hex.EncodeToString(hash[:]), nil
}
