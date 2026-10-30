//go:build integration

package wireguard

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// TestWireGuardEndToEnd creates two real WireGuard peers, performs a handshake,
// sends an authenticated HTTPS request through the tunnel, revokes one peer,
// and verifies traffic stops.
func TestWireGuardEndToEnd(t *testing.T) {
	// Generate keys for both peers
	kp1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate peer1 key: %v", err)
	}
	kp2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate peer2 key: %v", err)
	}

	// Create peer 1 tunnel (listens on 51821)
	tun1, err := tun.CreateTUN("wg-test1", 1420)
	if err != nil {
		t.Skipf("cannot create TUN device: %v", err)
	}
	defer tun1.Close()

	bind1 := conn.NewDefaultBind()
	dev1 := device.NewDevice(tun1, bind1, device.NewLogger(device.LogLevelSilent, "[wg1]"))
	defer dev1.Close()

	cfg1 := fmt.Sprintf("private_key=%s\nlisten_port=51821\n", kp1.PrivateKey)
	if err := dev1.IpcSet(cfg1); err != nil {
		t.Fatalf("configure peer1: %v", err)
	}
	if err := dev1.Up(); err != nil {
		t.Fatalf("bring up peer1: %v", err)
	}

	// Create peer 2 tunnel (listens on 51822)
	tun2, err := tun.CreateTUN("wg-test2", 1420)
	if err != nil {
		t.Skipf("cannot create TUN device: %v", err)
	}
	defer tun2.Close()

	bind2 := conn.NewDefaultBind()
	dev2 := device.NewDevice(tun2, bind2, device.NewLogger(device.LogLevelSilent, "[wg2]"))
	defer dev2.Close()

	cfg2 := fmt.Sprintf("private_key=%s\nlisten_port=51822\n", kp2.PrivateKey)
	if err := dev2.IpcSet(cfg2); err != nil {
		t.Fatalf("configure peer2: %v", err)
	}
	if err := dev2.Up(); err != nil {
		t.Fatalf("bring up peer2: %v", err)
	}

	// Configure peer1 -> peer2
	cfg1p := fmt.Sprintf(
		"public_key=%s\nendpoint=127.0.0.1:51822\nallowed_ip=10.200.200.2/32\npersistent_keepalive_interval=1\n",
		kp2.PublicKey,
	)
	if err := dev1.IpcSet(cfg1p); err != nil {
		t.Fatalf("configure peer1 peer: %v", err)
	}

	// Configure peer2 -> peer1
	cfg2p := fmt.Sprintf(
		"public_key=%s\nendpoint=127.0.0.1:51821\nallowed_ip=10.200.200.1/32\npersistent_keepalive_interval=1\n",
		kp1.PublicKey,
	)
	if err := dev2.IpcSet(cfg2p); err != nil {
		t.Fatalf("configure peer2 peer: %v", err)
	}

	// Wait for handshake
	time.Sleep(3 * time.Second)

	// Start HTTPS server on peer 2's overlay IP
	tlsCert, err := generateTestCert()
	if err != nil {
		t.Fatalf("generate test cert: %v", err)
	}
	server := &http.Server{
		Addr:      "10.200.200.2:8443",
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{*tlsCert}},
		Handler:   http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("ok"))
		}),
	}
	go server.ListenAndServeTLS("", "")
	defer server.Close()

	// Wait for server to be ready
	time.Sleep(1 * time.Second)

	// Make HTTPS request from peer 1 through tunnel
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				localAddr := &net.TCPAddr{IP: net.ParseIP("10.200.200.1"), Port: 0}
				d := &net.Dialer{LocalAddr: localAddr}
				return d.DialContext(ctx, network, addr)
			},
		},
	}

	req, err := http.NewRequest("GET", "https://10.200.200.2:8443/", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through tunnel failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "ok" {
		t.Errorf("expected 'ok', got %q", string(body))
	}
	t.Log("HTTPS request through WireGuard tunnel succeeded")

	// Revoke peer 1 from peer 2's device
	if err := dev2.IpcSet("public_key=" + kp1.PublicKey + "\n"); err != nil {
		t.Fatalf("revoke peer1 from peer2: %v", err)
	}
	t.Log("peer1 revoked from peer2")

	// Wait for revocation to take effect
	time.Sleep(3 * time.Second)

	// Verify request fails after revocation
	resp2, err := client.Do(req)
	if err == nil {
		resp2.Body.Close()
		t.Error("expected request to fail after revocation, but it succeeded")
	} else {
		t.Logf("request correctly failed after revocation: %v", err)
	}

	t.Log("WireGuard end-to-end test completed")
}

// generateTestCert creates a self-signed test certificate.
func generateTestCert() (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-receiver"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}
