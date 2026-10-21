package detect

import (
	"image"
	"math"
)

// HUDReferenceWidth/Height are the 16:9 screenshot coordinates used by the
// shipped bead/name profiles. They are a screen-space calibration unit, not
// the scene/template reference (LogicWidth x LogicHeight). The game itself
// fits the HUD into a 16:9 reference: wide screens use height, while tablets
// use width. Widgets stay top/edge anchored; decorative borders are not a
// reason to crop the game surface or vertically center the HUD.
const (
	HUDReferenceWidth  = 1600.0
	HUDReferenceHeight = 900.0
)

// HUDScale keeps widgets proportional on both wide phones and narrow tablets.
// Letterboxed captures use the proven content area, not the surrounding bars.
func (ca ContentArea) HUDScale(referenceWidth, referenceHeight float64) float64 {
	if ca.W <= 0 || ca.H <= 0 || referenceWidth <= 0 || referenceHeight <= 0 {
		return 0
	}
	return math.Min(float64(ca.W)/referenceWidth, float64(ca.H)/referenceHeight)
}

// MapHUDPixel maps a point from the fitted HUD reference into the capture.
// Right-side points are measured from the right edge; this is the important
// difference from stretching a 16:9 screenshot across an ultrawide display.
func (ca ContentArea) MapHUDPixel(x, y float64, side string, referenceWidth, referenceHeight float64) image.Point {
	scale := ca.HUDScale(referenceWidth, referenceHeight)
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
