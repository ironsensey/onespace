//go:build !windows

package clipboard

func SetText(s string) error     { return nil }
func GetText() (string, error)   { return "", nil }
func SetImage(path string) error { return nil }
