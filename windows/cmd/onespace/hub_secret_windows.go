//go:build windows

package main

import (
	"encoding/base64"
	"errors"
	"syscall"
	"unsafe"
)

type hubDataBlob struct {
	Size uint32
	Data *byte
}

func hubDPAPI(input []byte, decrypt bool) ([]byte, error) {
	if len(input) == 0 {
		return []byte{}, nil
	}
	in := hubDataBlob{uint32(len(input)), &input[0]}
	var out hubDataBlob
	name := "CryptProtectData"
	if decrypt {
		name = "CryptUnprotectData"
	}
	ret, _, _ := syscall.NewLazyDLL("crypt32.dll").NewProc(name).Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	if ret == 0 {
		return nil, errors.New("Windows could not protect/unprotect the hub token")
	}
	defer syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree").Call(uintptr(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
func protectHubToken(s string) (string, error) {
	v, e := hubDPAPI([]byte(s), false)
	return base64.StdEncoding.EncodeToString(v), e
}
func unprotectHubToken(s string) (string, error) {
	v, e := base64.StdEncoding.DecodeString(s)
	if e != nil {
		return "", e
	}
	v, e = hubDPAPI(v, true)
	return string(v), e
}
