//go:build windows

package main

import (
	"errors"
	"runtime"
	"syscall"
)

func performMediaAction(action string) error {
	keys := map[string]uintptr{"play_pause": 0xB3, "next": 0xB0, "previous": 0xB1, "stop": 0xB2}
	key, ok := keys[action]
	if !ok {
		return errors.New("Unsupported media action")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	proc := syscall.NewLazyDLL("user32.dll").NewProc("keybd_event")
	proc.Call(key, 0, 0, 0)
	proc.Call(key, 0, 2, 0)
	return nil
}
