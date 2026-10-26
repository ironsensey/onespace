//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

func startupEnabled() bool {
	return exec.Command("reg.exe", "query", runKey, "/v", "OneSpace").Run() == nil
}
func setStartup(enable bool) error {
	if !enable {
		out, err := exec.Command("reg.exe", "delete", runKey, "/v", "OneSpace", "/f").CombinedOutput()
		if err != nil && !strings.Contains(string(out), "unable to find") {
			return fmt.Errorf("remove startup: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	value := `"` + exe + `" -no-browser`
	out, err := exec.Command("reg.exe", "add", runKey, "/v", "OneSpace", "/t", "REG_SZ", "/d", value, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("enable startup: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
