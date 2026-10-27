package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type bufferedTunnel struct {
	net.Conn
	reader *bufio.Reader
	closed chan struct{}
	once   sync.Once
}

func (c *bufferedTunnel) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *bufferedTunnel) Close() error               { c.once.Do(func() { close(c.closed) }); return c.Conn.Close() }

type tunnelListener struct {
	conn   net.Conn
	used   bool
	closed chan struct{}
}

func (l *tunnelListener) Accept() (net.Conn, error) {
	if !l.used {
		l.used = true
		return l.conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}
func (l *tunnelListener) Close() error   { return l.conn.Close() }
func (l *tunnelListener) Addr() net.Addr { return l.conn.LocalAddr() }

func connectHub(ctx context.Context, c HubConfig) (*bufferedTunnel, error) {
	cli, err := newPinnedHubClient(c)
	if err != nil {
		return nil, err
	}
	transport := cli.Transport.(*http.Transport)
	u, err := url.Parse(c.Address)
	if err != nil {
		return nil, err
	}
	address := u.Host
	if u.Port() == "" {
		address = net.JoinHostPort(u.Hostname(), "443")
	}
	raw, err := transport.DialTLSContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	stopClose := context.AfterFunc(ctx, func() { raw.Close() })
	defer stopClose()
	success := false
	defer func() {
		if !success {
			raw.Close()
		}
	}()
	_ = raw.SetDeadline(time.Now().Add(8 * time.Second))
	req, err := http.NewRequest("POST", strings.TrimRight(c.Address, "/")+"/hub/v1/connect/listen", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "onespace-connect-v1")
	if err = req.Write(raw); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(raw, 16<<10)
	response, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 101 || response.Header.Get("Upgrade") != "onespace-connect-v1" {
		return nil, fmt.Errorf("hub connect denied (HTTP %d)", response.StatusCode)
	}
	_ = raw.SetDeadline(time.Now().Add(16 * time.Minute))
	marker := make([]byte, 1)
	if _, err = io.ReadFull(reader, marker); err != nil {
		return nil, err
	}
	if marker[0] != 1 {
		return nil, errors.New("invalid relay pairing marker")
	}
	_ = raw.SetDeadline(time.Time{})
	success = true
	return &bufferedTunnel{Conn: raw, reader: reader, closed: make(chan struct{})}, nil
}

func (b *HubBridge) connectLoop(ctx context.Context, c HubConfig) {
	retry := time.Second
	for ctx.Err() == nil {
		conn, err := connectHub(ctx, c)
		if err == nil {
			retry = time.Second
			connectionCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				select {
				case <-connectionCtx.Done():
					conn.Close()
				case <-conn.closed:
				}
			}()
			// Authenticate the original PC TLS identity inside the hub tunnel. Existing
			// PC bearer permissions remain mandatory, even for an enrolled hub device.
			secure := tls.Server(conn, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{b.app.pcCertificate}})
			listener := &tunnelListener{conn: secure, closed: conn.closed}
			server := &http.Server{Handler: http.HandlerFunc(b.app.remote), ReadHeaderTimeout: 65 * time.Second, IdleTimeout: 65 * time.Second, MaxHeaderBytes: 1 << 16}
			err = server.Serve(listener)
			conn.Close()
			cancel()
			<-done
			if errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
		if retry < 30*time.Second {
			retry *= 2
			if retry > 30*time.Second {
				retry = 30 * time.Second
			}
		}
	}
}
