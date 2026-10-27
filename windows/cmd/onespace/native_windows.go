//go:build windows

package main

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// The preview uses OS controls directly; no browser, HTML engine or new runtime.
// All state changes use the existing state lock and persistence implementation.
func openNativeDashboard(a *App) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	user := syscall.NewLazyDLL("user32.dll")
	kernel := syscall.NewLazyDLL("kernel32.dll")
	create := user.NewProc("CreateWindowExW")
	def := user.NewProc("DefWindowProcW")
	setText := user.NewProc("SetWindowTextW")
	getText := user.NewProc("GetWindowTextW")
	text := syscall.StringToUTF16Ptr
	var output, password, pending uintptr
	var hwnd uintptr
	show := func(message string) { setText.Call(output, uintptr(unsafe.Pointer(text(message)))) }
	refresh := func() {
		a.s.Lock()
		var b strings.Builder
		fmt.Fprintf(&b, "OneSpace %s\r\n\r\nPC identity SHA-256:\r\n%s\r\n\r\nAutomatic clipboard sharing: %t\r\nData directory: %s\r\n\r\nPaired devices:\r\n", appVersion, a.fingerprint, a.s.Data.AutoClipboard, a.s.root)
		for _, d := range a.s.Data.Devices {
			fmt.Fprintf(&b, "%s  [%s]\r\n", d.Name, d.ID)
		}
		b.WriteString("\r\nPending pairing requests (copy the ID to approve/reject):\r\n")
		for _, p := range a.s.Pending {
			if !p.Approved && !p.Rejected && time.Since(p.At) <= 5*time.Minute {
				fmt.Fprintf(&b, "%s  [%s]\r\n", p.Name, p.ID)
			}
		}
		fmt.Fprintf(&b, "\r\nFiles: %d   Notes: %d   Tasks: %d\r\n", len(a.s.Data.Items), len(a.s.Data.Notes), len(a.s.Data.Tasks))
		a.s.Unlock()
		show(b.String())
	}
	read := func(h uintptr) string {
		b := make([]uint16, 1024)
		getText.Call(h, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
		return syscall.UTF16ToString(b)
	}
	// Use the normal authenticated local handlers for actions; never export the key.
	action := func(path string, body map[string]any) {
		status, message := a.nativeAction(path, body)
		if status >= 300 {
			show(message)
		} else {
			refresh()
		}
	}
	callback := syscall.NewCallback(func(h uintptr, msg uint32, w, l uintptr) uintptr {
		switch msg {
		case 0x0010:
			user.NewProc("DestroyWindow").Call(h)
			return 0
		case 0x0002:
			user.NewProc("PostQuitMessage").Call(0)
			return 0
		case 0x0111:
			switch w & 0xffff {
			case 101:
				refresh()
			case 102:
				a.s.Lock()
				enabled := !a.s.Data.AutoClipboard
				a.s.Unlock()
				action("/local/clipboard", map[string]any{"enabled": enabled})
			case 103:
				value := read(password)
				setText.Call(password, uintptr(unsafe.Pointer(text(""))))
				action("/local/password", map[string]any{"password": value})
			case 104, 105:
				action("/local/pair/decision", map[string]any{"id": strings.TrimSpace(read(pending)), "approve": w&0xffff == 104})
			}
			return 0
		}
		result, _, _ := def.Call(h, uintptr(msg), w, l)
		return result
	})
	type windowClass struct {
		Size, Style                        uint32
		Proc                               uintptr
		ClsExtra, WndExtra                 int32
		Instance, Icon, Cursor, Background uintptr
		Menu, Class                        *uint16
		SmallIcon                          uintptr
	}
	instance, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := user.NewProc("LoadCursorW").Call(0, 32512)
	name := syscall.StringToUTF16Ptr("OneSpaceNativePreview")
	wc := windowClass{Proc: callback, Instance: instance, Cursor: cursor, Background: 6, Class: name}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if v, _, err := user.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); v == 0 {
		return fmt.Errorf("register window: %v", err)
	}
	defer user.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	hwnd, _, err := create.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(text("OneSpace — Native Preview"))), 0x00cf0000|0x10000000, 100, 100, 900, 700, 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("create window: %v", err)
	}
	control := func(class, label string, style uintptr, x, y, width, height, id int) uintptr {
		h, _, _ := create.Call(0, uintptr(unsafe.Pointer(text(class))), uintptr(unsafe.Pointer(text(label))), 0x40000000|0x10000000|style, uintptr(x), uintptr(y), uintptr(width), uintptr(height), hwnd, uintptr(id), instance, 0)
		font, _, _ := syscall.NewLazyDLL("gdi32.dll").NewProc("GetStockObject").Call(17)
		user.NewProc("SendMessageW").Call(h, 0x0030, font, 1)
		return h
	}
	control("BUTTON", "Refresh", 0x10000, 15, 15, 120, 30, 101)
	control("BUTTON", "Toggle clipboard sharing", 0x10000, 145, 15, 220, 30, 102)
	control("STATIC", "Initial password (12+ characters):", 0, 15, 60, 260, 25, 0)
	password = control("EDIT", "", 0x800000|0x10000|0x80|0x20, 280, 55, 365, 30, 0)
	control("BUTTON", "Set password", 0x10000, 660, 55, 180, 30, 103)
	control("STATIC", "Pending request ID:", 0, 15, 100, 160, 25, 0)
	pending = control("EDIT", "", 0x800000|0x10000|0x80, 180, 95, 350, 30, 0)
	control("BUTTON", "Approve", 0x10000, 545, 95, 140, 30, 104)
	control("BUTTON", "Reject", 0x10000, 700, 95, 140, 30, 105)
	output = control("EDIT", "", 0x800000|0x200000|0x100000|0x4|0x800|0x40|0x10000, 15, 140, 825, 480, 0)
	refresh()
	type point struct{ X, Y int32 }
	type message struct {
		Window         uintptr
		Message        uint32
		WParam, LParam uintptr
		Time           uint32
		Point          point
		Private        uint32
	}
	var m message
	for {
		value, _, err := user.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(value) == -1 {
			return fmt.Errorf("window messages: %v", err)
		}
		if value == 0 {
			break
		}
		user.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&m)))
		user.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}
