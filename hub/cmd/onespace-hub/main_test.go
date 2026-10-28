package main

import "testing"

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
