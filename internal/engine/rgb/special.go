package rgb

import (
	"image"
	"math"
	"time"

	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

func (e *Engine) specialPositions(img *image.RGBA, positions []detect.BeadPosition, area detect.ContentArea) ([]detect.BeadPosition, [2]ninja.Readout) {
	return e.specialPositionsFor("camp", img, positions, area)
}

func (e *Engine) specialPositionsFor(profile string, img *image.RGBA, positions []detect.BeadPosition, area detect.ContentArea) ([]detect.BeadPosition, [2]ninja.Readout) {
	return e.specialPositionsForAt(profile, img, positions, area, time.Now())
}

func (e *Engine) specialPositionsForAt(profile string, img *image.RGBA, positions []detect.BeadPosition, area detect.ContentArea, at time.Time) ([]detect.BeadPosition, [2]ninja.Readout) {
	profileIndex := 0
	if profile == "duel" {
		profileIndex = 1
	}
	var identified [2]ninja.Readout
	var out []detect.BeadPosition
	for index, side := range []string{"left", "right"} {
		var row []detect.BeadPosition
		for _, p := range positions {
			if p.Side == side {
				row = append(row, p)
			}
		}
		if len(row) >= 2 {
			// Stored name strips use the 960-wide HUD reference. Individual
			// integer-rounded bean centers may alternate 15/16px; their first
			// interval must not accidentally stretch all of the name lettering.
			scale := float64(area.W) / 960
			first := image.Pt(row[0].X, row[0].Y)
			identified[index] = e.nameTracker[profileIndex][index].Read(e.names, img, ninja.NameRegion(first, scale, index == 0), scale, at)
			// Only a recognized six-slot NAME permits usable slots five and six.
			// A brief label gap retains six UNKNOWN placeholders, never extra votes.
			if len(row) == 4 && identified[index].Slots == 6 {
				dx, dy := 30.0, 0.0
				if row[1].LX < row[0].LX {
					dx = -dx
				}
				for i := 4; i < 6; i++ {
					lx, ly := row[0].LX+float64(i)*dx, row[0].LY+float64(i)*dy
					row = append(row, detect.BeadPosition{Bead: detect.Bead{Side: side, Idx: i, LX: lx, LY: ly}, X: area.X + int(math.Round(lx*float64(area.W)/detect.LogicWidth)), Y: area.Y + int(math.Round(ly*float64(area.H)/detect.LogicHeight))})
				}
			}
		}
		out = append(out, row...)
	}
	return out, identified
}

func applyNames(res *engine.Result, readouts [2]ninja.Readout) {
	res.LeftNinja, res.RightNinja = readouts[0].Name, readouts[1].Name
	for _, b := range res.Beads {
		if len(b.Label) > 0 && b.Label[0] == 'L' {
			res.LeftSlots++
		} else if len(b.Label) > 0 && b.Label[0] == 'R' {
			res.RightSlots++
		}
	}
}

func specialPixel(palette ninja.Palette, r, g, b int) detect.BeadState {
	switch palette {
	case ninja.Warm:
		// Six-slot end flares become pale yellow when scaled, while retaining
		// clear yellow chroma. Do not require a saturated-red center in that slot.
		if r >= 225 && g >= 175 && r-b >= 55 && g-b >= 45 {
			return detect.StateLight
		}
		if r >= 145 && r-g >= 50 && r-b >= 45 {
			return detect.StateLight
		}
		// Yellow/gold and blue continue through the ordinary calibrated path.
	case ninja.Purple:
		if r >= 100 && b >= 165 && r-g >= 40 && b-g >= 55 {
			return detect.StateLight
		}
		if r >= 200 && b >= 225 && b-g >= 25 && r-g >= 15 {
			return detect.StateLight
		}
		if r <= 85 && g <= 55 && b <= 125 && b-r >= 10 && b-g >= 15 {
			return detect.StateDark
		}
	case ninja.Red:
		if r >= 130 && r-g >= 65 && r-b >= 55 {
			return detect.StateLight
		}
		if r <= 85 && g <= 50 && b <= 55 && r-g >= 12 && r-b >= 12 {
			return detect.StateDark
		}
	}
	return detect.StateUnknown
}

// A broad colored wash is not a row of beans. Strong special-color pixels
// above AND below the body must be treated as obscured rather than six votes.
func specialWash(img *image.RGBA, p detect.BeadPosition, palette ninja.Palette, guard int) bool {
	if palette == "" {
		return false
	}
	for _, dy := range []int{-guard, guard} {
		point := image.Pt(p.X, p.Y+dy)
		if !point.In(img.Bounds()) {
			return false
		}
		c := img.RGBAAt(point.X, point.Y)
		if specialPixel(palette, int(c.R), int(c.G), int(c.B)) != detect.StateLight {
			return false
		}
	}
	if palette == ninja.Red && isolatedRedHalo(img, p, guard) {
		return false
	}
	return true
}

// Read only the lower portion of this calibrated body, without moving the core
// to a brighter pixel. Empty red beans have dim shoulders even during rim glow.
func redLowerBody(img *image.RGBA, p detect.BeadPosition, w, h int) bool {
	red, total := 0, 0
	for dy := h/2 + 1; dy <= h; dy++ {
		for _, dx := range []int{-max(1, w/3), 0, max(1, w/3)} {
			point := image.Pt(p.X+dx, p.Y+dy)
			if !point.In(img.Bounds()) {
				return false
			}
			c := img.RGBAAt(point.X, point.Y)
			total++
			if specialPixel(ninja.Red, int(c.R), int(c.G), int(c.B)) == detect.StateLight {
				red++
			}
		}
	}
	return total > 0 && red*3 >= total*2
}

// A red health bar above and this skin's short halo below may trip the normal
// wash guard. Accept that case only with a distinct core against BOTH slot gaps
// and a halo that has already faded further below. A flat red/white band still
// fails; this never enlarges the sample core or relaxes ordinary/purple skins.
func isolatedRedHalo(img *image.RGBA, p detect.BeadPosition, guard int) bool {
	far := image.Pt(p.X, p.Y+2*guard)
	if !far.In(img.Bounds()) {
		return false
	}
	c := img.RGBAAt(far.X, far.Y)
	if specialPixel(ninja.Red, int(c.R), int(c.G), int(c.B)) == detect.StateLight {
		return false
	}
	radius := max(1, guard/8)
	chromaLight := func(x, y int) (float64, bool) {
		sum, n := 0, 0
		for dx := -radius; dx <= radius; dx++ {
			point := image.Pt(x+dx, y)
			if !point.In(img.Bounds()) {
				return 0, false
			}
			c := img.RGBAAt(point.X, point.Y)
			// R is saturated across the halo; G+B distinguishes the hot body.
			sum += int(c.G) + int(c.B)
			n++
		}
		return float64(sum) / float64(n), true
	}
	gap := max(2, int(math.Round(float64(guard)*.75)))
	halfBody, consecutive := max(2, guard*3/8), 0
	// The glint travels horizontally across the midpoint. Verify the bounded
	// body's shape on two adjacent scanlines instead of treating one bright gap
	// at the midpoint as proof that the entire body is an obscuring wash.
	for y := p.Y - halfBody; y <= p.Y+halfBody; y++ {
		core, ok := chromaLight(p.X, y)
		left, leftOK := chromaLight(p.X-gap, y)
		right, rightOK := chromaLight(p.X+gap, y)
		if ok && leftOK && rightOK && core-max(left, right) >= 32 {
			consecutive++
			if consecutive >= 2 {
				return true
			}
		} else {
			consecutive = 0
		}
	}
	return false
}
