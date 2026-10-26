//go:build !linux

package hub

import (
	"errors"
	"os"
)

func lockState(string) (*os.File, error) {
	return nil, errors.New("hub server requires Linux; cross-build with GOOS=linux GOARCH=arm64")
}
