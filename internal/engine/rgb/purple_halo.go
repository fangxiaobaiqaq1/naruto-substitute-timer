package rgb

import (
	"image"

	"narutotimer/internal/detect"
	"narutotimer/internal/ninja"
)

// Xiayin Sasuke's energy bar above and idle glow below can trip both wash
// guards. A confirmed version may keep its saturated bean vote only if the
// current body is filled, distinct from BOTH inter-slot gaps on consecutive
// scanlines, and the purple glow fades further below. This does not count white cores
// or search for a brighter position. Flat bands and hollow rims fail.
func isolatedPurpleHalo(img *image.RGBA, p detect.BeadPosition, w, h, guard, gap int) bool {
	far := image.Pt(p.X, p.Y+2*guard)
	if !far.In(img.Bounds()) {
		return false
	}
	c := img.RGBAAt(far.X, far.Y)
	// White damage numbers can pass BELOW an intact saturated bean. They do
	// not invalidate its current body; white cores still receive no votes here.
	if specialPixel(ninja.Purple, int(c.R), int(c.G), int(c.B)) == detect.StateLight {
		return false
	}
	signal := func(x, y int) ([3]int, bool) {
		if !image.Pt(x, y).In(img.Bounds()) {
			return [3]int{}, false
		}
		c := img.RGBAAt(x, y)
		// Bright scenery can exceed the purple body's luminance, while an
		// energy-bar halo saturates R. Keep independent value/chroma channels.
		return [3]int{min(int(c.R), int(c.B)) + int(c.G), min(int(c.R), int(c.B)) - int(c.G), int(c.B) - int(c.G)}, true
	}
	contrast := newBodyContrast(max(2, h/3))
	for dy := -h; dy <= h; dy++ {
		y := p.Y + dy
		filled := 0
		for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
			point := image.Pt(p.X+dx, y)
			if !point.In(img.Bounds()) {
				return false
			}
			c := img.RGBAAt(point.X, point.Y)
			if specialPixel(ninja.Purple, int(c.R), int(c.G), int(c.B)) == detect.StateLight {
				filled++
			}
		}
		core, ok := signal(p.X, y)
		left, leftOK := signal(p.X-gap, y)
		right, rightOK := signal(p.X+gap, y)
		c := img.RGBAAt(p.X, y)
		if filled >= 2 && specialPixel(ninja.Purple, int(c.R), int(c.G), int(c.B)) == detect.StateLight && ok && leftOK && rightOK {
			lc, rc := core[0]-left[0], core[0]-right[0]
			for channel := 1; channel < len(core); channel++ {
				lc, rc = max(lc, core[channel]-left[channel]), max(rc, core[channel]-right[channel])
			}
			if contrast.add(lc, rc, 32) {
				return true
			}
		} else {
			contrast.reset()
		}
	}
	return false
}
