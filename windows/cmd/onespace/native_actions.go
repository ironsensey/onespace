package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
)

// The native UI dispatches in-process through the same authorization, validation
// and persistence as the existing local UI, with no credential bootstrap file.
func (a *App) nativeAction(path string, body map[string]any) (int, string) {
	switch path {
	case "/local/clipboard", "/local/password", "/local/pair/decision":
	default:
		return 403, "Unsupported native action"
	}
	data, err := json.Marshal(body)
	if err != nil {
		return 400, "Invalid action"
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1:47600"+path, bytes.NewReader(data))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Local-Key", a.s.deskToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.local(rec, req)
	return rec.Code, rec.Body.String()
}
