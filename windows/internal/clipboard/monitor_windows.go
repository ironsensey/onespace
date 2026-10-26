//go:build windows

package clipboard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"syscall"
	"unsafe"
)

var getClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")

// Sequence costs no clipboard-open operation. Only decode clipboard data if it changed.
func Sequence() uint32 {
	n, _, _ := getClipboardSequenceNumber.Call()
	return uint32(n)
}

// GetImagePNG reads commonly supported 24/32-bit uncompressed Windows DIBs.
// Unrecognized formats are intentionally ignored rather than crashing the background agent.
func GetImagePNG() ([]byte, error) {
	var result []byte
	var dib []byte
	err := withClipboard(func() error {
		var handle uintptr
		for _, format := range []uintptr{cfDIB, 17 /* CF_DIBV5 */} {
			handle, _, _ = getClip.Call(format)
			if handle != 0 {
				break
			}
		}
		if handle == 0 {
			return errors.New("clipboard has no DIB image")
		}
		ptr, _, _ := globalLock.Call(handle)
		if ptr == 0 {
			return errors.New("cannot lock DIB")
		}
		defer globalUnlock.Call(handle)
		size, _, _ := globalSize.Call(handle)
		if size < 40 || size > 100<<20 {
			return errors.New("DIB too large or incomplete")
		}
		// Copy while the clipboard is locked; expensive conversion happens after CloseClipboard.
		dib = append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(size))...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	encode := func() error {
		header := int(binary.LittleEndian.Uint32(dib[0:4]))
		if header < 40 || header > 124 || header > len(dib) {
			return errors.New("unsupported DIB header")
		}
		w := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
		rawHeight := int32(binary.LittleEndian.Uint32(dib[8:12]))
		h := int(rawHeight)
		if h < 0 {
			h = -h
		}
		if w < 1 || h < 1 || int64(w)*int64(h) > 12000000 {
			return errors.New("image exceeds pixel budget")
		}
		if binary.LittleEndian.Uint16(dib[12:14]) != 1 {
			return errors.New("unsupported DIB planes")
		}
		bpp := int(binary.LittleEndian.Uint16(dib[14:16]))
		compression := binary.LittleEndian.Uint32(dib[16:20])
		if (bpp != 24 && bpp != 32) || compression != 0 {
			return errors.New("unsupported compressed or bitfield DIB")
		}
		// Default BI_RGB uncompressed pixels begin immediately after the DIB header.
		row := ((w*bpp + 31) / 32) * 4
		offset := header
		if row <= 0 || int64(offset)+int64(row)*int64(h) > int64(len(dib)) {
			return errors.New("truncated bitmap")
		}
		im := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			sy := y
			if rawHeight > 0 {
				sy = h - 1 - y
			}
			for x := 0; x < w; x++ {
				i := offset + sy*row + x*(bpp/8)
				im.SetRGBA(x, y, color.RGBA{R: dib[i+2], G: dib[i+1], B: dib[i], A: 255})
			}
		}
		var encoded bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&encoded, im); err != nil {
			return err
		}
		if encoded.Len() > 50<<20 {
			return errors.New("PNG exceeds sharing budget")
		}
		result = encoded.Bytes()
		return nil
	}
	err = encode()
	return result, err
}

// Ensure syscall isn't optimized out on minimal cross builds.
var _ = syscall.GetLastError
