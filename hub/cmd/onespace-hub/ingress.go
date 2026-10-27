package main

import (
	"net"
	"sync"
)

// Public ingress is opt-in and bounded before TLS. Keep the OS firewall enabled.
type ingressListener struct {
	net.Listener
	allowPublic bool
	mu          sync.Mutex
	total       int
	peers       map[string]int
}
type countedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *countedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
func (l *ingressListener) Accept() (net.Conn, error) {
	if !l.allowPublic {
		return (privateListener{l.Listener}).Accept()
	}
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		host, _, err := net.SplitHostPort(c.RemoteAddr().String())
		if err != nil {
			c.Close()
			continue
		}
		l.mu.Lock()
		if l.peers == nil {
			l.peers = map[string]int{}
		}
		if l.total >= 128 || l.peers[host] >= 16 {
			l.mu.Unlock()
			c.Close()
			continue
		}
		l.total++
		l.peers[host]++
		l.mu.Unlock()
		return &countedConn{Conn: c, release: func() {
			l.mu.Lock()
			l.total--
			l.peers[host]--
			if l.peers[host] == 0 {
				delete(l.peers, host)
			}
			l.mu.Unlock()
		}}, nil
	}
}
