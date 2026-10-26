//go:build !windows

package main

import "encoding/base64"

// Non-Windows test/development hosts rely on the 0600 profile file; not an OS vault.
func protectHubToken(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func unprotectHubToken(s string) (string, error) {
	v, e := base64.StdEncoding.DecodeString(s)
	return string(v), e
}
