package wireguard

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizePublicKey(t *testing.T) {
	k, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{k.PublicKey, strings.ToUpper(k.PublicKey), k.Base64PublicKey} {
		got, err := NormalizePublicKey(input)
		if err != nil || got != k.PublicKey {
			t.Fatalf("normalization failed: %v", err)
		}
	}
	for _, input := range []string{"", "abc", strings.Repeat("00", 32), k.PublicKey + "\nremove=true", k.Base64PublicKey + "\n"} {
		if _, err := NormalizePublicKey(input); err == nil {
			t.Fatal("accepted invalid key")
		}
	}
}

func TestPeerStats(t *testing.T) {
	a, _ := GenerateKeyPair()
	b, _ := GenerateKeyPair()
	raw := "private_key=do-not-log\npublic_key=" + a.PublicKey + "\nrx_bytes=123\ntx_bytes=456\nlast_handshake_time_sec=1700000000\nlast_handshake_time_nsec=42\npublic_key=" + b.PublicKey + "\nrx_bytes=7\ntx_bytes=8\nlast_handshake_time_sec=0\nlast_handshake_time_nsec=0\n"
	got, err := ParsePeerStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RxBytes != 123 || got[0].TxBytes != 456 || !got[0].LastHandshake.Equal(time.Unix(1700000000, 42)) || !got[1].LastHandshake.IsZero() {
		t.Fatalf("wrong peer stats: %+v", got)
	}
	if _, err := ParsePeerStats("public_key=" + a.PublicKey + "\nrx_bytes=nope\n"); err == nil {
		t.Fatal("invalid counter accepted")
	}
}

func TestIPCValidation(t *testing.T) {
	a, _ := GenerateKeyPair()
	b, _ := GenerateKeyPair()
	cfg := Config{PrivateKey: a.PrivateKey, ListenPort: 51820, Peers: []Peer{{PublicKey: b.Base64PublicKey, Endpoint: "192.168.0.106:51820", AllowedIPs: []string{"10.200.200.2/32"}}}}
	ipc, err := buildIPC(cfg)
	if err != nil || !strings.Contains(ipc, "public_key="+b.PublicKey+"\n") {
		t.Fatal("public key not normalized", err)
	}
	cfg.Peers[0].Endpoint = "127.0.0.1:51820\nremove=true"
	if _, err := buildIPC(cfg); err == nil {
		t.Fatal("IPC injection accepted")
	}
}
