package rgb

import (
	"image"

	"narutotimer/internal/detect"
)

// The purple idle sparkle can clip the whole upper core to white. Require a
// filled, saturated lower body on this frame, separated from BOTH neighboring
// gaps and from the pixels below it. An empty rim, white cover, or retained name
// alone cannot supply the missing votes.
func purpleGlintBody(img *image.RGBA, p detect.BeadPosition, w, h, gap int) bool {
	chroma := func(x, y int) (int, int, bool) {
		if !image.Pt(x, y).In(img.Bounds()) {
			return 0, 0, false
		}
		c := img.RGBAAt(x, y)
		return min(int(c.R), int(c.B)) - int(c.G), min(int(c.R), int(c.B)) + int(c.G), c.R >= 180 && c.B >= 210 && int(c.R)-int(c.G) >= 40 && int(c.B)-int(c.G) >= 55
	}
	// Two core heights can still land on the sprite's lower sparkle. Check
	// outside that halo; the interior must independently pass both slot gaps.
	far := image.Pt(p.X, p.Y+3*h)
	if !far.In(img.Bounds()) {
		return false
	}
	c := img.RGBAAt(far.X, far.Y)
	if _, _, purple := chroma(far.X, far.Y); purple || (c.R >= 235 && c.G >= 210 && c.B >= 235) {
		return false
	}
	contrast := newBodyContrast(max(2, h/3))
	for dy := max(2, h/3); dy <= h+max(1, h/2); dy++ {
		y := p.Y + dy
		filled := 0
		for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
			if _, _, purple := chroma(p.X+dx, y); purple {
				filled++
			}
		}
		if !image.Pt(p.X-gap, y).In(img.Bounds()) || !image.Pt(p.X+gap, y).In(img.Bounds()) {
			return false
		}
		core, value, centerPurple := chroma(p.X, y)
		left, leftValue, _ := chroma(p.X-gap, y)
		right, rightValue, _ := chroma(p.X+gap, y)
		// A moving flare may desaturate one gap and brighten the other. Both
		// gaps must remain separated, but each can use its surviving channel.
		// The diamond narrows towards its tip: require its center and at least
		// one interior shoulder, not three pixels across an already narrow row.
		if filled >= 2 && centerPurple {
			if contrast.add(max(core-left, value-leftValue), max(core-right, value-rightValue), 32) {
				return true
			}
		} else {
			contrast.reset()
		}
	}
	return false
}
