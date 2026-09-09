package rgb

import (
	"image"

	"narutotimer/internal/detect"
)

// The purple idle sparkle can clip the whole upper core to white. Require a
// filled, saturated lower body on this frame, separated from BOTH neighboring
// gaps and from the pixels below it. An empty rim, white cover, or retained name
// alone cannot supply the missing votes.
func purpleGlintBody(img *image.RGBA, p detect.BeadPosition, w, h int) bool {
	chroma := func(x, y int) (int, bool) {
		if !image.Pt(x, y).In(img.Bounds()) {
			return 0, false
		}
		c := img.RGBAAt(x, y)
		return min(int(c.R), int(c.B)) - int(c.G), c.R >= 180 && c.B >= 210 && int(c.R)-int(c.G) >= 40 && int(c.B)-int(c.G) >= 55
	}
	far := image.Pt(p.X, p.Y+2*h)
	if !far.In(img.Bounds()) {
		return false
	}
	c := img.RGBAAt(far.X, far.Y)
	if _, purple := chroma(far.X, far.Y); purple || (c.R >= 235 && c.G >= 210 && c.B >= 235) {
		return false
	}
	consecutive := 0
	for dy := max(2, h/3); dy <= h+max(1, h/3); dy++ {
		y := p.Y + dy
		filled := 0
		for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
			if _, purple := chroma(p.X+dx, y); purple {
				filled++
			}
		}
		if !image.Pt(p.X-2*w, y).In(img.Bounds()) || !image.Pt(p.X+2*w, y).In(img.Bounds()) {
			return false
		}
		core, _ := chroma(p.X, y)
		left, _ := chroma(p.X-2*w, y)
		right, _ := chroma(p.X+2*w, y)
		if filled == 3 && core-max(left, right) >= 32 {
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
