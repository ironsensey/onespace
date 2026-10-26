package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

const discoveryPort = 47602
const discoveryRequest = "ONESPACE_DISCOVER_V1 "
const discoveryReply = "ONESPACE_HERE_V1 "

// UDP discovery advertises only a public certificate fingerprint. No password,
// device token or file contents ever pass through this unauthenticated channel.
// Android verifies the pinned TLS certificate before accepting an IP change.
func serveDiscovery(ctx context.Context, fingerprint string) error {
	conn, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", discoveryPort))
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); _ = conn.Close() }()
	var b [256]byte
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, addr, e := conn.ReadFrom(b[:])
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			if t, ok := e.(net.Error); ok && t.Timeout() {
				continue
			}
			return e
		}
		peer, ok := addr.(*net.UDPAddr)
		if !ok || peer.IP == nil || (!peer.IP.IsPrivate() && !peer.IP.IsLoopback()) {
			continue
		}
		msg := string(b[:n])
		if !strings.HasPrefix(msg, discoveryRequest) {
			continue
		}
		nonce := strings.TrimPrefix(msg, discoveryRequest)
		if len(nonce) != 32 || !isLowerHex(nonce) {
			continue
		}
		_, err = conn.WriteTo([]byte(discoveryReply+nonce+" "+fingerprint), addr)
		if err != nil {
			log.Printf("discovery reply: %v", err)
		}
	}
}
func isLowerHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
