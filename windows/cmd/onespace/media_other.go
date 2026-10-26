//go:build !windows

package main

import "errors"

func performMediaAction(string) error { return errors.New("Media controls require Windows") }
