package rgb

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/ninja"
)

func TestPairedPurpleBodyRequiresBothLobesAndGaps(t *testing.T) {
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left"}, X: 100, Y: 100}
	purple := color.RGBA{220, 60, 245, 255}
	for _, kind := range []string{"both", "lower only", "upper only", "flat band", "one-sided", "vertical wash"} {
		t.Run(kind, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 200, 200))
			paint := func(r image.Rectangle, c color.RGBA) { draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src) }
			paint(image.Rect(96, 98, 105, 103), color.RGBA{255, 240, 255, 255})
			// White below is allowed only with TWO current filled lobes.
			paint(image.Rect(95, 112, 106, 119), color.RGBA{255, 255, 255, 255})
			if kind != "lower only" {
				paint(image.Rect(98, 94, 103, 97), purple)
			}
			if kind != "upper only" {
				paint(image.Rect(98, 104, 103, 107), purple)
			}
			switch kind {
			case "flat band":
				paint(image.Rect(80, 94, 121, 107), purple)
			case "one-sided":
				paint(image.Rect(80, 94, 103, 107), purple)
			case "vertical wash":
				paint(image.Rect(98, 70, 103, 131), purple)
			}
			paint(image.Rect(96, 98, 105, 103), color.RGBA{255, 240, 255, 255})
			if got := purplePairedBody(img, p, 4, 4, 8); got != (kind == "both") {
				t.Fatalf("proof=%v", got)
			}
			id := [2]ninja.Readout{{Name: ninja.SasukeXiayin, Palette: ninja.Purple, Slots: 4}, {}}
			bead := sampleCalibratedSpecial(img, []detect.BeadPosition{p}, detect.ContentArea{W: 960, H: 540}, config.Default().Vision, id)[0]
			if bead.Lit != (kind == "both") {
				t.Fatalf("unproved white core admitted or real body lost: %+v", bead)
			}
		})
	}
}
