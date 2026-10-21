package detect

import (
	"image"
	"math"
)

// SideCalibration is one side's player-drawn geometry in one frame
// (docs/区域校准.md). Positions of a calibrated side carry it; nil keeps the
// automatic layout for that side.
type SideCalibration struct {
	// Bead row: 1600x900 reference (x, y) maps to
	// (X0+Scale·(x−RefX), Y0+Scale·(y−RefY)). Scale 0 = automatic beads.
	X0, Y0, Scale, RefX, RefY float64
	// Avatar is the portrait box in frame pixels for this frame's profile;
	// an empty box keeps the automatic portrait window.
	Avatar PixelBox
}

// PixelBox is an unrounded rectangle in frame pixels.
type PixelBox struct{ X, Y, W, H float64 }

func (b PixelBox) Empty() bool { return !(b.W > 0 && b.H > 0) }

// Beads reports a calibrated bead row.
func (c *SideCalibration) Beads() bool { return c != nil && c.Scale > 0 }

// Portrait reports a calibrated portrait box.
func (c *SideCalibration) Portrait() bool { return c != nil && !c.Avatar.Empty() }

// Map maps a 1600x900 HUD reference point of a calibrated bead row.
func (c *SideCalibration) Map(x, y float64) image.Point {
	return image.Pt(int(math.Round(c.X0+c.Scale*(x-c.RefX))), int(math.Round(c.Y0+c.Scale*(y-c.RefY))))
}

// RowHUDScale is a side's HUD scale for a reference size: the calibrated
// bead scale on a calibrated row, else the content-area fit.
func RowHUDScale(row []BeadPosition, ca ContentArea, referenceWidth, referenceHeight float64) float64 {
	if len(row) > 0 && row[0].Cal.Beads() {
		return row[0].Cal.Scale * HUDReferenceWidth / referenceWidth
	}
	return ca.HUDScale(referenceWidth, referenceHeight)
}
