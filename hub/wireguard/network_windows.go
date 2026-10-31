package wireguard

import (
	"context"
	"fmt"
	"golang.org/x/sys/windows"
	"net/netip"
	"os/exec"
	"unsafe"
)

func configureNetwork(ctx context.Context, t *Tunnel, address netip.Prefix, routes []string) error {
	native, ok := t.TUN.(interface{ LUID() uint64 })
	if !ok {
		return fmt.Errorf("TUN does not expose its Windows LUID")
	}
	luid := native.LUID()
	var index uint32
	proc := windows.NewLazySystemDLL("iphlpapi.dll").NewProc("ConvertInterfaceLuidToIndex")
	result, _, _ := proc.Call(uintptr(unsafe.Pointer(&luid)), uintptr(unsafe.Pointer(&index)))
	if result != 0 {
		return fmt.Errorf("resolve TUN LUID: %w", windows.Errno(result))
	}
	// Only validated numeric IPs and the OS-resolved index enter this script.
	// ActiveStore entries disappear with the temporary adapter.
	script := fmt.Sprintf("$ErrorActionPreference='Stop'; New-NetIPAddress -InterfaceIndex %d -IPAddress '%s' -PrefixLength %d -PolicyStore ActiveStore | Out-Null;", index, address.Addr(), address.Bits())
	for _, route := range routes {
		script += fmt.Sprintf("New-NetRoute -InterfaceIndex %d -DestinationPrefix '%s' -NextHop '0.0.0.0' -RouteMetric 5 -PolicyStore ActiveStore | Out-Null;", index, route)
	}
	if out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput(); err != nil {
		return fmt.Errorf("configure adapter index %d: %w: %s", index, err, out)
	}
	fmt.Printf("ADAPTER_INDEX=%d LUID=%d\n", index, luid)
	return nil
}
