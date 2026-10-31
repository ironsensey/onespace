// Package wireguard provides optional integrated WireGuard connectivity for OneSpace.
// It generates and manages WireGuard configurations, handles enrollment,
// peer discovery, and direct/relay selection. It does not implement cryptography
// itself; it uses the system's WireGuard installation (wireguard-tools on Linux,
// wireguard-nt on Windows).
package wireguard

import (
	"fmt"
	"net"
	"strings"
)


// Peer represents a WireGuard peer.
type Peer struct {
	PublicKey           string
	Endpoint            string // host:port
	AllowedIPs          []string
	PersistentKeepalive int
	LastHandshake       string
	TransferRx          uint64
	TransferTx          uint64
}

// Config represents a WireGuard interface configuration.
type Config struct {
	PrivateKey string
	Address    string // e.g., "10.200.200.2/24"
	ListenPort int
	Peers      []Peer
	MTU        int
	DNS        []string
}

// RenderConfig produces a WireGuard configuration file content.
func (c *Config) RenderConfig() string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString("PrivateKey = " + c.PrivateKey + "\n")
	if c.Address != "" {
		b.WriteString("Address = " + c.Address + "\n")
	}
	if c.ListenPort > 0 {
		b.WriteString("ListenPort = " + fmt.Sprintf("%d", c.ListenPort) + "\n")
	}
	if c.MTU > 0 {
		b.WriteString("MTU = " + fmt.Sprintf("%d", c.MTU) + "\n")
	}
	for _, dns := range c.DNS {
		b.WriteString("DNS = " + dns + "\n")
	}
	b.WriteString("\n")

	for _, peer := range c.Peers {
		b.WriteString("[Peer]\n")
		b.WriteString("PublicKey = " + peer.PublicKey + "\n")
		if peer.Endpoint != "" {
			b.WriteString("Endpoint = " + peer.Endpoint + "\n")
		}
		if len(peer.AllowedIPs) > 0 {
			b.WriteString("AllowedIPs = " + strings.Join(peer.AllowedIPs, ", ") + "\n")
		}
		if peer.PersistentKeepalive > 0 {
			b.WriteString("PersistentKeepalive = " + fmt.Sprintf("%d", peer.PersistentKeepalive) + "\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// ConnectionMode represents the current connection mode.
type ConnectionMode int

const (
	ModeLAN ConnectionMode = iota
	ModeWireGuardDirect
	ModeWireGuardRelay
	ModeOffline
	ModeCoordinatorRelay // existing WebRTC/TURN fallback
)

func (m ConnectionMode) String() string {
	switch m {
	case ModeLAN:
		return "lan"
	case ModeWireGuardDirect:
		return "wireguard-direct"
	case ModeWireGuardRelay:
		return "wireguard-relay"
	case ModeOffline:
		return "offline"
	case ModeCoordinatorRelay:
		return "coordinator-relay"
	default:
		return "unknown"
	}
}

// PeerInfo holds discovery information for a peer.
type PeerInfo struct {
	DeviceID      string
	PublicKey     string
	Endpoints     []string // candidate endpoints
	RelayEndpoint string   // relay server endpoint if available
	LastSeen      string
}

// NetworkStatus holds the current network status.
type NetworkStatus struct {
	Mode          ConnectionMode
	PeerCount     int
	DirectPeers   int
	RelayPeers    int
	BytesReceived uint64
	BytesSent     uint64
	LastHandshake string
	MTU           int
}

// AllocateAddress assigns a unique IP address from the overlay subnet.
func AllocateAddress(subnet string, used []string) (string, error) {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("parse subnet: %w", err)
	}

	usedSet := make(map[string]bool)
	for _, ip := range used {
		usedSet[ip] = true
	}

	// Start from .2 (reserve .1 for gateway)
	ip := ipnet.IP.Mask(ipnet.Mask)
	ip[3] = 2

	for ipnet.Contains(ip) {
		ipStr := ip.String()
		if !usedSet[ipStr] {
			return ipStr + "/32", nil
		}
		ip[3]++
		if ip[3] == 0 {
			ip[2]++
		}
	}

	return "", fmt.Errorf("no available addresses in subnet %s", subnet)
}
