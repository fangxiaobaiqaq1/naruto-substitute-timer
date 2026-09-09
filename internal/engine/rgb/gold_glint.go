package rgb

import (
	"image"
	"image/color"

	"narutotimer/internal/detect"
)

// goldGlintBody examines fixed upper/lower interior lobes of THIS calibrated
// slot, not a search for the brightest neighbor. The narrow white center can
// then vote without lowering the normal core confidence threshold. No history
// or other side/slot supplies evidence. All distances scale with the core.
func goldGlintBody(img *image.RGBA, p detect.BeadPosition, w, h int) bool {
	goldAt := func(x, y int) bool {
		if !image.Pt(x, y).In(img.Bounds()) {
			return false
		}
		c := img.RGBAAt(x, y)
		r, g, b := int(c.R), int(c.G), int(c.B)
		return r >= 190 && g >= 110 && r-b >= 80 && g-b >= 70
	}
	// A full-height gold/white wash has no separated bottom. The health bar
	// above must not provide the second lobe for an otherwise hidden bead.
	if !image.Pt(p.X, p.Y+2*h).In(img.Bounds()) || goldAt(p.X, p.Y+2*h) {
		return false
	}
	separated := false
	for _, sign := range []int{-1, 1} {
		consecutive, longest, distinct := 0, 0, 0
		// Integer resizing can put the outermost row on the dim rim and the
		// next inner row on the real gold body. Require contiguous body rows
		// within each fixed lobe, rather than a majority of a rim-heavy box.
		for dy := max(1, h/3); dy <= h; dy++ {
			y := p.Y + sign*dy
			gold := 0
			for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
				if goldAt(p.X+dx, y) {
					gold++
				}
			}
			if gold >= 2 {
				consecutive++
				longest = max(longest, consecutive)
			} else {
				consecutive = 0
			}
			// Compare the body to BOTH gaps on the same row. A moving sparkle
			// may cover the gaps in one lobe, but not erase all shape evidence.
			if !image.Pt(p.X-2*w, y).In(img.Bounds()) || !image.Pt(p.X+2*w, y).In(img.Bounds()) {
				return false
			}
			core := img.RGBAAt(p.X, y)
			left, right := img.RGBAAt(p.X-2*w, y), img.RGBAAt(p.X+2*w, y)
			// A white glint can brighten a gap's G as much as the filled gold
			// body. Yellow chroma still separates that body from the pale flare.
			yellow := func(c color.RGBA) int { return min(int(c.R), int(c.G)) - int(c.B) }
			distinctColor := yellow(core)-max(yellow(left), yellow(right)) >= 32
			if gold >= 2 && (int(core.G)-max(int(left.G), int(right.G)) >= 32 || distinctColor) {
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
