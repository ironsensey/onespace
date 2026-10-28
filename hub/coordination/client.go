package coordination

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (c Client) request(ctx context.Context, method, path string, input, output any) error {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 65 * time.Second}
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("coordination request rejected")
	}
	if output != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxMessage+8192)).Decode(output); err != nil {
			return err
		}
	}
	return nil
}

func (c Client) Create(ctx context.Context, viewer, target string) (createResponse, error) {
	var response createResponse
	err := c.request(ctx, http.MethodPost, prefix+"sessions", createRequest{Viewer: viewer, Target: target}, &response)
	return response, err
}

func (c Client) Requests(ctx context.Context) ([]createResponse, error) {
	var response []createResponse
	err := c.request(ctx, http.MethodGet, prefix+"requests", nil, &response)
	return response, err
}

func (c Client) AcknowledgeRequest(ctx context.Context, id string) error {
	if id == "" || strings.ContainsAny(id, "/?#\\") {
		return errors.New("invalid coordination request")
	}
	return c.request(ctx, http.MethodPost, prefix+"requests/"+id, nil, nil)
}

func (c Client) Send(ctx context.Context, id, sender, kind, body string) error {
	return c.request(ctx, http.MethodPost, prefix+"sessions/"+id+"/messages?from="+sender, messageRequest{Type: kind, Body: body}, nil)
}

func (c Client) Poll(ctx context.Context, id, recipient string) ([]Message, error) {
	var response []Message
	err := c.request(ctx, http.MethodGet, prefix+"sessions/"+id+"/poll?for="+recipient, nil, &response)
	return response, err
}

func (c Client) Close(ctx context.Context, id, device string) error {
	return c.request(ctx, http.MethodDelete, prefix+"sessions/"+id+"/close?by="+device, nil, nil)
}
