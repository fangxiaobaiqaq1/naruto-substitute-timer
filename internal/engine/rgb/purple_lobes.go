package rgb

import (
	"image"

	"narutotimer/internal/detect"
	"narutotimer/internal/ninja"
)

// A white idle sweep crosses the middle of the diamond. A damage number may
// simultaneously obscure the distant background probe used by purpleGlintBody.
// In that case require BOTH saturated upper and lower interiors, each separated
// from BOTH slot gaps on adjacent scanlines. The extra independent upper proof
// permits weaker (20/255) local contrast, not a lone lower patch under a cover.
func purplePairedBody(img *image.RGBA, p detect.BeadPosition, w, h, gap int) bool {
	far := image.Pt(p.X, p.Y+4*h)
	if !far.In(img.Bounds()) {
		return false
	}
	c := img.RGBAAt(far.X, far.Y)
	if specialPixel(ninja.Purple, int(c.R), int(c.G), int(c.B)) == detect.StateLight {
		return false
	}
	signal := func(x, y int) ([3]int, bool) {
		c := img.RGBAAt(x, y)
		return [3]int{min(int(c.R), int(c.B)) - int(c.G), min(int(c.R), int(c.B)) + int(c.G), int(c.B) - int(c.G)}, c.R >= 180 && c.B >= 210 && int(c.R)-int(c.G) >= 40 && int(c.B)-int(c.G) >= 55
	}
	lobe := func(lo, hi int) bool {
		contrast := newBodyContrast(max(2, h/3))
		for dy := lo; dy <= hi; dy++ {
			y := p.Y + dy
			if !image.Pt(p.X-gap, y).In(img.Bounds()) || !image.Pt(p.X+gap, y).In(img.Bounds()) {
				return false
			}
			filled := 0
			for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
				if _, ok := signal(p.X+dx, y); ok {
					filled++
				}
			}
			core, ok := signal(p.X, y)
			if !ok || filled < 2 {
				contrast.reset()
				continue
			}
			left, _ := signal(p.X-gap, y)
			right, _ := signal(p.X+gap, y)
			lc, rc := core[0]-left[0], core[0]-right[0]
			for k := 1; k < len(core); k++ {
				lc, rc = max(lc, core[k]-left[k]), max(rc, core[k]-right[k])
			}
			if contrast.add(lc, rc, 20) {
				return true
			}
		}
		return false
	}
	extent := h + max(1, h/2)
	return lobe(-extent, -max(2, h/2)) && lobe(max(2, h/2+1), extent)
}
