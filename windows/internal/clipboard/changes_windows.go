//go:build windows

package clipboard

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmClipboardUpdate        = 0x031D
	wmCloseClipboard         = 0x0010
	wmDestroyClipboardWindow = 0x0002
	wmQuitClipboard          = 0x0012
)

type clipboardWindowClass struct {
	Style       uint32
	WindowProc  uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    uintptr
	Icon        uintptr
	Cursor      uintptr
	Background  uintptr
	MenuName    *uint16
	ClassName   *uint16
}

type clipboardWindowMessage struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PointX  int32
	PointY  int32
	Private uint32
}

var (
	clipboardChangesWindows      sync.Map // HWND -> chan struct{}; callbacks never perform clipboard I/O.
	clipboardChangesClassOnce    sync.Once
	clipboardChangesClassErr     error
	clipboardChangesClassName, _ = syscall.UTF16PtrFromString("OneSpaceClipboardChangesV1")
	clipboardChangesWindowProc   = syscall.NewCallback(func(window uintptr, message uint32, wParam, lParam uintptr) uintptr {
		switch message {
		case wmClipboardUpdate:
			if value, ok := clipboardChangesWindows.Load(window); ok {
				select {
				case value.(chan struct{}) <- struct{}{}:
				default:
				}
			}
			return 0
		case wmCloseClipboard:
			user32.NewProc("DestroyWindow").Call(window)
			return 0
		case wmDestroyClipboardWindow:
			user32.NewProc("PostQuitMessage").Call(0)
			return 0
		}
		result, _, _ := user32.NewProc("DefWindowProcW").Call(window, uintptr(message), wParam, lParam)
		return result
	})
)

// Changes registers an event-driven Windows clipboard listener on one dedicated
// OS thread. The buffered channel coalesces rapid changes; callers should read
// the current clipboard sequence, rather than treating each wake as a payload.
// The channel closes if the window/message loop exits. Callers must disable a
// closed channel and restore their bounded polling fallback.
func Changes(stop <-chan struct{}) (<-chan struct{}, error) {
	type startupResult struct {
		updates <-chan struct{}
		err     error
	}
	ready := make(chan startupResult, 1)
	cancel := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := user32.NewProc("AddClipboardFormatListener").Find(); err != nil {
			ready <- startupResult{err: err}
			return
		}
		instance, _, err := kernel32.NewProc("GetModuleHandleW").Call(0)
		if instance == 0 {
			ready <- startupResult{err: fmt.Errorf("clipboard listener module: %v", err)}
			return
		}
		clipboardChangesClassOnce.Do(func() {
			class := clipboardWindowClass{WindowProc: clipboardChangesWindowProc, Instance: instance, ClassName: clipboardChangesClassName}
			atom, _, err := user32.NewProc("RegisterClassW").Call(uintptr(unsafe.Pointer(&class)))
			if atom == 0 {
				clipboardChangesClassErr = fmt.Errorf("register clipboard listener window: %v", err)
			}
		})
		if clipboardChangesClassErr != nil {
			ready <- startupResult{err: clipboardChangesClassErr}
			return
		}
		window, _, err := user32.NewProc("CreateWindowExW").Call(0,
			uintptr(unsafe.Pointer(clipboardChangesClassName)), 0, 0, 0, 0, 0, 0,
			^uintptr(2), 0, instance, 0) // HWND_MESSAGE = -3
		if window == 0 {
			ready <- startupResult{err: fmt.Errorf("create clipboard listener window: %v", err)}
			return
		}
		defer user32.NewProc("DestroyWindow").Call(window)
		updates := make(chan struct{}, 1)
		clipboardChangesWindows.Store(window, updates)
		defer func() { clipboardChangesWindows.Delete(window); close(updates) }()
		added, _, err := user32.NewProc("AddClipboardFormatListener").Call(window)
		if added == 0 {
			ready <- startupResult{err: fmt.Errorf("listen for clipboard changes: %v", err)}
			return
		}
		defer user32.NewProc("RemoveClipboardFormatListener").Call(window)
		finished := make(chan struct{})
		defer close(finished)
		threadID, _, _ := kernel32.NewProc("GetCurrentThreadId").Call()
		go func() {
			select {
			case <-stop:
			case <-cancel:
			case <-finished:
				return
			}
			// A posted close executes destruction on the window's owning thread.
			posted, _, _ := user32.NewProc("PostMessageW").Call(window, wmCloseClipboard, 0, 0)
			if posted == 0 {
				user32.NewProc("PostThreadMessageW").Call(threadID, wmQuitClipboard, 0, 0)
			}
		}()
		ready <- startupResult{updates: updates}
		var message clipboardWindowMessage
		for {
			result, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if result == 0 || int32(result) == -1 {
				return
			}
			user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
			user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
	select {
	case result := <-ready:
		return result.updates, result.err
	case <-time.After(time.Second):
		close(cancel)
		return nil, errors.New("clipboard listener startup timed out; use polling fallback")
	}
}
