package main

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf16"
)

// APKInfo reads the compiled AndroidManifest.xml without requiring Android SDK/aapt.
// It is intentionally a strict metadata reader, NOT a signature verifier.
type APKInfo struct {
	Package     string
	VersionCode int64
	VersionName string
}

func readAPKInfo(path string) (APKInfo, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return APKInfo{}, err
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name != "AndroidManifest.xml" {
			continue
		}
		if f.UncompressedSize64 > 4<<20 {
			return APKInfo{}, errors.New("oversized Android manifest")
		}
		rc, e := f.Open()
		if e != nil {
			return APKInfo{}, e
		}
		b, e := io.ReadAll(io.LimitReader(rc, 4<<20+1))
		rc.Close()
		if e != nil {
			return APKInfo{}, e
		}
		if len(b) > 4<<20 {
			return APKInfo{}, errors.New("oversized Android manifest")
		}
		return parseAXML(b)
	}
	return APKInfo{}, errors.New("APK has no compiled AndroidManifest.xml")
}

func u16(b []byte, i int) uint16 { return binary.LittleEndian.Uint16(b[i:]) }
func u32(b []byte, i int) uint32 { return binary.LittleEndian.Uint32(b[i:]) }
func checkPart(b []byte, off, n int) bool {
	return off >= 0 && n >= 0 && off <= len(b) && n <= len(b)-off
}
func readLength8(b []byte, p *int, end int) (int, error) {
	if *p >= end {
		return 0, io.ErrUnexpectedEOF
	}
	x := int(b[*p])
	*p++
	if x&128 == 0 {
		return x, nil
	}
	if *p >= end {
		return 0, io.ErrUnexpectedEOF
	}
	y := int(b[*p])
	*p++
	return ((x & 127) << 8) | y, nil
}
func readLength16(b []byte, p *int, end int) (int, error) {
	if *p+2 > end {
		return 0, io.ErrUnexpectedEOF
	}
	x := int(u16(b, *p))
	*p += 2
	if x&0x8000 == 0 {
		return x, nil
	}
	if *p+2 > end {
		return 0, io.ErrUnexpectedEOF
	}
	y := int(u16(b, *p))
	*p += 2
	return ((x & 0x7fff) << 16) | y, nil
}
func readPool(b []byte, off, size, hdr int) ([]string, error) {
	if hdr < 28 || !checkPart(b, off, 28) {
		return nil, errors.New("invalid AXML string pool")
	}
	count := int(u32(b, off+8))
	flags := u32(b, off+16)
	start := int(u32(b, off+20))
	if count < 0 || count > 100000 || !checkPart(b, off+hdr, count*4) {
		return nil, errors.New("invalid string count")
	}
	strings := make([]string, count)
	for i := 0; i < count; i++ {
		pos := off + start + int(u32(b, off+hdr+i*4))
		end := off + size
		if pos < off || pos >= end {
			return nil, errors.New("invalid string pointer")
		}
		p := pos
		if flags&0x100 != 0 {
			if _, err := readLength8(b, &p, end); err != nil {
				return nil, err
			}
			n, err := readLength8(b, &p, end)
			if err != nil {
				return nil, err
			}
			if n > 1<<20 || !checkPart(b, p, n+1) || p+n+1 > end {
				return nil, errors.New("bad UTF-8 manifest string")
			}
			strings[i] = string(b[p : p+n])
		} else {
			n, err := readLength16(b, &p, end)
			if err != nil {
				return nil, err
			}
			if n > 1<<20 || !checkPart(b, p, n*2+2) || p+n*2+2 > end {
				return nil, errors.New("bad UTF-16 manifest string")
			}
			units := make([]uint16, n)
			for j := 0; j < n; j++ {
				units[j] = u16(b, p+j*2)
			}
			strings[i] = string(utf16.Decode(units))
		}
	}
	return strings, nil
}
func parseAXML(b []byte) (APKInfo, error) {
	if len(b) < 8 || u16(b, 0) != 3 || int(u32(b, 4)) > len(b) || u16(b, 2) < 8 {
		return APKInfo{}, errors.New("not a compiled Android XML manifest")
	}
	strs := []string(nil)
	var result APKInfo
	found := false
	for off := int(u16(b, 2)); off+8 <= len(b); {
		size := int(u32(b, off+4))
		hdr := int(u16(b, off+2))
		kind := u16(b, off)
		if size < 8 || hdr < 8 || hdr > size || !checkPart(b, off, size) {
			return APKInfo{}, errors.New("corrupt manifest chunk")
		}
		if kind == 1 {
			var err error
			strs, err = readPool(b, off, size, hdr)
			if err != nil {
				return APKInfo{}, err
			}
		}
		if kind == 0x0102 && len(strs) > 0 {
			if hdr < 16 || size < 36 {
				return APKInfo{}, errors.New("bad element")
			}
			get := func(idx uint32) string {
				if idx >= uint32(len(strs)) {
					return ""
				}
				return strs[idx]
			}
			if get(u32(b, off+20)) == "manifest" {
				found = true
				ext := off + 16
				start := int(u16(b, ext+8))
				attrSize := int(u16(b, ext+10))
				count := int(u16(b, ext+12))
				if attrSize < 20 || count > 500 || !checkPart(b, ext+start, attrSize*count) || ext+start+attrSize*count > off+size {
					return APKInfo{}, errors.New("bad manifest attributes")
				}
				for i := 0; i < count; i++ {
					a := ext + start + i*attrSize
					name := get(u32(b, a+4))
					value := get(u32(b, a+8))
					if value == "" {
						typ := b[a+15]
						v := u32(b, a+16)
						if typ == 3 {
							value = get(v)
						}
						if typ == 0x10 || typ == 0x11 {
							value = fmt.Sprint(v)
						}
					}
					switch name {
					case "package":
						result.Package = value
					case "versionCode":
						var n int64
						_, _ = fmt.Sscan(value, &n)
						result.VersionCode = n
					case "versionName":
						result.VersionName = value
					}
				}
				break
			}
		}
		off += size
	}
	if !found || result.Package != "com.onespace.companion" || result.VersionCode < 1 || result.VersionName == "" {
		return APKInfo{}, errors.New("APK package or version metadata missing/mismatched")
	}
	return result, nil
}
