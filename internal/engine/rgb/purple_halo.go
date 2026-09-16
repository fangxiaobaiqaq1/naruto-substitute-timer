package rgb

import (
	"image"
	"math"

	"narutotimer/internal/detect"
	"narutotimer/internal/ninja"
)

// Xiayin Sasuke's energy bar above and idle glow below can trip both wash
// guards. A confirmed version may keep its saturated bean vote only if the
// current body is filled, brighter than BOTH inter-slot gaps on consecutive
// scanlines, and the glow fades further below. This does not count white cores
// or search for a brighter position. Flat bands and hollow rims fail.
func isolatedPurpleHalo(img *image.RGBA, p detect.BeadPosition, w, h, guard int) bool {
	far := image.Pt(p.X, p.Y+2*guard)
	if !far.In(img.Bounds()) {
		return false
	}
	c := img.RGBAAt(far.X, far.Y)
	if specialPixel(ninja.Purple, int(c.R), int(c.G), int(c.B)) == detect.StateLight || (c.R >= 235 && c.G >= 210 && c.B >= 235) {
		return false
	}
	brightness := func(x, y int) (int, bool) {
		if !image.Pt(x, y).In(img.Bounds()) {
			return 0, false
		}
		c := img.RGBAAt(x, y)
		// Purple saturates R/B during the glint; G preserves body contrast.
		return min(int(c.R), int(c.B)) + int(c.G), true
	}
	gap := max(2, int(math.Round(float64(guard)*.75)))
	consecutive := 0
	for dy := -h; dy <= h; dy++ {
		y := p.Y + dy
		filled := 0
		for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
			point := image.Pt(p.X+dx, y)
			if !point.In(img.Bounds()) {
				return false
			}
			c := img.RGBAAt(point.X, point.Y)
			if c.R >= 180 && c.B >= 210 && int(c.R)-int(c.G) >= 40 && int(c.B)-int(c.G) >= 55 {
				filled++
			}
		}
		core, ok := brightness(p.X, y)
		left, leftOK := brightness(p.X-gap, y)
		right, rightOK := brightness(p.X+gap, y)
		if filled == 3 && ok && leftOK && rightOK && core-max(left, right) >= 32 {
			consecutive++
			if consecutive >= max(2, h/3) {
				return true
			}
		} else {
			consecutive = 0
		}
	}
	return false
}
