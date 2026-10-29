package wireguard

import (
	"strings"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if kp.PrivateKey == "" {
		t.Error("PrivateKey is empty")
	}
	if kp.PublicKey == "" {
		t.Error("PublicKey is empty")
	}
	if kp.PrivateKey == kp.PublicKey {
		t.Error("PrivateKey equals PublicKey")
	}
}

func TestRenderConfig(t *testing.T) {
	kp, _ := GenerateKeyPair()
	cfg := Config{
		PrivateKey: kp.PrivateKey,
		Address:    "10.200.200.2/32",
		ListenPort: 51820,
		MTU:        1280,
		Peers: []Peer{
			{
				PublicKey:           "testpubkey",
				Endpoint:            "1.2.3.4:51820",
				AllowedIPs:          []string{"10.200.200.1/32"},
				PersistentKeepalive: 25,
			},
		},
	}
	rendered := cfg.RenderConfig()
	if !strings.Contains(rendered, "[Interface]") {
		t.Error("missing [Interface] section")
	}
	if !strings.Contains(rendered, "[Peer]") {
		t.Error("missing [Peer] section")
	}
	if !strings.Contains(rendered, "PrivateKey = "+kp.PrivateKey) {
		t.Error("missing PrivateKey")
	}
	if !strings.Contains(rendered, "PublicKey = testpubkey") {
		t.Error("missing Peer PublicKey")
	}
}

func TestAllocateAddress(t *testing.T) {
	addr, err := AllocateAddress("10.200.200.0/24", []string{"10.200.200.1", "10.200.200.2"})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}
	if addr != "10.200.200.3/32" {
		t.Errorf("expected 10.200.200.3/32, got %s", addr)
	}
}

func TestConnectionModeString(t *testing.T) {
	if ModeLAN.String() != "lan" {
		t.Errorf("expected 'lan', got '%s'", ModeLAN.String())
	}
	if ModeWireGuardDirect.String() != "wireguard-direct" {
		t.Errorf("expected 'wireguard-direct', got '%s'", ModeWireGuardDirect.String())
	}
}