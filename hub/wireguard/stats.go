package wireguard

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type PeerStats struct {
	PublicKey     string    `json:"publicKey"`
	RxBytes       uint64    `json:"rxBytes"`
	TxBytes       uint64    `json:"txBytes"`
	LastHandshake time.Time `json:"lastHandshake"`
}

// ParsePeerStats deliberately discards interface secrets; raw IPC must never be logged.
func ParsePeerStats(raw string) ([]PeerStats, error) {
	var peers []PeerStats
	var sec, nsec int64
	finish := func() {
		if len(peers) > 0 && sec != 0 {
			peers[len(peers)-1].LastHandshake = time.Unix(sec, nsec).UTC()
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key == "public_key" {
			finish()
			pub, err := NormalizePublicKey(value)
			if err != nil {
				return nil, err
			}
			peers = append(peers, PeerStats{PublicKey: pub})
			sec = 0
			nsec = 0
			continue
		}
		if len(peers) == 0 {
			continue
		}
		p := &peers[len(peers)-1]
		switch key {
		case "rx_bytes", "tx_bytes":
			n, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid %s", key)
			}
			if key == "rx_bytes" {
				p.RxBytes = n
			} else {
				p.TxBytes = n
			}
		case "last_handshake_time_sec", "last_handshake_time_nsec":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 || (key == "last_handshake_time_nsec" && n >= 1e9) {
				return nil, fmt.Errorf("invalid handshake timestamp")
			}
			if key == "last_handshake_time_sec" {
				sec = n
			} else {
				nsec = n
			}
		}
	}
	finish()
	return peers, nil
}
