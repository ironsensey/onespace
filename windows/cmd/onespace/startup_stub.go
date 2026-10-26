//go:build !windows

package main

import "errors"

func startupEnabled() bool  { return false }
func setStartup(bool) error { return errors.New("Windows-only feature") }
