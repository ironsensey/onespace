//go:build integration && linux

package wireguard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in root test. Two network namespaces prevent local-route bypass
// between the peers. Missing prerequisites FAIL rather than masquerading as proof.
func TestWireGuardHTTPSAndRevocation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("requires root for disposable network namespaces")
	}
	binary := os.Getenv("ONESPACE_WGTEST_BINARY")
	if binary == "" {
		t.Fatal("set ONESPACE_WGTEST_BINARY to the built native wgtest")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", args[0], err, out)
		}
		return string(out)
	}
	nsA := fmt.Sprintf("oswg-a-%d", os.Getpid())
	nsB := fmt.Sprintf("oswg-b-%d", os.Getpid())
	for _, ns := range []string{nsA, nsB} {
		run("ip", "netns", "add", ns)
		t.Cleanup(func() {
			if out, err := exec.Command("ip", "netns", "delete", ns).CombinedOutput(); err != nil {
				t.Errorf("namespace cleanup: %v: %s", err, out)
			}
		})
		run("ip", "-n", ns, "link", "set", "lo", "up")
	}
	run("ip", "-n", nsA, "link", "add", "transport", "type", "veth", "peer", "name", "transport", "netns", nsB)
	run("ip", "-n", nsA, "addr", "add", "192.0.2.1/30", "dev", "transport")
	run("ip", "-n", nsB, "addr", "add", "192.0.2.2/30", "dev", "transport")
	run("ip", "-n", nsA, "link", "set", "transport", "up")
	run("ip", "-n", nsB, "link", "set", "transport", "up")
	dir := t.TempDir()
	a := filepath.Join(dir, "a.key")
	b := filepath.Join(dir, "b.key")
	cert := filepath.Join(dir, "server.pem")
	tlsKey := filepath.Join(dir, "tls.key")
	revoke := filepath.Join(dir, "revoke")
	public := func(output string) string {
		t.Helper()
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "PUBLIC_KEY=") {
				return strings.TrimPrefix(line, "PUBLIC_KEY=")
			}
		}
		t.Fatal("no public key")
		return ""
	}
	aPub := public(run(binary, "-mode", "init-client", "-key-file", a))
	bPub := public(run(binary, "-mode", "init-server", "-key-file", b, "-cert-file", cert, "-tls-key-file", tlsKey))
	logPath := filepath.Join(dir, "server.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	server := exec.CommandContext(ctx, "ip", "netns", "exec", nsB, binary, "-mode", "server", "-key-file", b, "-cert-file", cert, "-tls-key-file", tlsKey, "-overlay-ip", "10.200.200.2/32", "-peer-overlay-ip", "10.200.200.1", "-peer-ip", "192.0.2.1", "-peer-key", aPub, "-revoke-file", revoke, "-duration", "55s")
	server.Stdout = log
	server.Stderr = log
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
	waitLog := func(needle string) {
		t.Helper()
		timer := time.NewTimer(8 * time.Second)
		defer timer.Stop()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), needle) {
				return
			}
			select {
			case <-timer.C:
				t.Fatalf("missing %s: %s", needle, data)
			case <-ticker.C:
			}
		}
	}
	waitLog("HTTPS_LISTENING=")
	args := []string{"netns", "exec", nsA, binary, "-mode", "client", "-key-file", a, "-ca-cert", cert, "-overlay-ip", "10.200.200.1/32", "-peer-overlay-ip", "10.200.200.2", "-peer-ip", "192.0.2.2", "-peer-key", bPub, "-enroll-timeout", "3s"}
	output := run(append([]string{"ip"}, args...)...)
	if !strings.Contains(output, "tls_verified=true") {
		t.Fatal("no verified HTTPS", output)
	}
	t.Log(output)
	if err = os.WriteFile(revoke, []byte("revoke test peer\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitLog("PEER_REVOKED=")
	outputBytes, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	if err == nil || !strings.Contains(string(outputBytes), "REQUEST_FAILED") {
		t.Fatalf("fresh revoked connection did not fail: %v %s", err, outputBytes)
	}
	// Check the very same server remains reachable from its own namespace, with TLS validation.
	health := run("ip", "netns", "exec", nsB, "curl", "--noproxy", "*", "--fail", "--silent", "--show-error", "--max-time", "3", "--cacert", cert, "https://10.200.200.2:8443/")
	if health != "ok" {
		t.Fatal("server unhealthy after revocation", health)
	}
	t.Log("fresh revoked connection failed; same HTTPS server still returns verified ok")
}
