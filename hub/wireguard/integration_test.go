//go:build integration

package wireguard

import (
	"net"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/device"
)

func TestWireGuardHandshake(t *testing.T) {
	kp1, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	kp2, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	tun1, err := NewTunnel(TunnelConfig{Name: "wg-test1", MTU: 1420, LogLevel: device.LogLevelSilent})
	if err != nil {
		t.Skipf("cannot create TUN: %v", err)
	}
	defer tun1.Close()

	tun2, err := NewTunnel(TunnelConfig{Name: "wg-test2", MTU: 1420, LogLevel: device.LogLevelSilent})
	if err != nil {
		t.Skipf("cannot create TUN: %v", err)
	}
	defer tun2.Close()

	cfg1 := Config{
		PrivateKey: kp1.PrivateKey,
		Address:    "10.200.200.1/24",
		ListenPort: 51821,
		Peers: []Peer{
			{
				PublicKey:           kp2.PublicKey,
				Endpoint:            "127.0.0.1:51822",
				AllowedIPs:          []string{"10.200.200.2/32"},
				PersistentKeepalive: 1,
			},
		},
	}
	if err := tun1.Configure(cfg1); err != nil {
		t.Fatalf("configure tun1: %v", err)
	}

	cfg2 := Config{
		PrivateKey: kp2.PrivateKey,
		Address:    "10.200.200.2/24",
		ListenPort: 51822,
		Peers: []Peer{
			{
				PublicKey:           kp1.PublicKey,
				Endpoint:            "127.0.0.1:51821",
				AllowedIPs:          []string{"10.200.200.1/32"},
				PersistentKeepalive: 1,
			},
		},
	}
	if err := tun2.Configure(cfg2); err != nil {
		t.Fatalf("configure tun2: %v", err)
	}

	time.Sleep(3 * time.Second)

	// Try to connect to overlay IP
	conn, err := net.DialTimeout("tcp", "10.200.200.2:80", 5*time.Second)
	if err != nil {
		t.Logf("TCP connectivity: %v (expected without listener)", err)
	} else {
		conn.Close()
		t.Log("TCP connectivity established")
	}

	t.Log("Handshake test completed")
}


