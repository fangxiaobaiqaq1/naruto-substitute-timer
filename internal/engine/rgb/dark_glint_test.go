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

func TestDarkSweepUsesBothCurrentLobesAcrossPalettes(t *testing.T) {
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left"}, X: 100, Y: 100}
	area := detect.ContentArea{W: 1600, H: 900}
	for _, tc := range []struct {
		name      string
		id        ninja.Readout
		dark, rim color.RGBA
	}{
		{"ordinary", ninja.Readout{}, color.RGBA{28, 54, 98, 255}, color.RGBA{35, 150, 190, 255}},
		{"warm-four", ninja.Readout{Name: ninja.Madara, Slots: 4, Palette: ninja.Warm}, color.RGBA{28, 54, 98, 255}, color.RGBA{35, 150, 190, 255}},
		{"purple", ninja.Readout{Name: ninja.Obito, Slots: 4, Palette: ninja.Purple}, color.RGBA{40, 15, 80, 255}, color.RGBA{200, 50, 240, 255}},
		{"red", ninja.Readout{Name: ninja.Naruto, Slots: 4, Palette: ninja.Red}, color.RGBA{70, 25, 20, 255}, color.RGBA{240, 65, 18, 255}},
	} {
		for _, kind := range []string{"bounded", "flat dark", "one rim", "upper only", "lower only", "full cover"} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				img := image.NewRGBA(image.Rect(0, 0, area.W, area.H))
				// Synthetic outlined body and a horizontal sweep; not game pixels.
				draw.Draw(img, image.Rect(93, 93, 108, 108), image.NewUniform(tc.rim), image.Point{}, draw.Src)
				draw.Draw(img, image.Rect(96, 93, 105, 108), image.NewUniform(tc.dark), image.Point{}, draw.Src)
				switch kind {
				case "flat dark":
					draw.Draw(img, img.Bounds(), image.NewUniform(tc.dark), image.Point{}, draw.Src)
				case "one rim":
					draw.Draw(img, image.Rect(93, 93, 97, 108), image.NewUniform(tc.dark), image.Point{}, draw.Src)
				case "upper only":
					draw.Draw(img, image.Rect(90, 100, 111, 120), image.White, image.Point{}, draw.Src)
				case "lower only":
					draw.Draw(img, image.Rect(90, 80, 111, 101), image.White, image.Point{}, draw.Src)
				case "full cover":
					draw.Draw(img, image.Rect(90, 80, 111, 120), image.White, image.Point{}, draw.Src)
				}
				draw.Draw(img, image.Rect(89, 97, 112, 102), image.White, image.Point{}, draw.Src)
				want := kind == "bounded"
				if got := darkGlintBody(img, p, 6, 7, tc.id.Palette); got != want {
					t.Fatalf("body proof=%v want=%v", got, want)
				}
				b := sampleCalibratedSpecial(img, []detect.BeadPosition{p}, area, config.Default().Vision, [2]ninja.Readout{tc.id, {}})[0]
				if b.Lit || b.Unknown == want {
					t.Fatalf("wrong empty/unknown state: %+v", b)
				}
			})
		}
	}
}

func TestSpatialContrastDoesNotReuseMissingRowsOrOneGap(t *testing.T) {
	w := newBodyContrast(2)
	if w.add(80, 0, 32) || w.add(80, 0, 32) {
		t.Fatal("one-sided effect passed")
	}
	w.reset()
	if w.add(80, 80, 32) {
		t.Fatal("single row passed")
	}
	w.reset()
	if w.add(0, 0, 32) || w.add(0, 0, 32) {
		t.Fatal("rows before invalidation were retained")
	}
	w.reset()
	if w.add(33, 31, 32) || !w.add(31, 33, 32) {
		t.Fatal("adjacent spatial support lost to rounding")
	}
	positions := []detect.BeadPosition{
		{Bead: detect.Bead{Side: "left", Idx: 0}, X: 100},
		{Bead: detect.Bead{Side: "left", Idx: 1}, X: 125},
		{Bead: detect.Bead{Side: "right", Idx: 0}, X: 101},
	}
	if got := beadHalfPitch(positions, positions[0], 12); got != 13 {
		t.Fatalf("rounded core/opposite side replaced 25px pitch: %d", got)
	}
}

func TestDarkSweepCannotTurnFilledBodyIntoEmpty(t *testing.T) {
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left"}, X: 100, Y: 100}
	for _, c := range []color.RGBA{{70, 190, 235, 255}, {255, 220, 0, 255}, {220, 50, 255, 255}, {240, 65, 18, 255}} {
		img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
		draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{28, 54, 98, 255}), image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(94, 92, 107, 109), image.NewUniform(c), image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(89, 97, 112, 102), image.White, image.Point{}, draw.Src)
		for _, palette := range []ninja.Palette{"", ninja.Purple, ninja.Warm, ninja.Red} {
			if darkGlintBody(img, p, 6, 7, palette) {
				t.Fatalf("filled %v became dark with %s", c, palette)
			}
		}
	}
}
