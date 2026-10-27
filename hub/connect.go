package hub

// Connect is a bounded reverse TCP rendezvous over pinned HTTP/1.1 TLS.
// It carries a second, end-to-end PC TLS connection. The hub never selects
// arbitrary hostnames/ports and cannot authenticate as the paired PC.
import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const ConnectProtocol = "onespace-connect-v1"
const maxConnectPerDevice = 10

type connectPeer struct {
	conn    net.Conn
	reader  *bufio.Reader
	request *http.Request
	paired  chan struct{}
	done    chan struct{}
}

type idleConn struct{ net.Conn }

func (c idleConn) Read(p []byte) (int, error) {
	_ = c.SetReadDeadline(time.Now().Add(90 * time.Second))
	return c.Conn.Read(p)
}
func (c idleConn) Write(p []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(90 * time.Second))
	return c.Conn.Write(p)
}

func (h *Hub) connect(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	d, ok := h.Authorize(r, "connect")
	if !ok {
		fail(w, 403, "connect permission required")
		return
	}
	if r.ProtoMajor != 1 || !strings.EqualFold(r.Header.Get("Upgrade"), ConnectProtocol) || !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		fail(w, 400, "HTTP/1.1 upgrade without body required")
		return
	}
	listening := r.URL.Path == "/hub/v1/connect/listen"
	if !listening && r.URL.Path != "/hub/v1/connect/open" {
		fail(w, 404, "unknown connect operation")
		return
	}
	target := r.URL.Query().Get("to")
	h.mu.Lock()
	if h.closed || h.connectCount[d.ID] >= maxConnectPerDevice || h.connectTotal >= 32 {
		h.mu.Unlock()
		fail(w, 429, "connect capacity reached")
		return
	}
	var peer *connectPeer
	if !listening {
		record, exists := h.state.Devices[target]
		if !exists || record.Revoked || !record.Scopes["connect"] || target == d.ID {
			h.mu.Unlock()
			fail(w, 403, "target unavailable or forbidden")
			return
		}
		queue := h.connectWaiting[target]
		if len(queue) == 0 {
			h.mu.Unlock()
			fail(w, 503, "target is not accepting connections")
			return
		}
		peer = queue[0]
		h.connectWaiting[target] = queue[1:]
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		if peer != nil {
			h.connectWaiting[target] = append([]*connectPeer{peer}, h.connectWaiting[target]...)
		}
		h.mu.Unlock()
		fail(w, 500, "upgrade unavailable")
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		if peer != nil {
			h.connectWaiting[target] = append([]*connectPeer{peer}, h.connectWaiting[target]...)
		}
		h.mu.Unlock()
		return
	}
	h.connectCount[d.ID]++
	h.connectTotal++
	h.mu.Unlock()
	defer func() { conn.Close(); h.mu.Lock(); h.connectCount[d.ID]--; h.connectTotal--; h.mu.Unlock() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", ConnectProtocol); err == nil {
		err = rw.Flush()
	}
	if err != nil {
		if peer != nil {
			peer.conn.Close()
			close(peer.paired)
			close(peer.done)
		}
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if listening {
		p := &connectPeer{conn: conn, reader: rw.Reader, request: r, paired: make(chan struct{}), done: make(chan struct{})}
		h.mu.Lock()
		h.connectWaiting[d.ID] = append([]*connectPeer{p}, h.connectWaiting[d.ID]...)
		h.mu.Unlock()
		defer func() {
			h.mu.Lock()
			q := h.connectWaiting[d.ID]
			for i, v := range q {
				if v == p {
					h.connectWaiting[d.ID] = append(q[:i], q[i+1:]...)
					break
				}
			}
			h.mu.Unlock()
		}()
		timer := time.NewTimer(15 * time.Minute)
		defer timer.Stop()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-p.paired:
				select {
				case <-p.done:
				case <-h.done:
				}
				return
			case <-h.done:
				return
			case <-timer.C:
				return
			case <-ticker.C:
				if _, ok := h.CheckPermission(r, "connect"); !ok {
					return
				}
			}
		}
	}
	defer close(peer.done)
	close(peer.paired)
	_ = peer.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := peer.conn.Write([]byte{1}); err != nil {
		return
	}
	_ = peer.conn.SetWriteDeadline(time.Time{})
	defer peer.conn.Close()
	// Both copy goroutines are joined before releasing accounting slots.
	finished := make(chan struct{}, 2)
	copyStream := func(dst net.Conn, src io.Reader) {
		_, _ = io.CopyBuffer(idleConn{dst}, src, make([]byte, 32<<10))
		finished <- struct{}{}
	}
	// Preserve any bytes read ahead during HTTP parsing, then use sliding timeouts.
	incoming := io.MultiReader(io.LimitReader(rw.Reader, int64(rw.Reader.Buffered())), idleConn{conn})
	outgoing := io.MultiReader(io.LimitReader(peer.reader, int64(peer.reader.Buffered())), idleConn{peer.conn})
	go copyStream(peer.conn, incoming)
	go copyStream(conn, outgoing)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	consumed := false
loop:
	for {
		select {
		case <-finished:
			consumed = true
			break loop
		case <-h.done:
			break loop
		case <-timer.C:
			break loop
		case <-ticker.C:
			_, senderOK := h.CheckPermission(r, "connect")
			_, targetOK := h.CheckPermission(peer.request, "connect")
			if !senderOK || !targetOK {
				break loop
			}
		}
	}
	conn.Close()
	peer.conn.Close()
	if !consumed {
		<-finished
	}
	<-finished
}
