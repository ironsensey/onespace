//go:build !linux

package cloud

import "errors"

func diskFree(string) (uint64, error) {
	return 0, errors.New("recording disk checks require the Linux Pi hub")
}

func boundedBuffer(string, string) bool { return false }
