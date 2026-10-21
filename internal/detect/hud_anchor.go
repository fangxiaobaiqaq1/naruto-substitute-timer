package detect

import (
	"image"
	"math"
)

// HUDRowOffset measures a wide-screen HUD's horizontal safe-area adjustment
// from the OUTER sloping edge of its health bar, never from bean brightness.
// Three adjacent scanlines must agree on a long bar and its diagonal end.
// A purple idle flare may tint the red HP bar; blue-dominant effects are not
// an anchor. This changes geometry only, never a bean's palette or state.
// Missing/covered bars leave the original calibration alone. The two sides are
// independent: training controls and device safe areas need not be symmetric.
func HUDRowOffset(img *image.RGBA, ca ContentArea, first image.Point, right bool, profile string) int {
	if img == nil || ca.H <= 0 || float64(ca.W)/float64(ca.H) < 1.9 {
		return 0
	}
	scale := ca.HUDScale(HUDReferenceWidth, HUDReferenceHeight)
	y := 74.0 // duel health-bar interior in the 1600x900 reference
	if profile == "camp" {
		y = 70
	}
	cy := ca.Y + int(math.Round(y*scale))
	step := max(1, int(math.Round(scale)))
	limit := int(math.Round(160 * scale))
	x0, x1 := ca.X, ca.X+ca.W/3
	if right {
		x0, x1 = ca.X+ca.W*2/3, ca.X+ca.W
	}
	red := func(x, y int) bool {
		if !image.Pt(x, y).In(img.Bounds()) {
			return false
		}
		c := img.RGBAAt(x, y)
		return c.R >= 150 && int(c.R)-int(c.G) >= 65 && c.G >= 30 && int(c.B)-int(c.R) <= 20
	}
	var edges [3]int
	for row := range edges {
		y := cy + (row-1)*step
		bestLength, edge := 0, -1
		for x := x0; x < x1; {
			if !red(x, y) {
				x++
				continue
			}
			start := x
			for x < x1 && red(x, y) {
				x++
			}
			length, candidate := x-start, start
			if right {
				candidate = x - 1
			}
			if float64(length) < 60*scale || float64(length) > 330*scale || abs(candidate-first.X) > limit {
				continue
			}
			if length > bestLength {
				bestLength, edge = length, candidate
			}
		}
		if edge < 0 {
			return 0
		}
		edges[row] = edge
	}
	direction := -1
	if right {
		direction = 1
	}
	for i := 1; i < len(edges); i++ {
		slope := direction * (edges[i] - edges[i-1])
		if slope < 0 || slope > 3*step {
			return 0
		}
	}
	if direction*(edges[2]-edges[0]) < step {
		return 0 // A flat red band/effect is not a HUD end cap.
	}
	delta := edges[1] - first.X
	if float64(abs(delta)) <= 8*scale {
		return 0 // Preserve an already aligned, hand-calibrated core.
	}
	return delta
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
