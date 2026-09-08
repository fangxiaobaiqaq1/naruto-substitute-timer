package rgb

import (
	"image"

	"narutotimer/internal/detect"
)

// A translucent start/skill effect can move an EMPTY dark core into the RGB
// range of a blue available bean. The core is still a dim hollow relative to
// its surroundings. Verify local blue/white body prominence, not just color.
// Fixed offsets only: never search for a brighter neighbor or reuse old pixels.
func blueBodyVisible(img *image.RGBA, p detect.BeadPosition, w, h int) bool {
	radius := max(1, w/4)
	gap := max(3, 2*w)
	value := func(x, y int) (int, bool) {
		sum := 0
		for dx := -radius; dx <= radius; dx++ {
			q := image.Pt(x+dx, y)
			if !q.In(img.Bounds()) {
				return 0, false
			}
			c := img.RGBAAt(q.X, q.Y)
			// Retain white-core brightness while distinguishing blue from a
			// white background. Plain G+B alone can favor the bright scenery.
			sum += int(c.G) + int(c.B) - int(c.R)
		}
		return sum / (2*radius + 1), true
	}
	hits := 0
	for y := p.Y - h/2; y <= p.Y+h/2; y++ {
		core, ok := value(p.X, y)
		left, lok := value(p.X-gap, y)
		right, rok := value(p.X+gap, y)
		if !ok || !lok || !rok {
			return false
		}
		// One side may contain a neighboring bead's halo or a moving sparkle.
		// A filled core must still stand out from at least one gap on two
		// adjacent scanlines. A hollow/flat wash stands out from neither.
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
