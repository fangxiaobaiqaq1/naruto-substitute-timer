package ninja

import (
	"image"
	"image/draw"
	"math"
	"os"
	"testing"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/match"
)

func TestBothLiveNarutoNamesAtScaledWidths(t *testing.T) {
	f, err := os.Open("../../inbox/regressions/camp-both-naruto-right-one-20260907.png")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader()
	for _, w := range []int{800, 960, 1280, 1600, 1920, 2560} {
		img := image.NewRGBA(image.Rect(0, 0, w, w*9/16))
		xdraw.BiLinear.Scale(img, img.Bounds(), source, source.Bounds(), draw.Src, nil)
		for i, nx := range []float64{.096875, .869375} {
			first := image.Pt(int(math.Round(nx*float64(w))), int(math.Round(.11222222222222222*float64(img.Bounds().Dy()))))
			roi := NameRegion(first, float64(w)/960, i == 0)
			got := r.Read(img, roi, float64(w)/960)
			if got.Name != Naruto {
				t.Errorf("%d side%d: %+v", w, i, got)
				for _, p := range r.prepared {
					score, _ := (match.NCC{}).Match(match.Query{Image: img, ROI: roi, Prepared: p.ncc})
					t.Logf("%s reference %.4f at %v", p.readout.Name, score.Value, score.Peak)
				}
			}
		}
	}
}
