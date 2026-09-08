package rgb

import (
	"image"

	"narutotimer/internal/detect"
)

// The ordinary saturated-gold branch also needs a spatially distinct body.
// Otherwise a flat yellow skill overlay can turn an empty blue slot into a
// gold recovery. Sample fixed core/gap rows only, never follow a bright rim.
// White glints use the independently checked upper/lower gold lobes instead.
func goldBodyVisible(img *image.RGBA, p detect.BeadPosition, w, h int) bool {
	radius, gap := max(1, w/4), max(3, 2*w)
	value := func(x, y int) (int, bool) {
		sum := 0
		for dx := -radius; dx <= radius; dx++ {
			q := image.Pt(x+dx, y)
			if !q.In(img.Bounds()) {
				return 0, false
			}
			c := img.RGBAAt(q.X, q.Y)
			sum += int(c.R) + int(c.G) - int(c.B)
		}
		return sum / (2*radius + 1), true
	}
	hits := 0
	// A neighboring sparkle can cover the core's horizontal mid-band. The
	// upper/lower body remains inside this fixed slot; it is not a relocation.
	for y := p.Y - h; y <= p.Y+h; y++ {
		core, ok := value(p.X, y)
		left, lok := value(p.X-gap, y)
		right, rok := value(p.X+gap, y)
		if !ok || !lok || !rok {
			return false
		}
		if core-min(left, right) >= 8 {
			hits++
			if hits >= 2 {
				return true
			}
		} else {
			hits = 0
		}
	}
	return false
}
