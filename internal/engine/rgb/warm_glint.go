package rgb

import (
	"image"

	"narutotimer/internal/detect"
	"narutotimer/internal/ninja"
)

// A six-slot orange/red bean retains colored body lobes when its narrow idle
// sweep clips the center to white. Gold-only lobe checks cannot see that body.
// Require current color above AND below this fixed core, plus separation from
// both neighboring gaps on at least one lobe. No past count or adjacent bean
// supplies a vote; a white cover, hollow rim, or flat warm wash fails.
func warmGlintBody(img *image.RGBA, p detect.BeadPosition, w, h int) bool {
	type sample struct {
		warm          bool
		chroma, value int
	}
	at := func(x, y int) (sample, bool) {
		if !image.Pt(x, y).In(img.Bounds()) {
			return sample{}, false
		}
		c := img.RGBAAt(x, y)
		r, g, b := int(c.R), int(c.G), int(c.B)
		return sample{specialPixel(ninja.Warm, r, g, b) == detect.StateLight, r - b, r + g - b}, true
	}
	far := image.Pt(p.X, p.Y+2*h)
	body, ok := at(far.X, far.Y)
	if !ok || body.warm {
		return false
	}
	c := img.RGBAAt(far.X, far.Y)
	if c.R >= 235 && c.G >= 210 && c.B >= 210 {
		return false
	}
	separated := false
	for _, sign := range []int{-1, 1} {
		consecutive, longest, distinct := 0, 0, 0
		for dy := max(2, h/3); dy <= h; dy++ {
			y := p.Y + sign*dy
			filled := 0
			for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
				body, ok := at(p.X+dx, y)
				if ok && body.warm {
					filled++
				}
			}
			core, ok := at(p.X, y)
			left, leftOK := at(p.X-2*w, y)
			right, rightOK := at(p.X+2*w, y)
			if !ok || !leftOK || !rightOK {
				return false
			}
			if filled >= 2 && core.warm {
				consecutive++
				longest = max(longest, consecutive)
			} else {
				consecutive = 0
			}
			if filled >= 2 && core.warm && (core.chroma-max(left.chroma, right.chroma) >= 32 || core.value-max(left.value, right.value) >= 32) {
				distinct++
			}
		}
		if longest < max(2, h/3) {
			return false
		}
		separated = separated || distinct >= 2
	}
	return separated
}
