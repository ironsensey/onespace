// addpeer talks to an existing wireguard-go Unix UAPI socket. It cannot attach
// to a kernel interface or invent a device API. wgtest uses startup peer options.
package main

import (
	"bufio"
	"fmt"
	"net"
	"net/netip"
	"onespace.local/hub/wireguard"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("usage: addpeer <interface> <public-key> <endpoint-ip:port> <allowed-prefix>")
	}
	name := args[0]
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\r\n") {
		return fmt.Errorf("invalid interface")
	}
	key, err := wireguard.NormalizePublicKey(args[1])
	if err != nil {
		return err
	}
	endpoint, err := netip.ParseAddrPort(args[2])
	if err != nil {
		return err
	}
	prefix, err := netip.ParsePrefix(args[3])
	if err != nil {
		return err
	}
	c, err := net.DialTimeout("unix", filepath.Join("/var/run/wireguard", name+".sock"), 3*time.Second)
	if err != nil {
		return fmt.Errorf("connect existing UAPI socket: %w", err)
	}
	defer c.Close()
	if err = c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	if _, err = fmt.Fprintf(c, "set=1\npublic_key=%s\nendpoint=%s\nallowed_ip=%s\npersistent_keepalive_interval=5\n\n", key, endpoint, prefix); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != "errno=0" {
		return fmt.Errorf("peer update rejected: %s", line)
	}
	fmt.Println("PEER_ADDED")
	return nil
}
