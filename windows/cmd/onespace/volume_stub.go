//go:build !windows

package main

import "errors"

func setSystemVolume(percent int) error { return errors.New("Windows only") }
