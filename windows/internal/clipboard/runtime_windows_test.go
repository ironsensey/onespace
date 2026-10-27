//go:build windows

package clipboard

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// Opt in only on a disposable Windows test desktop: this changes its clipboard.
func TestWindowsClipboardTextAndImageRoundTrip(t *testing.T) {
	if os.Getenv("ONESPACE_WINDOWS_CLIPBOARD_TEST") != "1" {
		t.Skip("requires a disposable Windows desktop")
	}
	previous, _ := GetText()
	defer func() {
		if previous != "" {
			_ = SetText(previous)
		}
	}()
	value := "OneSpace native clipboard — Unicode ✓"
	if err := SetText(value); err != nil {
		t.Fatal(err)
	}
	got, err := GetText()
	if err != nil || got != value {
		t.Fatalf("native text round-trip failed: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, G: 50, B: 100, A: 255})
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "fixture.png")
	if err = os.WriteFile(file, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err = SetImage(file); err != nil {
		t.Fatal(err)
	}
	data, err := GetImagePNG()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r>>8 != 200 || g>>8 != 50 || b>>8 != 100 {
		t.Fatal("native bitmap copy altered colour")
	}
}
