//go:build linux

package cloud

import (
	"os"
	"syscall"
)

func diskFree(path string) (uint64, error) {
	var s syscall.Statfs_t
	err := syscall.Statfs(path, &s)
	return s.Bavail * uint64(s.Bsize), err
}

// A separate, small filesystem gives a hard cap even if the hub crashes.
func boundedBuffer(spool, data string) bool {
	s, e := os.Stat(spool)
	if e != nil {
		return false
	}
	p, e := os.Stat(data)
	if e != nil {
		return false
	}
	a, ok := s.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	b, ok := p.Sys().(*syscall.Stat_t)
	if !ok || a.Dev == b.Dev {
		return false
	}
	var fs syscall.Statfs_t
	if syscall.Statfs(spool, &fs) != nil {
		return false
	}
	size := fs.Blocks * uint64(fs.Bsize)
	return size >= 64<<20 && size <= 256<<20
}
