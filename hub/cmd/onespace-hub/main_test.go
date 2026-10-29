package main

import (
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCameraICEServersAcceptTurnTransportQuery(t *testing.T) {
	servers, err := cameraICEServers(`[{"urls":["turn:[2001:db8::1]:3478?transport=udp","turn:[2001:db8::1]:3478?transport=tcp"],"username":"onespace","credential":"secret"}]`)
	if err != nil {
		t.Fatalf("cameraICEServers rejected installer output: %v", err)
	}
	if len(servers) != 1 || len(servers[0].URLs) != 2 {
		t.Fatalf("unexpected ICE server result: %#v", servers)
	}
}

func TestCameraICEServersRejectUnexpectedQuery(t *testing.T) {
	tests := []string{
		`[{"urls":["stun:stun.example:3478?transport=udp"]}]`,
		`[{"urls":["turn:turn.example:3478?transport=tls"],"username":"u","credential":"c"}]`,
		`[{"urls":["turn:turn.example:3478?transport=udp&transport=tcp"],"username":"u","credential":"c"}]`,
	}
	for _, raw := range tests {
		if _, err := cameraICEServers(raw); err == nil {
			t.Errorf("cameraICEServers accepted invalid configuration %s", raw)
		}
	}
}

func TestCameraTURNURLsAndShortLivedCredentials(t *testing.T) {
	raw := `["turn:turn.example:3478?transport=udp","turns:turn.example:5349?transport=tcp"]`
	urls, err := cameraTURNURLs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("unexpected TURN URLs: %#v", urls)
	}
	for _, value := range urls {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "turn" && parsed.Scheme != "turns" {
			t.Fatalf("invalid parsed TURN URL %q: %v", value, err)
		}
	}
	provider := turnProvider(urls, "01234567890123456789012345678901", time.Minute)
	servers := provider()
	if len(servers) != 1 || servers[0].Credential == "" {
		t.Fatalf("missing generated credential: %#v", servers)
	}
	parts := strings.Split(servers[0].Username, ":")
	if len(parts) != 2 {
		t.Fatalf("unexpected REST username %q", servers[0].Username)
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expiry < time.Now().Unix() || expiry > time.Now().Add(2*time.Minute).Unix() {
		t.Fatalf("unexpected expiry %q", servers[0].Username)
	}
	if _, err := base64.StdEncoding.DecodeString(servers[0].Credential); err != nil {
		t.Fatalf("credential is not base64: %v", err)
	}
}

func TestCameraTURNURLsRejectInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{
		`["stun:stun.example:3478"]`,
		`["turn:turn.example:3478?transport=tls"]`,
		`["turn:turn.example:3478?transport=udp&transport=tcp"]`,
	} {
		if _, err := cameraTURNURLs(raw); err == nil {
			t.Errorf("cameraTURNURLs accepted %s", raw)
		}
	}
}
