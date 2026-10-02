package leidian

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"
)

// toRGBALegacy is the original per-pixel conversion used before the fast path.
func toRGBALegacy(decoded image.Image) *image.RGBA {
	bounds := decoded.Bounds()
	result := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			result.Set(x, y, decoded.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return result
}

func randomBytes(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func assertSameRGBA(t *testing.T, name string, got, want *image.RGBA) {
	t.Helper()
	if got.Rect != want.Rect || got.Stride != want.Stride || !bytes.Equal(got.Pix, want.Pix) {
		t.Fatalf("%s: fast path differs from At/Set (rect %v vs %v)", name, got.Rect, want.Rect)
	}
}

func TestToRGBAParity(t *testing.T) {
	r := image.Rect(3, 5, 3+67, 5+41)
	rgba := image.NewRGBA(r)
	copy(rgba.Pix, randomBytes(len(rgba.Pix), 1))
	opaque := image.NewNRGBA(r)
	copy(opaque.Pix, randomBytes(len(opaque.Pix), 2))
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 255
	}
	translucent := image.NewNRGBA(r)
	copy(translucent.Pix, randomBytes(len(translucent.Pix), 3))
	gray := image.NewGray(r)
	copy(gray.Pix, randomBytes(len(gray.Pix), 4))
	nrgba64 := image.NewNRGBA64(r)
	copy(nrgba64.Pix, randomBytes(len(nrgba64.Pix), 5))
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.NRGBA{uint8(i), uint8(255 - i), uint8(i * 7), uint8(i * 3)}
	}
	paletted := image.NewPaletted(r, pal)
	copy(paletted.Pix, randomBytes(len(paletted.Pix), 6))
	zeroOrigin := image.NewRGBA(image.Rect(0, 0, 50, 30))
	copy(zeroOrigin.Pix, randomBytes(len(zeroOrigin.Pix), 7))
	sub := rgba.SubImage(image.Rect(10, 9, 40, 30))

	for name, img := range map[string]image.Image{
		"rgba": rgba, "rgba-zero-origin": zeroOrigin, "rgba-subimage": sub, "nrgba-opaque": opaque,
		"nrgba-translucent": translucent, "gray": gray, "nrgba64": nrgba64, "paletted": paletted,
	} {
		want := toRGBALegacy(img)
		assertSameRGBA(t, name, toRGBA(img), want)
	}
}

func TestToRGBAPNGRoundTripParity(t *testing.T) {
	for name, img := range map[string]image.Image{
		"rgba": func() image.Image {
			m := image.NewRGBA(image.Rect(0, 0, 64, 36))
			copy(m.Pix, randomBytes(len(m.Pix), 8))
			for i := 3; i < len(m.Pix); i += 4 {
				m.Pix[i] = 255
			}
			return m
		}(),
		"nrgba-opaque": func() image.Image {
			m := image.NewNRGBA(image.Rect(0, 0, 64, 36))
			copy(m.Pix, randomBytes(len(m.Pix), 9))
			for i := 3; i < len(m.Pix); i += 4 {
				m.Pix[i] = 255
			}
			return m
		}(),
		"nrgba-translucent": func() image.Image {
			m := image.NewNRGBA(image.Rect(0, 0, 64, 36))
			copy(m.Pix, randomBytes(len(m.Pix), 10))
			return m
		}(),
	} {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		decodedA, _ := png.Decode(bytes.NewReader(buf.Bytes()))
		decodedB, _ := png.Decode(bytes.NewReader(buf.Bytes()))
		assertSameRGBA(t, name, toRGBA(decodedA), toRGBALegacy(decodedB))
	}
}

func benchImage(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	copy(m.Pix, randomBytes(len(m.Pix), 11))
	for i := 3; i < len(m.Pix); i += 4 {
		m.Pix[i] = 255
	}
	return m
}

func BenchmarkPNGConvertLegacy1080p(b *testing.B) {
	img := benchImage(1920, 1080)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		toRGBALegacy(img)
	}
}

func BenchmarkPNGConvertFast1080p(b *testing.B) {
	img := benchImage(1920, 1080)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		toRGBA(img)
	}
}

var (
	pngEncode = png.Encode
	pngDecode = png.Decode
)
