package imgsafe

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"narutotimer/internal/imgsafe/imgsafetest"
)

func TestDecodeRejectsDimensionBombWithoutAllocating(t *testing.T) {
	bomb := imgsafetest.DimensionBombPNG()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	img, _, err := DecodeBytes(bomb, Capture)
	runtime.ReadMemStats(&after)
	if err == nil || img != nil {
		t.Fatalf("bomb accepted: %v", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("rejecting the bomb allocated %d bytes", grew)
	}
}

func TestDecodeKeepsValidPixelsAndChecksFormat(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 37, 11))
	for i := range src.Pix {
		src.Pix[i] = byte(i * 7)
	}
	src.Set(3, 4, color.NRGBA{R: 1, G: 2, B: 3, A: 128})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	want, _, err := image.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, format, err := DecodeBytes(buf.Bytes(), Limits{MaxWidth: 37, MaxHeight: 11, MaxPixels: 37 * 11, Formats: []string{"png"}})
	if err != nil || format != "png" {
		t.Fatalf("valid PNG rejected: %v %q", err, format)
	}
	if !bytes.Equal(got.(*image.NRGBA).Pix, want.(*image.NRGBA).Pix) {
		t.Fatal("pixels differ from image.Decode")
	}
	for _, l := range []Limits{
		{MaxWidth: 36, MaxHeight: 11, MaxPixels: 1 << 20},
		{MaxWidth: 37, MaxHeight: 10, MaxPixels: 1 << 20},
		{MaxWidth: 99, MaxHeight: 99, MaxPixels: 37*11 - 1},
		{MaxWidth: 99, MaxHeight: 99, MaxPixels: 1 << 20, Formats: []string{"jpeg"}},
	} {
		if _, _, err := DecodeBytes(buf.Bytes(), l); err == nil {
			t.Fatalf("limits %+v not enforced", l)
		}
	}
	path := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecodeFile(path, Asset); err != nil {
		t.Fatal(err)
	}
}
