package wireguard

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
)

func configureNetwork(ctx context.Context, t *Tunnel, address netip.Prefix, routes []string) error {
	commands := [][]string{{"address", "add", address.String(), "dev", t.Name}, {"link", "set", "dev", t.Name, "up"}}
	for _, route := range routes {
		commands = append(commands, []string{"route", "add", route, "dev", t.Name, "src", address.Addr().String()})
	}
	for _, args := range commands {
		if out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("ip %v: %w: %s", args, err, out)
		}
	}
	return nil
}
