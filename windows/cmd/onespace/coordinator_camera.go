//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type coordinatorCamera struct {
	base, token, device, target string
	client                      *http.Client
}

type coordinatorSession struct {
	Session string `json:"session"`
	Viewer  string `json:"viewer"`
	Target  string `json:"target"`
}

type coordinatorMessage struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	From string `json:"from"`
	Body string `json:"body"`
}

func newCoordinatorCamera(c HubConfig) (*coordinatorCamera, error) {
	if c.CoordinatorAddress == "" {
		return nil, errors.New("remote camera coordinator is not configured")
	}
	pin, err := hex.DecodeString(strings.ReplaceAll(strings.ReplaceAll(c.CoordinatorFingerprint, ":", ""), " ", ""))
	if err != nil || len(pin) != sha256.Size {
		return nil, errors.New("invalid coordinator certificate fingerprint")
	}
	base := strings.TrimRight(c.CoordinatorAddress, "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid coordinator endpoint")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12, InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("coordinator supplied no certificate")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(pin, sum[:]) != 1 {
				return errors.New("coordinator certificate identity mismatch")
			}
			return nil
		},
	}}
	return &coordinatorCamera{base: base, token: c.CoordinatorToken, device: c.CoordinatorDevice, target: c.CoordinatorTarget, client: &http.Client{Transport: transport, Timeout: 65 * time.Second}}, nil
}

func (c *coordinatorCamera) request(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("coordinator request rejected")
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 512<<10)).Decode(out)
	}
	return nil
}

func (c *coordinatorCamera) create(ctx context.Context) (coordinatorSession, error) {
	var out coordinatorSession
	err := c.request(ctx, http.MethodPost, "/v1/coord/sessions", map[string]string{"viewer": c.device, "target": c.target}, &out)
	return out, err
}
func (c *coordinatorCamera) send(ctx context.Context, id, kind, body string) error {
	return c.request(ctx, http.MethodPost, "/v1/coord/sessions/"+url.PathEscape(id)+"/messages?from="+url.QueryEscape(c.device), map[string]string{"type": kind, "body": body}, nil)
}
func (c *coordinatorCamera) poll(ctx context.Context, id string) ([]coordinatorMessage, error) {
	var out []coordinatorMessage
	err := c.request(ctx, http.MethodGet, "/v1/coord/sessions/"+url.PathEscape(id)+"/poll?for="+url.QueryEscape(c.device), nil, &out)
	return out, err
}
func (c *coordinatorCamera) close(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/v1/coord/sessions/"+url.PathEscape(id)+"/close?by="+url.QueryEscape(c.device), nil, nil)
}
