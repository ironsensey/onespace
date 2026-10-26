//go:build !windows

package clipboard

import "errors"

func Sequence() uint32             { return 0 }
func GetImagePNG() ([]byte, error) { return nil, errors.New("Windows only") }
