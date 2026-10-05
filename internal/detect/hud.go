package detect

import (
	"image"
	"math"
)

// HUDReferenceWidth/Height are the 16:9 screenshot coordinates used by the
// shipped bead/name profiles. They are a screen-space calibration unit, not
// the scene/template reference (LogicWidth x LogicHeight). The game itself
// uses an NGUI fixed-height root: on a native full-screen HUD, physical UI
// size follows screen height, while the left and right margins stay anchored
// to their respective edges.
const (
	HUDReferenceWidth  = 1600.0
	HUDReferenceHeight = 900.0
)

// HeightScale returns the NGUI physical scale for coordinates measured at a
// reference height. Letterboxed content uses the content height, not the
// surrounding client height.
func (ca ContentArea) HeightScale(referenceHeight float64) float64 {
	if ca.H <= 0 || referenceHeight <= 0 {
		return 0
	}
	return float64(ca.H) / referenceHeight
}

// MapHUDPixel maps a point from a height-scaled HUD reference into the capture.
// Right-side points are measured from the right edge; this is the important
// difference from stretching a 16:9 screenshot across an ultrawide display.
func (ca ContentArea) MapHUDPixel(x, y float64, side string, referenceWidth, referenceHeight float64) image.Point {
	scale := ca.HeightScale(referenceHeight)
	if scale <= 0 || referenceWidth <= 0 {
		return image.Pt(ca.X, ca.Y)
	}
	var px float64
	if side == "right" {
		px = float64(ca.X+ca.W) - (referenceWidth-x)*scale
	} else {
		px = float64(ca.X) + x*scale
	}
	py := float64(ca.Y) + y*scale
	return image.Pt(int(math.Round(px)), int(math.Round(py)))
}

// MapHUDNormalized is MapHUDPixel for normalized reference coordinates.
func (ca ContentArea) MapHUDNormalized(x, y float64, side string, referenceWidth, referenceHeight float64) image.Point {
	return ca.MapHUDPixel(x*referenceWidth, y*referenceHeight, side, referenceWidth, referenceHeight)
}
