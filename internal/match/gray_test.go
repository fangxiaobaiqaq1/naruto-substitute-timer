package match

import (
	"image"
	"math/rand/v2"
	"testing"
)

// toGrayReference is the former scalar RGBA path of ToGray.
func toGrayReference(rgba *image.RGBA) *image.Gray {
	b := rgba.Bounds()
	out := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			i := rgba.PixOffset(b.Min.X+x, b.Min.Y+y)
			out.Pix[y*out.Stride+x] = uint8((299*uint32(rgba.Pix[i]) + 587*uint32(rgba.Pix[i+1]) + 114*uint32(rgba.Pix[i+2]) + 500) / 1000)
		}
	}
	return out
}

// Every 24-bit color (with arbitrary alpha) converts exactly like the scalar
// formula: the SIMD reciprocal division is exact for the whole range.
func TestToGrayAllColorsExact(t *testing.T) {
	if grayRows == nil {
		t.Skip("no SIMD luma in this build")
	}
	img := image.NewRGBA(image.Rect(0, 0, 4096, 256))
	for chunk := 0; chunk < 16; chunk++ {
		for i := 0; i < 4096*256; i++ {
			c := chunk<<20 | i
			p := img.Pix[i*4 : i*4+4]
			p[0], p[1], p[2], p[3] = uint8(c>>16), uint8(c>>8), uint8(c), uint8(c*7)
		}
		got, want := ToGray(img), toGrayReference(img)
		for i := range want.Pix {
			if got.Pix[i] != want.Pix[i] {
				c := chunk<<20 | i
				t.Fatalf("rgb %06x: got %d want %d", c, got.Pix[i], want.Pix[i])
			}
		}
	}
}

// Sub-images (origins, strides, widths not a multiple of 4) convert exactly.
func TestToGraySubImagesExact(t *testing.T) {
	r := rand.New(rand.NewPCG(12, 34))
	base := image.NewRGBA(image.Rect(-7, -3, 301, 97))
	for i := range base.Pix {
		base.Pix[i] = uint8(r.IntN(256))
	}
	for _, rect := range []image.Rectangle{base.Rect, image.Rect(-7, -3, -6, -2), image.Rect(0, 0, 3, 5), image.Rect(1, 2, 6, 3), image.Rect(5, 9, 300, 97), image.Rect(-2, 4, 271, 60), image.Rect(296, 90, 301, 97)} {
		view := base.SubImage(rect).(*image.RGBA)
		got, want := ToGray(view), toGrayReference(view)
		if got.Rect != want.Rect || string(got.Pix) != string(want.Pix) {
			t.Fatalf("%v: gray differs", rect)
		}
	}
}

func BenchmarkToGray(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 1))
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.IntN(256))
	}
	view := img.SubImage(image.Rect(1000, 700, 1864, 1002)).(*image.RGBA)
	b.Run("reference", func(b *testing.B) {
		b.SetBytes(864 * 302)
		for b.Loop() {
			toGrayReference(view)
		}
	})
	b.Run("ToGray", func(b *testing.B) {
		b.SetBytes(864 * 302)
		for b.Loop() {
			ToGray(view)
		}
	})
}
