package ninja

import (
	"image"
	"image/draw"
	"os"
	"testing"

	"narutotimer/internal/match"
)

// Same templates and NCC; file decoding is not measured. This is an ordinary
// label, not one of the pre-templated special ninjas.
func BenchmarkUnknownNameSearch(b *testing.B) {
	f, err := os.Open("../../inbox/regressions/duel-second-round-20260906.png")
	if err != nil {
		b.Fatal(err)
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		b.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	scale := float64(img.Bounds().Dx()) / 960
	first := image.Pt(int(.889375*float64(img.Bounds().Dx())+.5), int(.11444444444444445*float64(img.Bounds().Dy())+.5))
	roi := NameRegion(first, scale, false).Intersect(img.Bounds())
	reader := NewReader()
	reader.Read(img, roi, scale)
	b.Run("full_native_reference", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			view := img.SubImage(roi).(*image.RGBA)
			gray := match.ToGray(view)
			for _, templ := range reader.prepared {
				if _, err := (match.NCC{}).Match(match.Query{Image: view, Gray: gray, ROI: roi, Prepared: templ.ncc}); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("bounded_then_native", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if got := reader.Read(img, roi, scale); got.Name != "" {
				b.Fatalf("ordinary name mislabeled as special: %+v", got)
			}
		}
	})
}
