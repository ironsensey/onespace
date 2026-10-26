package clipboard

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
)

// imageToDIB builds a clipboard-ready, bottom-up 32-bit BI_RGB bitmap.
// In particular JPEG frames are converted straight from YCbCr into the DIB,
// avoiding a second full-frame RGBA allocation and repeated virtual At calls.
func imageToDIB(src image.Image) ([]byte, error) {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 1 || h < 1 || int64(w)*int64(h) > 32_000_000 {
		return nil, errors.New("image dimensions exceed clipboard safety limit")
	}
	size := w * h * 4
	data := make([]byte, 40+size)
	binary.LittleEndian.PutUint32(data[0:4], 40)
	binary.LittleEndian.PutUint32(data[4:8], uint32(w))
	binary.LittleEndian.PutUint32(data[8:12], uint32(h))
	binary.LittleEndian.PutUint16(data[12:14], 1)
	binary.LittleEndian.PutUint16(data[14:16], 32)
	binary.LittleEndian.PutUint32(data[20:24], uint32(size))

	for y := 0; y < h; y++ {
		srcY := bounds.Min.Y + y
		row := 40 + (h-1-y)*w*4
		switch im := src.(type) {
		case *image.YCbCr:
			for x := 0; x < w; x++ {
				xx := bounds.Min.X + x
				yy := im.Y[im.YOffset(xx, srcY)]
				ci := im.COffset(xx, srcY)
				r, g, b := color.YCbCrToRGB(yy, im.Cb[ci], im.Cr[ci])
				j := row + x*4
				data[j], data[j+1], data[j+2], data[j+3] = b, g, r, 255
			}
		case *image.RGBA:
			for x := 0; x < w; x++ {
				k := im.PixOffset(bounds.Min.X+x, srcY)
				j := row + x*4
				data[j], data[j+1], data[j+2], data[j+3] = im.Pix[k+2], im.Pix[k+1], im.Pix[k], 255
			}
		case *image.NRGBA:
			for x := 0; x < w; x++ {
				k := im.PixOffset(bounds.Min.X+x, srcY)
				j := row + x*4
				data[j], data[j+1], data[j+2], data[j+3] = im.Pix[k+2], im.Pix[k+1], im.Pix[k], 255
			}
		default:
			for x := 0; x < w; x++ {
				r, g, b, _ := src.At(bounds.Min.X+x, srcY).RGBA()
				j := row + x*4
				data[j], data[j+1], data[j+2], data[j+3] = byte(b>>8), byte(g>>8), byte(r>>8), 255
			}
		}
	}
	return data, nil
}
