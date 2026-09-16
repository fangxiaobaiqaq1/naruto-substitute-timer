package rgb

import (
	"image"

	"narutotimer/internal/detect"
	"narutotimer/internal/ninja"
)

// A horizontal idle sweep can turn an empty blue/purple/red core pale without
// filling the slot. Require dark interior on BOTH sides of that stripe in the
// current frame, bounded by a brighter rim on both sides. Never reuse a count
// or take a neighbor's rim as the missing core. Whole covers and flat bands fail.
func darkGlintBody(img *image.RGBA, p detect.BeadPosition, w, h int, palette ninja.Palette) bool {
	if img == nil || w < 2 || h < 3 {
		return false
	}
	darkAt := func(x, y int) bool {
		if !image.Pt(x, y).In(img.Bounds()) {
			return false
		}
		c := img.RGBAAt(x, y)
		r, g, b := int(c.R), int(c.G), int(c.B)
		return (detect.DarkRange.Contains(r, g, b) && b-r >= 15 && b-g >= 8) || specialPixel(palette, r, g, b) == detect.StateDark
	}
	rim := w + max(1, w/4)
	for _, sign := range []int{-1, 1} {
		contrast := newBodyContrast(max(2, h/3))
		proved := false
		for dy := max(2, h/3); dy <= h; dy++ {
			y := p.Y + sign*dy
			if !image.Pt(p.X-rim, y).In(img.Bounds()) || !image.Pt(p.X+rim, y).In(img.Bounds()) {
				return false
			}
			filled := 0
			for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
				if darkAt(p.X+dx, y) {
					filled++
				}
			}
			core, left, right := img.RGBAAt(p.X, y), img.RGBAAt(p.X-rim, y), img.RGBAAt(p.X+rim, y)
			value := int(max(core.R, core.G, core.B))
			if filled >= 2 && darkAt(p.X, y) {
				if contrast.add(int(max(left.R, left.G, left.B))-value, int(max(right.R, right.G, right.B))-value, 24) {
					proved = true
					break
				}
			} else {
				contrast.reset()
			}
		}
		if !proved {
			return false
		}
	}
	return true
}
