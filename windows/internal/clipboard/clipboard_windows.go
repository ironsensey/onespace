//go:build windows

package clipboard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	cfUnicodeText = 13
	cfDIB         = 8
	gmemMoveable  = 0x0002
)

var (
	user32       = syscall.NewLazyDLL("user32.dll")
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	openClip     = user32.NewProc("OpenClipboard")
	closeClip    = user32.NewProc("CloseClipboard")
	emptyClip    = user32.NewProc("EmptyClipboard")
	setClip      = user32.NewProc("SetClipboardData")
	getClip      = user32.NewProc("GetClipboardData")
	globalAlloc  = kernel32.NewProc("GlobalAlloc")
	globalLock   = kernel32.NewProc("GlobalLock")
	globalUnlock = kernel32.NewProc("GlobalUnlock")
	globalFree   = kernel32.NewProc("GlobalFree")
	globalSize   = kernel32.NewProc("GlobalSize")
	copyMemory   = kernel32.NewProc("RtlMoveMemory")
)

func withClipboard(f func() error) error { return withClipboardOwner(0, f) }

func withClipboardOwner(owner uintptr, f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for i := 0; i < 12; i++ {
		r, _, _ := openClip.Call(owner)
		if r != 0 {
			defer closeClip.Call()
			return f()
		}
		time.Sleep(35 * time.Millisecond)
	}
	return errors.New("clipboard busy; close another clipboard application and retry")
}

// OpenClipboard(NULL) is valid for reads, but EmptyClipboard then
// SetClipboardData requires an actual owner window on this same OS thread.
func withClipboardWriter(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := syscall.UTF16PtrFromString("STATIC")
	title, _ := syscall.UTF16PtrFromString("OneSpace clipboard owner")
	window, _, err := user32.NewProc("CreateWindowExW").Call(0,
		uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0) // HWND_MESSAGE = -3
	if window == 0 {
		return fmt.Errorf("clipboard owner window: %v", err)
	}
	defer user32.NewProc("DestroyWindow").Call(window)
	return withClipboardOwner(window, f)
}

func put(format uintptr, data []byte) error {
	if len(data) == 0 {
		return errors.New("empty clipboard content")
	}
	h, _, e := globalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return fmt.Errorf("GlobalAlloc: %v", e)
	}
	ptr, _, _ := globalLock.Call(h)
	if ptr == 0 {
		globalFree.Call(h)
		return errors.New("GlobalLock failed")
	}
	copyMemory.Call(ptr, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	globalUnlock.Call(h)
	if r, _, _ := emptyClip.Call(); r == 0 {
		globalFree.Call(h)
		return errors.New("EmptyClipboard failed")
	}
	if r, _, _ := setClip.Call(format, h); r == 0 {
		globalFree.Call(h)
		return errors.New("SetClipboardData failed")
	}
	// Windows owns the memory handle from now on.
	return nil
}
func SetText(s string) error {
	units := utf16.Encode([]rune(s))
	units = append(units, 0)
	b := make([]byte, len(units)*2)
	for i, v := range units {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return withClipboardWriter(func() error { return put(cfUnicodeText, b) })
}
func GetText() (string, error) {
	var out string
	err := withClipboard(func() error {
		h, _, _ := getClip.Call(cfUnicodeText)
		if h == 0 {
			return errors.New("clipboard contains no text")
		}
		ptr, _, _ := globalLock.Call(h)
		if ptr == 0 {
			return errors.New("cannot lock clipboard data")
		}
		defer globalUnlock.Call(h)
		sz, _, _ := globalSize.Call(h)
		if sz == 0 || sz > 8*1024*1024 {
			return errors.New("clipboard text too large")
		}
		n := int(sz / 2)
		if n == 0 {
			return nil
		}
		u := make([]uint16, n)
		copyMemory.Call(uintptr(unsafe.Pointer(&u[0])), ptr, uintptr(n*2))
		end := 0
		for end < len(u) && u[end] != 0 {
			end++
		}
		out = string(utf16.Decode(u[:end]))
		return nil
	})
	return out, err
}
func SetImage(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 40*1024*1024 {
		return errors.New("image too large for clipboard decoding")
	}
	dimensions, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("unsupported image: %w", err)
	}
	if int64(dimensions.Width)*int64(dimensions.Height) > 32_000_000 {
		return errors.New("image dimensions exceed clipboard safety limit")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	im, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("unsupported image: %w", err)
	}
	data, err := imageToDIB(im)
	if err != nil {
		return err
	}
	return withClipboardWriter(func() error { return put(cfDIB, data) })
}
