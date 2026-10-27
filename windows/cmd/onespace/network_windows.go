//go:build windows

package main

import (
	"context"
	"golang.org/x/sys/windows"
	"log"
	"syscall"
	"time"
)

// OS notifications avoid periodic network probes and rebuild outbound hub
// connections when a laptop obtains a new address or changes interfaces.
func watchNetworkChanges(ctx context.Context, changed func()) {
	events := make(chan struct{}, 1)
	callback := syscall.NewCallback(func(_ uintptr, _ uintptr, _ uint32) uintptr {
		select {
		case events <- struct{}{}:
		default:
		}
		return 0
	})
	var interfaces, addresses windows.Handle
	if err := windows.NotifyIpInterfaceChange(windows.AF_UNSPEC, callback, nil, false, &interfaces); err != nil {
		log.Print("Network interface notifications unavailable")
		return
	}
	defer windows.CancelMibChangeNotify2(interfaces)
	if err := windows.NotifyUnicastIpAddressChange(windows.AF_UNSPEC, callback, nil, false, &addresses); err != nil {
		log.Print("Network address notifications unavailable")
	} else {
		defer windows.CancelMibChangeNotify2(addresses)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-events:
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			for len(events) > 0 {
				<-events
			}
			localHubFailures.Lock()
			localHubFailures.values = map[string]time.Time{}
			localHubFailures.Unlock()
			changed()
		}
	}
}
