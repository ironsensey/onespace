//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

func openNativeDashboard(a *App) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	user := syscall.NewLazyDLL("user32.dll")
	kernel := syscall.NewLazyDLL("kernel32.dll")
	ole := syscall.NewLazyDLL("ole32.dll")
	ole.NewProc("CoInitializeEx").Call(0, 2)
	defer ole.NewProc("CoUninitialize").Call()
	create := user.NewProc("CreateWindowExW")
	def := user.NewProc("DefWindowProcW")
	send := user.NewProc("SendMessageW")
	set := user.NewProc("SetWindowTextW")
	ptr := func(s string) *uint16 { return syscall.StringToUTF16Ptr(strings.ReplaceAll(s, "\x00", "")) }
	var hwnd, nav, output, help uintptr
	var children, editors []uintptr
	var fields []nativeField
	var buttons []nativeButton
	var refresh func()
	var render func(int)
	var busy bool
	var cameraRunning bool
	var cancel context.CancelFunc
	var view int
	var generation uint64
	var mu sync.Mutex
	type result struct {
		generation uint64
		code       int
		text       string
	}
	results := make(chan result, 4)
	control := func(class, label string, style uintptr, x, y, width, height, id int) uintptr {
		h, _, _ := create.Call(0, uintptr(unsafe.Pointer(ptr(class))), uintptr(unsafe.Pointer(ptr(label))), 0x40000000|0x10000000|style, uintptr(x), uintptr(y), uintptr(width), uintptr(height), hwnd, uintptr(id), 0, 0)
		font, _, _ := syscall.NewLazyDLL("gdi32.dll").NewProc("GetStockObject").Call(17)
		send.Call(h, 0x0030, font, 1)
		return h
	}
	text := func(h uintptr, s string) { set.Call(h, uintptr(unsafe.Pointer(ptr(s)))) }
	read := func(h uintptr) string {
		n, _, _ := user.NewProc("GetWindowTextLengthW").Call(h)
		if n > 200000 {
			n = 200000
		}
		buf := make([]uint16, n+1)
		user.NewProc("GetWindowTextW").Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		return syscall.UTF16ToString(buf)
	}
	run := func(action func(context.Context) (int, string)) {
		if busy {
			return
		}
		busy = true
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		stamp := generation
		text(output, "Working… You can cancel a transfer below. Do not close the source file.")
		go func() {
			code, message := action(ctx)
			stop()
			mu.Lock()
			results <- result{stamp, code, message}
			mu.Unlock()
			user.NewProc("PostMessageW").Call(hwnd, 0x8001, 0, 0)
		}()
	}
	callback := syscall.NewCallback(func(h uintptr, msg uint32, w, l uintptr) uintptr {
		switch msg {
		case 0x0218:
			if w == 0x12 || w == 7 {
				localHubFailures.Lock()
				localHubFailures.values = map[string]time.Time{}
				localHubFailures.Unlock()
				select {
				case a.hub.changed <- struct{}{}:
				default:
				}
			}
			return 1
		case 0x0010:
			if cancel != nil {
				cancel()
			}
			user.NewProc("DestroyWindow").Call(h)
			return 0
		case 0x0002:
			user.NewProc("PostQuitMessage").Call(0)
			return 0
		case 0x8002:
			cameraRunning = false
			return 0
		case 0x8001:
			select {
			case r := <-results:
				busy = false
				if r.generation == generation {
					prefix := ""
					if r.code >= 300 {
						prefix = fmt.Sprintf("Action failed (%d)\r\n", r.code)
					}
					text(output, prefix+nativeReadable(r.text))
				}
			default:
			}
			return 0
		case 0x0111:
			id := int(w & 0xffff)
			if id == 10 && w>>16 == 1 {
				if busy {
					send.Call(nav, 0x0186, uintptr(view), 0)
					return 0
				}
				selected, _, _ := send.Call(nav, 0x0188, 0, 0)
				render(int(selected))
				return 0
			}
			if id == 20 {
				if refresh != nil {
					refresh()
				}
				return 0
			}
			if id == 21 {
				if cancel != nil {
					cancel()
				}
				return 0
			}
			if id == 23 {
				if !busy && len(fields) > 0 && strings.Contains(strings.ToLower(fields[0].Label), "path") {
					if path := nativePickFile(hwnd); path != "" {
						text(editors[0], path)
					}
				}
				return 0
			}
			if id == 22 {
				if cameraRunning {
					return 0
				}
				executable, err := os.Executable()
				if err != nil {
					text(output, "Cannot locate OneSpace executable")
					return 0
				}
				command := exec.Command(executable, "-camera-ui", "-data", a.s.root)
				if err = command.Start(); err != nil {
					text(output, "Cannot start the camera player")
					return 0
				}
				cameraRunning = true
				go func() { _ = command.Wait(); user.NewProc("PostMessageW").Call(hwnd, 0x8002, 0, 0) }()
				return 0
			}

			if id >= 100 && id < 100+len(buttons) && !busy {
				b := buttons[id-100]
				if b.Destructive {
					answer, _, _ := user.NewProc("MessageBoxW").Call(hwnd, uintptr(unsafe.Pointer(ptr("Confirm: "+b.Name+"?"))), uintptr(unsafe.Pointer(ptr("OneSpace"))), 0x4|0x30)
					if answer != 6 {
						return 0
					}
				}
				values := make([]string, len(editors))
				for i, edit := range editors {
					if fields[i].Check {
						checked, _, _ := send.Call(edit, 0x00f0, 0, 0)
						values[i] = strconv.FormatBool(checked == 1)
					} else {
						values[i] = read(edit)
						if fields[i].Secret {
							text(edit, "")
						}
					}
				}
				run(func(ctx context.Context) (int, string) { return b.Run(ctx, values) })
				return 0
			}
		}
		result, _, _ := def.Call(h, uintptr(msg), w, l)
		return result
	})
	type wc struct {
		Size, Style                        uint32
		Proc                               uintptr
		ClsExtra, WndExtra                 int32
		Instance, Icon, Cursor, Background uintptr
		Menu, Class                        *uint16
		SmallIcon                          uintptr
	}
	instance, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := user.NewProc("LoadCursorW").Call(0, 32512)
	name := ptr("OneSpaceNative")
	class := wc{Proc: callback, Instance: instance, Cursor: cursor, Background: 6, Class: name}
	class.Size = uint32(unsafe.Sizeof(class))
	if value, _, err := user.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class))); value == 0 {
		return fmt.Errorf("register native window: %v", err)
	}
	defer user.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	// Fixed client layout keeps every form visible at the tested design dimensions.
	screenHeight, _, _ := user.NewProc("GetSystemMetrics").Call(1)
	windowHeight := min(900, max(640, int(screenHeight)-80))
	footerY := windowHeight - 85
	hwnd, _, err := create.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(ptr("OneSpace"))), 0x00ca0000|0x10000000, 60, 30, 1120, uintptr(windowHeight), 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("create native window: %v", err)
	}
	nav = control("LISTBOX", "", 0x800000|0x10000|1, 12, 15, 175, footerY-30, 10)
	pages := a.nativePages()
	for _, p := range pages {
		send.Call(nav, 0x0180, 0, uintptr(unsafe.Pointer(ptr(p.Name))))
	}
	control("BUTTON", "Refresh", 0x10000, 12, footerY, 175, 30, 20)
	control("BUTTON", "Cancel transfer", 0x10000, 200, footerY, 175, 30, 21)
	control("BUTTON", "Live camera", 0x10000, 390, footerY, 175, 30, 22)
	control("BUTTON", "Choose file…", 0x10000, 580, footerY, 175, 30, 23)
	render = func(index int) {
		if index < 0 || index >= len(pages) {
			return
		}
		generation++
		view = index
		for _, c := range children {
			user.NewProc("DestroyWindow").Call(c)
		}
		children = nil
		editors = nil
		// Re-read local state so checkboxes and the saved hub profile are current.
		pages = a.nativePages()
		p := pages[index]
		fields = p.Fields
		buttons = p.Buttons
		help = control("STATIC", p.Help, 0, 200, 15, 880, 55, 0)
		children = append(children, help)
		y := 80
		for index, f := range fields {
			if f.Check {
				h := control("BUTTON", f.Label, 0x10000|3, 205, y, 860, 30, 200+index)
				if f.Value == "true" {
					send.Call(h, 0x00f1, 1, 0)
				}
				editors = append(editors, h)
				children = append(children, h)
				y += 36
				continue
			}
			label := control("STATIC", f.Label, 0, 205, y, 350, 22, 0)
			style := uintptr(0x800000 | 0x10000 | 0x80)
			height := 28
			if f.Secret {
				style |= 0x20
			}
			if f.Multi {
				style |= 0x4 | 0x40 | 0x200000
				height = 58
			}
			edit := control("EDIT", f.Value, style, 555, y, 520, height, 200+index)
			send.Call(edit, 0x00c5, 200000, 0)
			editors = append(editors, edit)
			children = append(children, label, edit)
			y += height + 10
		}
		for i, b := range buttons {
			h := control("BUTTON", b.Name, 0x10000, 205+(i%3)*290, y+(i/3)*36, 280, 30, 100+i)
			children = append(children, h)
		}
		if len(buttons) > 0 {
			y += ((len(buttons) + 2) / 3) * 36
		}
		y += 8
		output = control("EDIT", "", 0x800000|0x200000|0x100000|0x4|0x800|0x40|0x10000, 205, y, 870, max(80, footerY-20-y), 0)
		send.Call(output, 0x00c5, 8<<20, 0)
		children = append(children, output)
		refresh = func() {
			if p.Load != nil {
				run(p.Load)
			}
		}
		refresh()
	}
	send.Call(nav, 0x0186, 0, 0)
	render(0)
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
			return fmt.Errorf("native message loop: %v", err)
		}
		if value == 0 {
			break
		}
		handled, _, _ := user.NewProc("IsDialogMessageW").Call(hwnd, uintptr(unsafe.Pointer(&m)))
		if handled == 0 {
			user.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&m)))
			user.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&m)))
		}
	}
	if cancel != nil {
		cancel()
	}
	if busy {
		select {
		case <-results:
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

func showNativeError(message string) {
	text := syscall.StringToUTF16Ptr(strings.ReplaceAll(message, "\x00", ""))
	title := syscall.StringToUTF16Ptr("OneSpace")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
