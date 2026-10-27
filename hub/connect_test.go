package hub

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func upgradeTest(e *testHub, device, path string) (net.Conn, *bufio.Reader, int, error) {
	cfg := e.server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	c, err := tls.Dial("tcp", e.server.Listener.Addr().String(), cfg)
	if err != nil {
		return nil, nil, 0, err
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	req, _ := http.NewRequest("POST", e.server.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+e.tokens[device])
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", ConnectProtocol)
	if err = req.Write(c); err != nil {
		c.Close()
		return nil, nil, 0, err
	}
	reader := bufio.NewReader(c)
	res, err := http.ReadResponse(reader, req)
	if err != nil {
		c.Close()
		return nil, nil, 0, err
	}
	if res.StatusCode != 101 {
		res.Body.Close()
		c.Close()
		return nil, nil, res.StatusCode, nil
	}
	_ = c.SetDeadline(time.Time{})
	return c, reader, res.StatusCode, nil
}
func waitConnect(t *testing.T, e *testHub, n int) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		e.h.mu.Lock()
		v := len(e.h.connectWaiting["pc"])
		e.h.mu.Unlock()
		if v == n {
			return
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("receiver queue did not settle")
}
func connectScopes(t *testing.T, e *testHub) {
	t.Helper()
	for _, id := range []string{"pc", "phone"} {
		if err := e.h.SetScopes(id, []string{"connect"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConnectScopeAndOfflineTarget(t *testing.T) {
	e := newTestHub(t)
	_, _, code, err := upgradeTest(e, "phone", "/hub/v1/connect/open?to=pc")
	if err != nil || code != 403 {
		t.Fatalf("unauthorized: %d %v", code, err)
	}
	connectScopes(t, e)
	_, _, code, err = upgradeTest(e, "phone", "/hub/v1/connect/open?to=pc")
	if err != nil || code != 503 {
		t.Fatalf("offline: %d %v", code, err)
	}
	_, _, code, err = upgradeTest(e, "phone", "/hub/v1/connect/open?to=phone")
	if err != nil || code != 403 {
		t.Fatalf("self target: %d %v", code, err)
	}
}
func TestConnectCarriesIndependentPCIdentityAndEncryptedStream(t *testing.T) {
	e := newTestHub(t)
	connectScopes(t, e)
	pc, _, code, err := upgradeTest(e, "pc", "/hub/v1/connect/listen")
	if err != nil || code != 101 {
		t.Fatalf("listen: %d %v", code, err)
	}
	defer pc.Close()
	waitConnect(t, e, 1)
	phone, _, code, err := upgradeTest(e, "phone", "/hub/v1/connect/open?to=pc")
	if err != nil || code != 101 {
		t.Fatalf("open: %d %v", code, err)
	}
	defer phone.Close()
	marker := make([]byte, 1)
	if _, err := io.ReadFull(pc, marker); err != nil || marker[0] != 1 {
		t.Fatalf("pair marker: %v", err)
	}

	certificate, _, err := Identity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	server := tls.Server(pc, &tls.Config{Certificates: []tls.Certificate{certificate}})
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	client := tls.Client(phone, &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})

	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	completed := make(chan error, 1)
	go func() {
		if err := server.Handshake(); err != nil {
			completed <- err
			return
		}
		buf := make([]byte, 18)
		_, err := io.ReadFull(server, buf)
		if err == nil && string(buf) != "private-PC-message" {
			err = fmt.Errorf("wrong content")
		}
		if err == nil {
			_, err = server.Write(buf)
		}
		completed <- err
	}()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("private-PC-message")); err != nil {
		t.Fatal(err)
	}
	result := make([]byte, 18)
	if _, err := io.ReadFull(client, result); err != nil {
		t.Fatal(err)
	}
	if string(result) != "private-PC-message" {
		t.Fatal("corrupt relay")
	}
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
}
func TestConnectRevocationAndCapacity(t *testing.T) {
	e := newTestHub(t)
	connectScopes(t, e)
	pc, _, _, err := upgradeTest(e, "pc", "/hub/v1/connect/listen")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	waitConnect(t, e, 1)
	phone, _, _, err := upgradeTest(e, "phone", "/hub/v1/connect/open?to=pc")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()
	if err := e.h.Revoke("phone"); err != nil {
		t.Fatal(err)
	}
	_ = phone.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1)
	_, err = phone.Read(buf)
	if err == nil {
		t.Fatal("revoked tunnel stayed open")
	}
	if n, ok := err.(net.Error); ok && n.Timeout() {
		t.Fatal("revocation did not close relay")
	}
	for i := 0; i < maxConnectPerDevice; i++ {
		c, _, code, err := upgradeTest(e, "pc", "/hub/v1/connect/listen")
		if err != nil || code != 101 {
			t.Fatalf("slot %d: %d %v", i, code, err)
		}
		defer c.Close()
		waitConnect(t, e, i+1)
	}
	_, _, code, err := upgradeTest(e, "pc", "/hub/v1/connect/listen")
	if err != nil || code != 429 {
		t.Fatalf("unbounded slots: %d %v", code, err)
	}
}
func TestConnectRejectsBodyAndOrdinaryHTTP(t *testing.T) {
	e := newTestHub(t)
	connectScopes(t, e)
	req, _ := http.NewRequest("POST", e.server.URL+"/hub/v1/connect/listen", strings.NewReader("payload"))
	req.Header.Set("Authorization", "Bearer "+e.tokens["pc"])
	res, err := e.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("accepted malformed upgrade: %d", res.StatusCode)
	}
}
