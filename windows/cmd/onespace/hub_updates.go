package main

import (
	"io"
	"net/http"
	"strings"
	"time"
)

func (a *App) proxyHubUpdates(w http.ResponseWriter, r *http.Request, cli *http.Client, c HubConfig, path string) {
	method := r.Method
	remote := "/hub/v1/updates/manifest"
	if path == "/updates/publish" && method == "POST" {
		method = "PUT"
		remote = "/hub/v1/updates/apk"
	} else if path == "/updates/reserve" && method == "POST" {
		remote = "/hub/v1/updates/reserve"
	} else if path != "/updates/manifest" || method != "GET" {
		bad(w, 404, "Unknown update operation")
		return
	}
	if r.ContentLength > maxAPKSize {
		bad(w, 413, "APK exceeds 150 MiB")
		return
	}
	copyClient := *cli
	copyClient.Timeout = 10 * time.Minute
	req, err := http.NewRequestWithContext(r.Context(), method, strings.TrimRight(c.Address, "/")+remote, http.MaxBytesReader(w, r.Body, maxAPKSize))
	if err != nil {
		bad(w, 400, "Invalid update request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = r.ContentLength
	res, err := copyClient.Do(req)
	if err != nil {
		bad(w, 502, "Pi update publishing interrupted; refresh the Pi manifest before retrying")
		return
	}
	defer res.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(res.Body, 1<<20))
}
