package rgb

import (
	"image"

	"narutotimer/internal/detect"
)

// A glowing portrait can add warm light to a blue bean, removing the core's
// B-R chroma for seconds. Its lower CYAN interior remains visible. Require
// filled current pixels on adjacent rows and contrast against BOTH slot gaps;
// a pale core alone, an empty rim or a flat overlay cannot provide this proof.
func blueGlintBody(img *image.RGBA, p detect.BeadPosition, w, h, gap int) bool {
	cyanAt := func(x, y int) bool {
		if !image.Pt(x, y).In(img.Bounds()) {
			return false
		}
		c := img.RGBAAt(x, y)
		return c.G >= 150 && c.B >= 150 && int(c.B)-int(c.R) >= 20
	}
	contrast := newBodyContrast(max(2, h/3))
	for dy := max(1, h/3); dy <= h; dy++ {
		y := p.Y + dy
		if !image.Pt(p.X-gap, y).In(img.Bounds()) || !image.Pt(p.X+gap, y).In(img.Bounds()) {
			return false
		}
		filled := 0
		for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
			if cyanAt(p.X+dx, y) {
				filled++
			}
		}
		if filled < 2 || !cyanAt(p.X, y) {
			contrast.reset()
			continue
		}
		core, left, right := img.RGBAAt(p.X, y), img.RGBAAt(p.X-gap, y), img.RGBAAt(p.X+gap, y)
		v := int(min(core.G, core.B))
		if contrast.add(v-int(min(left.G, left.B)), v-int(min(right.G, right.B)), 32) {
			return true
		}
	}
	return false
}
