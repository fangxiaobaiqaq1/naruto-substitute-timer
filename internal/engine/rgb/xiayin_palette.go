package rgb

import (
	"image"

	"narutotimer/internal/detect"
	"narutotimer/internal/ninja"
)

// The exact Xiayin variant can render purple OR red, including a purple/red
// mirror match. Do not infer skin from screen side, player identity or the other
// row. Resolve only this frame's calibrated cores; retain no color/count history.
// Red needs two independently readable cores and no readable purple core, so a
// single red sparkle on an otherwise purple row cannot change its classifier.
func xiayinPalette(img *image.RGBA, positions []detect.BeadPosition, side string, w, h int) ninja.Palette {
	redSlots, purpleSlots := 0, 0
	for _, p := range positions {
		if p.Side != side {
			continue
		}
		red, purple, total := 0, 0, 0
		for dy := -h / 2; dy <= h/2; dy++ {
			for dx := -w / 2; dx <= w/2; dx++ {
				if !detect.InBeadDiamond(0, 0, w, h, dx, dy) {
					continue
				}
				total++
				q := image.Pt(p.X+dx, p.Y+dy)
				if !q.In(img.Bounds()) {
					continue
				}
				c := img.RGBAAt(q.X, q.Y)
				if specialPixel(ninja.Red, int(c.R), int(c.G), int(c.B)) != detect.StateUnknown {
					red++
				}
				if specialPixel(ninja.Purple, int(c.R), int(c.G), int(c.B)) != detect.StateUnknown {
					purple++
				}
			}
		}
		if total > 0 && red*5 >= total*3 {
			redSlots++
		}
		if total > 0 && purple*5 >= total*3 {
			purpleSlots++
		}
	}
	if redSlots >= 2 && purpleSlots == 0 {
		return ninja.Red
	}
	return ninja.Purple
}

// Only the bounded hint of this dual-skin variant permits current RED votes
// during a name gap. Unlike a verified identity it cannot turn white highlights
// into red beans. A lit core also needs a bounded body against both slot gaps.
func sampleUnverifiedXiayinRed(img *image.RGBA, p detect.BeadPosition, w, h, guard int) (detect.BeadState, float64) {
	light, dark, total := 0, 0, 0
	for dy := -h / 2; dy <= h/2; dy++ {
		for dx := -w / 2; dx <= w/2; dx++ {
			if !detect.InBeadDiamond(0, 0, w, h, dx, dy) {
				continue
			}
			total++
			q := image.Pt(p.X+dx, p.Y+dy)
			if !q.In(img.Bounds()) {
				continue
			}
			c := img.RGBAAt(q.X, q.Y)
			switch specialPixel(ninja.Red, int(c.R), int(c.G), int(c.B)) {
			case detect.StateLight:
				light++
			case detect.StateDark:
				dark++
			}
		}
	}
	if total == 0 {
		return detect.StateUnknown, 0
	}
	if dark*5 >= total*3 {
		return detect.StateDark, float64(dark) / float64(total)
	}
	if light*5 >= total*3 && isolatedRedHalo(img, p, guard) {
		return detect.StateLight, float64(light) / float64(total)
	}
	return detect.StateUnknown, float64(max(light, dark)) / float64(total)
}
