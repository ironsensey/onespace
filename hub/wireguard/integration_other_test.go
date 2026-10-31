//go:build integration && !linux

package wireguard

import "testing"

func TestWireGuardHTTPSAndRevocation(t *testing.T) {
	t.Fatal("namespace integration requires Linux root; Windows physical acceptance uses tools/Invoke-WireGuardMilestone.ps1")
}
