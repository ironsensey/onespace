//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

func nativePickFile(owner uintptr) string { return nativeFileDialog(owner, make([]uint16, 32768)) }
func nativeFileDialog(owner uintptr, buffer []uint16) string {
	type openFile struct {
		Size                        uint32
		Owner, Instance             uintptr
		Filter, Custom              *uint16
		MaxCustom, FilterIndex      uint32
		File                        *uint16
		MaxFile                     uint32
		FileTitle                   *uint16
		MaxTitle                    uint32
		InitialDir, Title           *uint16
		Flags                       uint32
		FileOffset, ExtensionOffset uint16
		DefaultExtension            *uint16
		Data, Hook                  uintptr
		Template                    *uint16
		Reserved                    uintptr
		ReservedNumber, FlagsEx     uint32
	}
	filter := []uint16{'A', 'l', 'l', ' ', 'f', 'i', 'l', 'e', 's', 0, '*', '.', '*', 0, 0}
	dialog := openFile{Owner: owner, Filter: &filter[0], FilterIndex: 1, File: &buffer[0], MaxFile: uint32(len(buffer)), Flags: 0x1000 | 0x800 | 0x80000 | 8}
	dialog.Size = uint32(unsafe.Sizeof(dialog))
	ok, _, _ := syscall.NewLazyDLL("comdlg32.dll").NewProc("GetOpenFileNameW").Call(uintptr(unsafe.Pointer(&dialog)))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buffer)
}
