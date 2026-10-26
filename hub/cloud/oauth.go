package cloud

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Connect is a local installed-app OAuth flow with PKCE and a single-use
// loopback callback. The hub must be stopped; its lifetime lock is held by CLI.
// The public URL may be opened on another PC using an SSH loopback tunnel.
func Connect(ctx context.Context, clientFile, output string, port int, printURL func(string)) error {
	b, err := os.ReadFile(clientFile)
	if err != nil {
		return err
	}
	if len(b) > 32768 {
		return errors.New("OAuth client file exceeds 32 KiB")
	}
	var config struct {
		Installed struct {
			ClientID string `json:"client_id"`
			Secret   string `json:"client_secret"`
		} `json:"installed"`
	}
	if json.Unmarshal(b, &config) != nil || !strings.HasSuffix(config.Installed.ClientID, ".apps.googleusercontent.com") {
		return errors.New("use a Google OAuth Desktop app client JSON, not a web or service-account key")
	}
	random := make([]byte, 64)
	if _, err = rand.Read(random); err != nil {
		return err
	}
	state := base64.RawURLEncoding.EncodeToString(random[:32])
	verifier := base64.RawURLEncoding.EncodeToString(random[32:])
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if port < 1024 || port > 65535 {
		return errors.New("OAuth callback port must be between 1024 and 65535")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer listener.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	code := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		if r.Method != "GET" || r.Host != fmt.Sprintf("127.0.0.1:%d", port) || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid sign-in callback", 400)
			return
		}
		value := r.URL.Query().Get("code")
		if value == "" || len(value) > 8192 {
			http.Error(w, "Google sign-in was not completed", 400)
			return
		}
		select {
		case code <- value:
			fmt.Fprintln(w, "Google authorization received. Check the OneSpace terminal for completion, then close this tab.")
		default:
			http.Error(w, "Sign-in callback already received", 409)
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	v := url.Values{"client_id": {config.Installed.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {Scope}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "access_type": {"offline"}, "prompt": {"consent"}}
	printURL("https://accounts.google.com/o/oauth2/v2/auth?" + v.Encode())
	var authorization string
	select {
	case authorization = <-code:
	case <-ctx.Done():
		return errors.New("Google sign-in timed out or was cancelled")
	}
	v = url.Values{"client_id": {config.Installed.ClientID}, "client_secret": {config.Installed.Secret}, "code": {authorization}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "grant_type": {"authorization_code"}}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("Google token exchange unavailable")
	}
	defer res.Body.Close()
	var token struct {
		Refresh string `json:"refresh_token"`
		Scope   string `json:"scope"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 32768)).Decode(&token) != nil || token.Refresh == "" {
		return errors.New("Google did not issue an offline credential; check OAuth Desktop app configuration and consent")
	}
	// Only drive.file was requested. Reject a result that lacks that grant.
	granted := false
	for _, s := range strings.Fields(token.Scope) {
		if s == Scope {
			granted = true
		}
	}
	if !granted {
		return errors.New("Drive file access was not granted")
	}
	return atomicJSON(output, Credentials{ClientID: config.Installed.ClientID, ClientSecret: config.Installed.Secret, RefreshToken: token.Refresh})
}

// Disconnect revokes the grant before forgetting the local credential. A
// failed remote revoke leaves the credential intact for a deliberate retry.
func (d *Drive) Disconnect(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.credentials.RefreshToken == "" {
		return nil
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/revoke", strings.NewReader(url.Values{"token": {d.credentials.RefreshToken}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := d.client.Do(req)
	if err != nil {
		return errors.New("Google revoke unavailable; retry locally or revoke OneSpace in your Google Account")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("Google revoke failed; revoke OneSpace in your Google Account before removing local credentials")
	}
	if err = os.Remove(d.credentialPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	d.credentials = Credentials{}
	d.hasCredential.Store(false)
	d.access = ""
	return nil
}
