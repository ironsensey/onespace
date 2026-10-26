package main

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var cloudResource = regexp.MustCompile(`^/cloud/(files|uploads)/[A-Za-z0-9_-]{8,200}$`)

// The existing local Host/loopback/X-Local-Key gate remains mandatory. Only
// bounded app endpoints are forwarded; credentials and remote session URLs
// remain inside the Go processes.
func (a *App) proxyHubCloud(w http.ResponseWriter, r *http.Request, cli *http.Client, c HubConfig, path string) {
	valid := false
	switch path {
	case "/cloud/status", "/cloud/account":
		valid = r.Method == "GET"
	case "/cloud/recording":
		valid = r.Method == "POST"
	case "/cloud/files":
		valid = r.Method == "GET"
	case "/cloud/uploads":
		valid = r.Method == "POST"
	default:
		if cloudResource.MatchString(path) {
			valid = r.Method == "GET" || r.Method == "DELETE" || (strings.HasPrefix(path, "/cloud/uploads/") && r.Method == "PUT") || (strings.HasPrefix(path, "/cloud/files/") && r.Method == "PATCH")
		}
	}
	if !valid {
		bad(w, 404, "Unknown cloud operation")
		return
	}
	endpoint := strings.TrimRight(c.Address, "/") + "/hub/v1" + path
	if r.URL.RawQuery != "" {
		q, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil || path != "/cloud/files" || len(q) != 1 || len(q["pageToken"]) != 1 || len(q.Get("pageToken")) > 4096 {
			bad(w, 400, "Invalid cloud query")
			return
		}
		endpoint += "?" + q.Encode()
	}
	limit := int64(64 << 10)
	if r.Method == "PUT" {
		limit = 1 << 20
	}
	if r.ContentLength > limit {
		bad(w, 413, "Cloud request exceeds chunk limit")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, endpoint, http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		bad(w, 400, "Invalid cloud request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	req.Header.Set("X-Upload-Offset", r.Header.Get("X-Upload-Offset"))
	res, err := cli.Do(req)
	if err != nil {
		bad(w, 502, "Cloud connection interrupted; refresh upload progress to resume")
		return
	}
	defer res.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(res.Body, 1<<20))
}
