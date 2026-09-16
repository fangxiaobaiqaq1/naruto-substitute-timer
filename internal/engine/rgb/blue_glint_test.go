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

func TestPortraitGlintNeedsCurrentBlueBody(t *testing.T) {
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left"}, X: 100, Y: 100}
	area := detect.ContentArea{W: 960, H: 540}
	pale := color.RGBA{245, 235, 215, 255}
	cyan := color.RGBA{100, 200, 230, 255}
	for _, kind := range []string{"filled", "pale only", "white", "flat cyan", "one-sided", "one row", "hollow", "dark"} {
		t.Run(kind, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 200, 200))
			paint := func(box image.Rectangle, c color.RGBA) {
				draw.Draw(img, box, image.NewUniform(c), image.Point{}, draw.Src)
			}
			paint(image.Rect(97, 98, 104, 103), pale)
			switch kind {
			case "filled":
				paint(image.Rect(98, 103, 103, 105), cyan)
			case "white":
				draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
			case "flat cyan":
				paint(img.Bounds(), cyan)
			case "one-sided":
				paint(image.Rect(90, 103, 103, 105), cyan)
			case "one row":
				paint(image.Rect(98, 104, 103, 105), cyan)
			case "hollow":
				paint(image.Rect(96, 98, 97, 106), cyan)
				paint(image.Rect(104, 98, 105, 106), cyan)
			case "dark":
				paint(image.Rect(96, 96, 105, 105), color.RGBA{20, 40, 75, 255})
			}
			if got := blueGlintBody(img, p, 4, 4, 8); got != (kind == "filled") {
				t.Fatalf("body proof=%v", got)
			}
			bead := sampleCalibratedSpecial(img, []detect.BeadPosition{p}, area, config.Default().Vision, [2]ninja.Readout{})[0]
			if bead.Lit != (kind == "filled") {
				t.Fatalf("unproved core gained/lost vote: %+v", bead)
			}
			if kind == "dark" && bead.Unknown {
				t.Fatal("real dark core no longer readable")
			}
		})
	}
}

func TestDimPurpleBodyAgainstBrightScenery(t *testing.T) {
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left"}, X: 100, Y: 100}
	id := ninja.Readout{Name: ninja.SasukeXiayin, Slots: 4, Palette: ninja.Purple, RowOffsetY: 9}
	for _, whiteCore := range []bool{false, true} {
		img := image.NewRGBA(image.Rect(0, 0, 200, 200))
		// A white damage number BELOW the bean is background, not a white core.
		draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(96, 96, 105, 105), image.NewUniform(color.RGBA{151, 50, 214, 255}), image.Point{}, draw.Src)
		if !isolatedPurpleHalo(img, p, 4, 4, 10, 8) {
			t.Fatal("dim saturated filled body lost against white background")
		}
		if whiteCore {
			draw.Draw(img, image.Rect(98, 98, 103, 103), image.White, image.Point{}, draw.Src)
		}
		got := sampleCalibratedSpecial(img, []detect.BeadPosition{p}, detect.ContentArea{W: 960, H: 540}, config.Default().Vision, [2]ninja.Readout{id, {}})[0]
		if got.Lit == whiteCore || got.Unknown != whiteCore {
			t.Fatalf("whiteCore=%v: %+v", whiteCore, got)
		}
	}
}
