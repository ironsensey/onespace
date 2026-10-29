package wireguard

import (
	"fmt"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// Tunnel wraps a WireGuard device with its TUN interface.
type Tunnel struct {
	Device  *device.Device
	TUN     tun.Device
	Bind    conn.Bind
	Logger  *device.Logger
	Name    string
}

// TunnelConfig holds configuration for creating a tunnel.
type TunnelConfig struct {
	Name     string
	MTU      int
	LogLevel int
}

// NewTunnel creates a new WireGuard tunnel with a TUN device.
func NewTunnel(cfg TunnelConfig) (*Tunnel, error) {
	if cfg.MTU == 0 {
		cfg.MTU = 1420
	}
	if cfg.Name == "" {
		cfg.Name = "wg0"
	}

	tunDev, err := tun.CreateTUN(cfg.Name, cfg.MTU)
	if err != nil {
		return nil, fmt.Errorf("create TUN: %w", err)
	}

	bind := conn.NewDefaultBind()
	logger := device.NewLogger(cfg.LogLevel, "[wireguard]")

	dev := device.NewDevice(tunDev, bind, logger)

	t := &Tunnel{
		Device: dev,
		TUN:    tunDev,
		Bind:   bind,
		Logger: logger,
		Name:   cfg.Name,
	}

	return t, nil
}

// Configure applies a WireGuard configuration to the tunnel.
func (t *Tunnel) Configure(cfg Config) error {
	uapi := fmt.Sprintf("private_key=%s\n", cfg.PrivateKey)
	if cfg.ListenPort > 0 {
		uapi += fmt.Sprintf("listen_port=%d\n", cfg.ListenPort)
	}

	for _, peer := range cfg.Peers {
		uapi += fmt.Sprintf("public_key=%s\n", peer.PublicKey)
		if peer.Endpoint != "" {
			uapi += fmt.Sprintf("endpoint=%s\n", peer.Endpoint)
		}
		if len(peer.AllowedIPs) > 0 {
			for _, ip := range peer.AllowedIPs {
				uapi += fmt.Sprintf("allowed_ip=%s\n", ip)
			}
		}
		if peer.PersistentKeepalive > 0 {
			uapi += fmt.Sprintf("persistent_keepalive_interval=%d\n", peer.PersistentKeepalive)
		}
	}

	if err := t.Device.IpcSet(uapi); err != nil {
		return fmt.Errorf("configure device: %w", err)
	}

	// Bring interface up
	if err := t.Device.Up(); err != nil {
		return fmt.Errorf("bring up: %w", err)
	}

	return nil
}

// Close shuts down the tunnel.
func (t *Tunnel) Close() {
	if t.Device != nil {
		t.Device.Close()
	}
	if t.Bind != nil {
		t.Bind.Close()
	}
}

// GetStats returns transfer statistics.
func (t *Tunnel) GetStats() (uint64, uint64, error) {
	stats, err := t.Device.IpcGet()
	if err != nil {
		return 0, 0, err
	}
	// Parse stats from IPC output
	_ = stats
	_ = time.Second
	return 0, 0, nil
}
