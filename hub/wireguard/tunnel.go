package wireguard

import (
	"context"
	"fmt"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"net"
	"net/netip"
	"strings"
	"sync"
)

type Tunnel struct {
	Device    *device.Device
	TUN       tun.Device
	Bind      conn.Bind
	Logger    *device.Logger
	Name      string
	closeOnce sync.Once
}
type TunnelConfig struct {
	Name     string
	MTU      int
	LogLevel int
}

func NewTunnel(cfg TunnelConfig) (*Tunnel, error) {
	if cfg.MTU == 0 {
		cfg.MTU = 1420
	}
	if cfg.Name == "" {
		cfg.Name = "wg-test"
	}
	if _, err := net.InterfaceByName(cfg.Name); err == nil {
		return nil, fmt.Errorf("interface %s already exists; refusing to reuse it", cfg.Name)
	}
	td, err := tun.CreateTUN(cfg.Name, cfg.MTU)
	if err != nil {
		return nil, fmt.Errorf("create TUN: %w", err)
	}
	name, err := td.Name()
	if err != nil {
		td.Close()
		return nil, fmt.Errorf("TUN identity: %w", err)
	}
	bind := conn.NewDefaultBind()
	logger := device.NewLogger(cfg.LogLevel, "[wireguard]")
	return &Tunnel{Device: device.NewDevice(td, bind, logger), TUN: td, Bind: bind, Logger: logger, Name: name}, nil
}

func buildIPC(cfg Config) (string, error) {
	kp, err := LoadKeyPair(cfg.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}
	if cfg.ListenPort < 0 || cfg.ListenPort > 65535 {
		return "", fmt.Errorf("invalid listen port")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\nlisten_port=%d\n", kp.PrivateKey, cfg.ListenPort)
	for _, p := range cfg.Peers {
		pub, err := NormalizePublicKey(p.PublicKey)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "public_key=%s\nreplace_allowed_ips=true\n", pub)
		if p.Endpoint != "" {
			if _, err := netip.ParseAddrPort(p.Endpoint); err != nil {
				return "", fmt.Errorf("peer endpoint must be an IP:port")
			}
			fmt.Fprintf(&b, "endpoint=%s\n", p.Endpoint)
		}
		for _, ip := range p.AllowedIPs {
			prefix, err := netip.ParsePrefix(ip)
			if err != nil {
				return "", fmt.Errorf("invalid allowed prefix")
			}
			fmt.Fprintf(&b, "allowed_ip=%s\n", prefix.Masked())
		}
		if p.PersistentKeepalive < 0 || p.PersistentKeepalive > 65535 {
			return "", fmt.Errorf("invalid keepalive")
		}
		fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", p.PersistentKeepalive)
	}
	return b.String(), nil
}
func (t *Tunnel) Configure(cfg Config) error {
	ipc, err := buildIPC(cfg)
	if err != nil {
		return err
	}
	if err = t.Device.IpcSet(ipc); err != nil {
		return fmt.Errorf("configure device: %w", err)
	}
	if err = t.Device.Up(); err != nil {
		return fmt.Errorf("bring up: %w", err)
	}
	return nil
}

// ConfigureNetwork configures only this newly-created adapter and explicit peer routes.
func (t *Tunnel) ConfigureNetwork(ctx context.Context, address string, routes []string) error {
	prefix, err := netip.ParsePrefix(address)
	if err != nil || !prefix.Addr().Is4() {
		return fmt.Errorf("an IPv4 interface prefix is required")
	}
	for _, route := range routes {
		p, err := netip.ParsePrefix(route)
		if err != nil || !p.Addr().Is4() || p.Bits() != 32 {
			return fmt.Errorf("test routes must be IPv4 /32 host routes")
		}
	}
	return configureNetwork(ctx, t, prefix, routes)
}
func (t *Tunnel) RemovePeer(key string) error {
	pub, err := NormalizePublicKey(key)
	if err != nil {
		return err
	}
	return t.Device.IpcSet("public_key=" + pub + "\nremove=true\n")
}
func (t *Tunnel) Close() {
	t.closeOnce.Do(func() {
		if t.Device != nil {
			t.Device.Close()
		}
	})
}
func (t *Tunnel) PeerStats() ([]PeerStats, error) {
	raw, err := t.Device.IpcGet()
	if err != nil {
		return nil, err
	}
	return ParsePeerStats(raw)
}
func (t *Tunnel) GetStats() (rx, tx uint64, err error) {
	peers, err := t.PeerStats()
	if err != nil {
		return 0, 0, err
	}
	for _, p := range peers {
		rx += p.RxBytes
		tx += p.TxBytes
	}
	return rx, tx, nil
}
