package coordination

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type WHEPBridge struct {
	Coordinator Client
	Device      string
	Upstream    string
	HTTP        *http.Client
	mu          sync.Mutex
	watchers    map[string]context.CancelFunc
}

// ServeRequest handles one authorized viewer request on the Pi. It consumes
// the viewer's SDP offer from the coordinator, posts it to the fixed local
// MediaMTX WHEP endpoint, and returns the answer through the coordinator.
// Media never passes through this bridge.
func (b *WHEPBridge) ServeRequest(ctx context.Context, request createResponse) error {
	if request.Target != b.Device || request.Viewer == "" || request.Session == "" || !validUpstream(b.Upstream) {
		return errors.New("invalid WHEP bridge request")
	}
	messages, err := b.Coordinator.Poll(ctx, request.Session, b.Device)
	if err != nil {
		return err
	}
	var offer string
	for _, message := range messages {
		if message.Type == "offer" && message.From == request.Viewer {
			offer = message.Body
			break
		}
	}
	if offer == "" || len(offer) > maxMessage {
		return errors.New("WHEP offer missing or oversized")
	}
	client := b.HTTP
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, b.Upstream, bytes.NewReader([]byte(offer)))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/sdp")
	response, err := client.Do(httpRequest)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return errors.New("MediaMTX rejected WHEP offer")
	}
	endpoint, err := b.sessionURL(response.Header.Get("Location"))
	if err != nil {
		return err
	}
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxMessage+1))
	if err != nil || len(answer) == 0 || len(answer) > maxMessage {
		b.deleteUpstream(ctx, endpoint)
		return errors.New("invalid MediaMTX WHEP answer")
	}
	if err := b.Coordinator.Send(ctx, request.Session, b.Device, "answer", string(answer)); err != nil {
		b.deleteUpstream(ctx, endpoint)
		return err
	}
	if err := b.Coordinator.AcknowledgeRequest(ctx, request.Session); err != nil {
		b.deleteUpstream(ctx, endpoint)
		return err
	}
	b.watchSession(request.Session, endpoint)
	return nil
}

func (b *WHEPBridge) watchSession(id, endpoint string) {
	b.mu.Lock()
	if b.watchers == nil {
		b.watchers = make(map[string]context.CancelFunc)
	}
	if cancel := b.watchers[id]; cancel != nil {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.watchers[id] = cancel
	b.mu.Unlock()
	go func() {
		for {
			messages, err := b.Coordinator.Poll(ctx, id, b.Device)
			if err != nil {
				b.deleteUpstream(ctx, endpoint)
				b.finishWatcher(id)
				return
			}
			for _, message := range messages {
				if message.Type == "close" {
					b.deleteUpstream(ctx, endpoint)
					_ = b.Coordinator.Close(context.Background(), id, b.Device)
					b.finishWatcher(id)
					return
				}
			}
		}
	}()
}

func (b *WHEPBridge) finishWatcher(id string) {
	b.mu.Lock()
	delete(b.watchers, id)
	b.mu.Unlock()
}

func (b *WHEPBridge) Close(ctx context.Context, id string) {
	b.mu.Lock()
	cancel := b.watchers[id]
	delete(b.watchers, id)
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = b.Coordinator.Close(ctx, id, b.Device)
}

func validUpstream(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasSuffix(u.Path, "/whep") {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func (b *WHEPBridge) sessionURL(location string) (string, error) {
	base, _ := url.Parse(b.Upstream)
	raw, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	u := base.ResolveReference(raw)
	prefix := strings.TrimSuffix(base.Path, "/") + "/"
	if u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, prefix) {
		return "", errors.New("untrusted MediaMTX session location")
	}
	id := strings.TrimPrefix(u.Path, prefix)
	if id == "" || strings.Contains(id, "/") || len(id) > 128 {
		return "", errors.New("invalid MediaMTX session location")
	}
	return u.String(), nil
}

func (b *WHEPBridge) deleteUpstream(ctx context.Context, endpoint string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return
	}
	client := b.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if response, err := client.Do(req); err == nil {
		response.Body.Close()
	}
}
