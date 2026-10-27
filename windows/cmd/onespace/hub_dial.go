package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"sync"
	"time"
)

var localHubFailures = struct {
	sync.Mutex
	values map[string]time.Time
}{values: map[string]time.Time{}}

// Select a route before sending HTTP or credentials. An unreachable LAN route
// is cooled down to avoid adding a failed probe to every cloud upload chunk.
func pinnedHubDial(c HubConfig, config *tls.Config) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		remote, _ := url.Parse(c.Address)
		settings := config.Clone()
		settings.ServerName = remote.Hostname()
		settings.NextProtos = []string{"http/1.1"}
		dial := func(destination string, timeout time.Duration) (net.Conn, error) {
			return (&tls.Dialer{Config: settings, NetDialer: &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}}).DialContext(ctx, network, destination)
		}
		if c.LocalAddress != "" {
			key := c.Fingerprint + ":" + c.LocalAddress
			localHubFailures.Lock()
			until := localHubFailures.values[key]
			localHubFailures.Unlock()
			if time.Now().After(until) {
				local, _ := url.Parse(c.LocalAddress)
				destination := local.Host
				if local.Port() == "" {
					destination = net.JoinHostPort(local.Hostname(), "443")
				}
				attempt, stop := context.WithTimeout(ctx, 800*time.Millisecond)
				conn, err := (&tls.Dialer{Config: settings, NetDialer: &net.Dialer{Timeout: 800 * time.Millisecond, KeepAlive: 30 * time.Second}}).DialContext(attempt, network, destination)
				stop()
				if err == nil {
					return conn, nil
				}
				localHubFailures.Lock()
				if len(localHubFailures.values) >= 64 {
					localHubFailures.values = map[string]time.Time{}
				}
				localHubFailures.values[key] = time.Now().Add(30 * time.Second)
				localHubFailures.Unlock()
			}
		}
		return dial(address, 4*time.Second)
	}
}
