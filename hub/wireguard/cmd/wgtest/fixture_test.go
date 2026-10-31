package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureCert(t *testing.T) (tls.Certificate, *x509.CertPool, string) {
	t.Helper()
	d := t.TempDir()
	path := filepath.Join(d, "cert.pem")
	cert, err := loadOrCreateCertificate(path, filepath.Join(d, "tls.key"))
	if err != nil {
		t.Fatal(err)
	}
	roots, _, err := LoadCACertificate(path)
	if err != nil {
		t.Fatal(err)
	}
	return cert, roots, path
}
func TestCertificatePersistenceAndPartialFiles(t *testing.T) {
	cert, _, path := fixtureCert(t)
	key := filepath.Join(filepath.Dir(path), "tls.key")
	again, err := loadOrCreateCertificate(path, key)
	if err != nil || !bytes.Equal(cert.Certificate[0], again.Certificate[0]) {
		t.Fatal("certificate replaced", err)
	}
	if err = validateServerCertificate(path, "10.200.200.2"); err != nil {
		t.Fatal(err)
	}
	if err = validateServerCertificate(path, "10.200.200.3"); err == nil {
		t.Fatal("wrong SAN accepted")
	}
	if err = os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if _, err = loadOrCreateCertificate(path, key); err == nil {
		t.Fatal("partial pair replaced")
	}
}
func TestClientValidatesCertificateBeforeResources(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "wg.key")
	args := []string{"-mode", "client", "-key-file", path, "-peer-key", strings.Repeat("11", 32), "-peer-ip", "127.0.0.1", "-ca-cert", filepath.Join(d, "missing")}
	if err := execute(context.Background(), args, io.Discard); err == nil {
		t.Fatal("missing cert accepted")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("key created before certificate validation")
	}
}
func TestHTTPSResultsAndVerification(t *testing.T) {
	cert, roots, _ := fixtureCert(t)
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		trust   bool
		wantErr bool
	}{{"valid", 200, "ok", true, false}, {"bad status", 503, "ok", true, true}, {"bad body", 200, "wrong", true, true}, {"untrusted", 200, "ok", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			s.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
			s.StartTLS()
			defer s.Close()
			pool := roots
			if !tc.trust {
				pool = x509.NewCertPool()
			}
			err := requestHTTPS(context.Background(), s.URL, "", pool, io.Discard)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v expectedError=%v", err, tc.wantErr)
			}
		})
	}
}
func TestListenerConflictIsFatal(t *testing.T) {
	cert, _, _ := fixtureCert(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s, _, err := startHTTPS(context.Background(), l.Addr().String(), cert, io.Discard)
	if err == nil {
		if s != nil {
			s.Close()
		}
		t.Fatal("bind conflict ignored")
	}
}
func TestReadinessCancellationAndFatalResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := awaitReady(ctx, func(context.Context) error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("cancellation ignored", err)
	}
	fatal := errors.New("invalid HTTPS body")
	calls = 0
	err = awaitReady(context.Background(), func(context.Context) error { calls++; return fatal })
	if !errors.Is(err, fatal) || calls != 1 {
		t.Fatal("invalid response retried")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = awaitReady(ctx, func(context.Context) error { return &net.OpError{Op: "dial", Err: errors.New("not ready")} })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline ignored", err)
	}
}
