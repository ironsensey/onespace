//go:build !windows && !linux

package wireguard

import (
	"context"
	"fmt"
	"net/netip"
)

func configureNetwork(context.Context, *Tunnel, netip.Prefix, []string) error {
	return fmt.Errorf("network setup supports Windows and Linux only")
}
