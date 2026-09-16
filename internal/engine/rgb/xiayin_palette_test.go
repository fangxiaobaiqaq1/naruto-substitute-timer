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

// Synthetic body geometry, not captured gameplay. Each row is independent.
func xiayinTestRows(redLeft bool, counts [2]int) (*image.RGBA, []detect.BeadPosition) {
	img := image.NewRGBA(image.Rect(0, 0, 960, 540))
	var positions []detect.BeadPosition
	for side := 0; side < 2; side++ {
		for i := 0; i < 4; i++ {
			p := detect.BeadPosition{Bead: detect.Bead{Side: []string{"left", "right"}[side], Idx: i}, X: 100 + 16*i, Y: 100}
			if side == 1 {
				p.X = 850 - 16*i
			}
			positions = append(positions, p)
			c := color.RGBA{200, 60, 240, 255}
			if i >= counts[side] {
				c = color.RGBA{40, 15, 80, 255}
			}
			if (side == 0) == redLeft {
				c = color.RGBA{240, 95, 80, 255}
				if i >= counts[side] {
					c = color.RGBA{40, 10, 15, 255}
				}
			}
			draw.Draw(img, image.Rect(p.X-4, p.Y-5, p.X+5, p.Y+6), image.NewUniform(c), image.Point{}, draw.Src)
		}
	}
	return img, positions
}

func TestXiayinSkinsAreBilateralAndUseCurrentCounts(t *testing.T) {
	area := detect.ContentArea{W: 960, H: 540}
	for _, redLeft := range []bool{false, true} {
		for _, hint := range []bool{false, true} {
			id := ninja.Readout{Name: ninja.SasukeXiayin, Palette: ninja.Xiayin, Slots: 4, RowOffsetY: 9}
			if hint {
				id = ninja.Readout{Unverified: true, PaletteHint: ninja.Xiayin, Slots: 4, RowOffsetY: 9}
			}
			for _, counts := range [][2]int{{2, 3}, {2, 2}, {0, 0}, {4, 4}} {
				img, positions := xiayinTestRows(redLeft, counts)
				beads := sampleCalibratedSpecial(img, positions, area, config.Default().Vision, [2]ninja.Readout{id, id})
				l, r := countReady(beads)
				if l != counts[0] || r != counts[1] || knownCount(beads) != 8 {
					t.Fatalf("redLeft=%v hint=%v want=%v: %+v", redLeft, hint, counts, beads)
				}
			}
		}
	}
}

func TestXiayinRedRequiresVariantAndRejectsEffects(t *testing.T) {
	area := detect.ContentArea{W: 960, H: 540}
	for _, id := range []ninja.Readout{{}, {Name: ninja.Obito, Palette: ninja.Purple, Slots: 4}, {Unverified: true, PaletteHint: ninja.Purple, Slots: 4}} {
		img, positions := xiayinTestRows(false, [2]int{2, 3})
		beads := sampleCalibratedSpecial(img, positions, area, config.Default().Vision, [2]ninja.Readout{id, id})
		for _, b := range beads[4:] {
			if b.Lit || !b.Unknown {
				t.Fatalf("unverified red skin admitted: %+v %+v", id, b)
			}
		}
	}
	for _, hint := range []bool{false, true} {
		id := ninja.Readout{Name: ninja.SasukeXiayin, Palette: ninja.Xiayin, Slots: 4}
		if hint {
			id = ninja.Readout{Unverified: true, PaletteHint: ninja.Xiayin, Slots: 4}
		}
		for _, c := range []color.RGBA{{240, 40, 20, 255}, {255, 255, 255, 255}} {
			img, positions := xiayinTestRows(false, [2]int{2, 3})
			draw.Draw(img, image.Rect(750, 50, 900, 150), image.NewUniform(c), image.Point{}, draw.Src)
			beads := sampleCalibratedSpecial(img, positions, area, config.Default().Vision, [2]ninja.Readout{id, id})
			for _, b := range beads[4:] {
				if !b.Unknown || b.Lit {
					t.Fatalf("flat effect became red beans: hint=%v %+v", hint, b)
				}
			}
		}
		img, positions := xiayinTestRows(false, [2]int{2, 3})
		draw.Draw(img, image.Rect(750, 95, 900, 106), image.NewUniform(color.RGBA{240, 95, 80, 255}), image.Point{}, draw.Src)
		for _, b := range sampleCalibratedSpecial(img, positions, area, config.Default().Vision, [2]ninja.Readout{id, id})[4:] {
			if !b.Unknown || b.Lit {
				t.Fatalf("thin red band became beans: hint=%v %+v", hint, b)
			}
		}
	}
	// One red sparkle cannot change an otherwise purple row's skin.
	img, positions := xiayinTestRows(false, [2]int{2, 3})
	p := positions[0]
	draw.Draw(img, image.Rect(p.X-4, p.Y-5, p.X+5, p.Y+6), image.NewUniform(color.RGBA{240, 95, 80, 255}), image.Point{}, draw.Src)
	if got := xiayinPalette(img, positions, "left", 4, 4); got != ninja.Purple {
		t.Fatalf("single sparkle selected %s", got)
	}
}
