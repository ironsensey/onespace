//go:build !windows

package clipboard

import "errors"

// Changes is available only on Windows; callers can retain their polling fallback.
func Changes(stop <-chan struct{}) (<-chan struct{}, error) {
	return nil, errors.New("clipboard change notifications require Windows")
}
