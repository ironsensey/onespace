//go:build linux

package hub

import (
	"fmt"
	"os"
	"syscall"
)

func lockState(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("hub state is in use; stop the service before local provisioning: %w", err)
	}
	return f, nil
}
