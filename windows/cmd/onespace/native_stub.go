//go:build !windows

package main

import "errors"

func openNativeDashboard(*App) error { return errors.New("native dashboard requires Windows") }
