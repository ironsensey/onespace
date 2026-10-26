package main

import (
	"log"
	"os"
	"path/filepath"
	"sync"
)

// GUI builds retain at most two 1MiB diagnostic files, never clipboard payloads.
type profileLogWriter struct {
	sync.Mutex
	file *os.File
	path string
	size int64
}

func configureProfileLog(root string) (*profileLogWriter, error) {
	path := filepath.Join(root, "onespace.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	writer := &profileLogWriter{file: f, path: path, size: info.Size()}
	log.SetOutput(writer)
	return writer, nil
}
func (w *profileLogWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	original := len(p)
	if len(p) > 1<<20 {
		p = p[:1<<20]
	}
	if w.size+int64(len(p)) > 1<<20 {
		if err := w.file.Close(); err != nil {
			return 0, err
		}
		_ = os.Remove(w.path + ".old")
		if err := os.Rename(w.path, w.path+".old"); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return 0, err
		}
		w.file = f
		w.size = 0
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	if err == nil {
		return original, nil
	}
	return n, err
}
func (w *profileLogWriter) Close() error { w.Lock(); defer w.Unlock(); return w.file.Close() }
