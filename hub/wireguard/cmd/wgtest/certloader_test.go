package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadCACertificate tests the certificate loading functionality with various scenarios
func TestLoadCACertificate(t *testing.T) {
	tests := []struct {
		name          string
		certPEM       []byte
		expectError   bool
		errorContains string
	}{
		{
			name: "valid certificate",
			certPEM: func() []byte {
				key, _ := rsa.GenerateKey(rand.Reader, 2048)
				template := &x509.Certificate{
					SerialNumber:          big.NewInt(1),
					Subject:               pkix.Name{CommonName: "test-ca"},
					NotBefore:             time.Now(),
					NotAfter:              time.Now().Add(time.Hour),
					KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
					ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
					IsCA:                  true,
					BasicConstraintsValid: true,
				}
				certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
				return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
			}(),
			expectError: false,
		},
		{
			name:          "missing file",
			certPEM:       nil,
			expectError:   true,
			errorContains: "read certificate",
		},
		{
			name:          "empty file",
			certPEM:       []byte(""),
			expectError:   true,
			errorContains: "CA certificate file is empty",
		},
		{
			name:          "malformed PEM",
			certPEM:       []byte("not a valid certificate"),
			expectError:   true,
			errorContains: "malformed PEM or invalid X.509 certificate",
		},
		{
			name: "valid PEM but not a certificate",
			certPEM: func() []byte {
				key, _ := rsa.GenerateKey(rand.Reader, 2048)
				keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
				return keyPEM
			}(),
			expectError:   true,
			errorContains: "malformed PEM or invalid X.509 certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary file for the certificate
			tmpDir := t.TempDir()
			certPath := filepath.Join(tmpDir, "ca.pem")

			if tt.certPEM != nil {
				err := os.WriteFile(certPath, tt.certPEM, 0644)
				if err != nil {
					t.Fatalf("failed to write test certificate: %v", err)
				}
			}

			pool, fingerprint, err := LoadCACertificate(certPath)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errorContains)
				} else if !contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got %q", tt.errorContains, err.Error())
				}
				if pool != nil {
					t.Errorf("expected nil pool on error, got %v", pool)
				}
				if fingerprint != "" {
					t.Errorf("expected empty fingerprint on error, got %s", fingerprint)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if pool == nil {
					t.Errorf("expected non-nil pool, got nil")
				}
				if fingerprint == "" {
					t.Errorf("expected non-empty fingerprint")
				}
				// Verify fingerprint format (64 hex characters for SHA-256)
				if len(fingerprint) != 64 {
					t.Errorf("expected 64-character hex fingerprint, got %d: %s", len(fingerprint), fingerprint)
				}
				// Verify fingerprint is valid hex
				if _, err := hex.DecodeString(fingerprint); err != nil {
					t.Errorf("fingerprint is not valid hex: %v", err)
				}
			}
		})
	}
}

// TestLoadCACertificate_FileErrors tests file system related errors
func TestLoadCACertificate_FileErrors(t *testing.T) {
	tmpDir := t.TempDir()

	// Test non-existent file
	nonExistent := filepath.Join(tmpDir, "nonexistent.pem")
	_, _, err := LoadCACertificate(nonExistent)
	if err == nil {
		t.Error("expected error for non-existent file")
	}

	// Test unreadable file (permission denied)
	unreadable := filepath.Join(tmpDir, "unreadable.pem")
	writeErr := os.WriteFile(unreadable, []byte("test"), 0000)
	if writeErr == nil {
		// Try to read it - should fail
		_, _, err := LoadCACertificate(unreadable)
		if err == nil {
			t.Error("expected error for unreadable file")
		}
		// Restore permissions for cleanup
		os.Chmod(unreadable, 0644)
	}
}

// TestLoadCACertificate_FingerprintConsistency tests that the same certificate produces the same fingerprint
func TestLoadCACertificate_FingerprintConsistency(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "ca.pem")
	err := os.WriteFile(certPath, certPEM, 0644)
	if err != nil {
		t.Fatalf("failed to write test certificate: %v", err)
	}

	// Load twice and verify fingerprint consistency
	_, fp1, err := LoadCACertificate(certPath)
	if err != nil {
		t.Fatalf("first load failed: %v", err)
	}
	_, fp2, err := LoadCACertificate(certPath)
	if err != nil {
		t.Fatalf("second load failed: %v", err)
	}
	if fp1 != fp2 {
		t.Errorf("fingerprint mismatch: %s vs %s", fp1, fp2)
	}

	// Verify fingerprint matches direct SHA-256 of DER
	parsedCert, _ := x509.ParseCertificate(certDER)
	sum := sha256.Sum256(parsedCert.Raw)
	expectedFP := hex.EncodeToString(sum[:])
	if fp1 != expectedFP {
		t.Errorf("fingerprint mismatch with direct calculation: %s vs %s", fp1, expectedFP)
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
