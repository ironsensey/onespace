package clipboard

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestImageToDIBOrientationAndColor(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	im.SetRGBA(1, 0, color.RGBA{G: 255, A: 255})
	im.SetRGBA(0, 1, color.RGBA{B: 255, A: 255})
	im.SetRGBA(1, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	data, err := imageToDIB(im)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(data[4:8]) != 2 || len(data) != 56 {
		t.Fatalf("invalid bitmap: %d", len(data))
	}
	// DIB bottom-left pixel is first: blue in BGRA.
	if !bytes.Equal(data[40:44], []byte{255, 0, 0, 255}) {
		t.Fatalf("bottom-left %v", data[40:44])
	}
	if !bytes.Equal(data[48:52], []byte{0, 0, 255, 255}) {
		t.Fatalf("top-left %v", data[48:52])
	}
}
func TestJPEGToDIB(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < 16; i++ {
		for j := 0; j < 16; j++ {
			im.SetRGBA(i, j, color.RGBA{R: 235, G: 41, B: 31, A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, nil); err != nil {
		t.Fatal(err)
	}
	jpegIm, err := jpeg.Decode(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	dib, err := imageToDIB(jpegIm)
	if err != nil {
		t.Fatal(err)
	}
	if len(dib) != 40+16*16*4 {
		t.Fatalf("size %d", len(dib))
	}
	if dib[40+2] < 180 {
		t.Fatalf("unexpected JPEG red channel: %v", dib[40:44])
	}
}
func TestInvalidDIBDimensions(t *testing.T) {
	if _, err := imageToDIB(image.NewRGBA(image.Rect(0, 0, 0, 0))); err == nil {
		t.Fatal("empty image accepted")
	}
}
func BenchmarkImageToDIBYCbCr(b *testing.B) {
	im := image.NewYCbCr(image.Rect(0, 0, 1920, 1080), image.YCbCrSubsampleRatio420)
	b.SetBytes(int64(1920 * 1080 * 4))
	for n := 0; n < b.N; n++ {
		_, err := imageToDIB(im)
		if err != nil {
			b.Fatal(err)
		}
	}
}
