package leidian

import (
	"image"
	"image/draw"
)

// toRGBA converts a decoded screenshot into an owned, zero-origin RGBA image.
// The result is pixel-identical to the legacy per-pixel
// result.Set(x, y, decoded.At(x, y)) loop: RGBA is reused or row-copied,
// fully opaque NRGBA is row-copied (premultiplication is a no-op at alpha
// 255), and everything else goes through draw.Src, which applies the same
// color-model conversion as RGBA.Set.
func toRGBA(decoded image.Image) *image.RGBA {
	bounds := decoded.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	switch src := decoded.(type) {
	case *image.RGBA:
		if bounds.Min == (image.Point{}) && src.Stride == 4*w {
			return src
		}
		return copyRows(src.Pix, src.Stride, src.PixOffset(bounds.Min.X, bounds.Min.Y), w, h)
	case *image.NRGBA:
		if src.Opaque() {
			return copyRows(src.Pix, src.Stride, src.PixOffset(bounds.Min.X, bounds.Min.Y), w, h)
		}
	}
	result := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(result, result.Bounds(), decoded, bounds.Min, draw.Src)
	return result
}

func copyRows(pix []byte, stride, offset, w, h int) *image.RGBA {
	result := image.NewRGBA(image.Rect(0, 0, w, h))
	row := 4 * w
	for y := 0; y < h; y++ {
		copy(result.Pix[y*result.Stride:y*result.Stride+row], pix[offset+y*stride:offset+y*stride+row])
	}
	return result
}
