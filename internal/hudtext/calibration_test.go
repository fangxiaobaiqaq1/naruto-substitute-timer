package hudtext

import (
	"image"
	"math"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/engine"
)

type calibratedFake struct {
	fakeEngine
	got      *config.RegionCalibration
	revision uint64
}

func (f *calibratedFake) SetCalibration(c *config.RegionCalibration) { f.got = c; f.revision++ }
func (f *calibratedFake) CalibrationRevision() uint64                { return f.revision }

// OCR name strips follow a calibrated bead row: its own scale and vertical
// placement. The uncalibrated side keeps its strip; SetCalibration is live.
func TestNameRegionsFollowRegionCalibration(t *testing.T) {
	cfg := config.Default()
	img := image.NewRGBA(image.Rect(0, 0, 2388, 1668))
	before, ok := nameRegions(img, cfg.Layout, "duel")
	if !ok {
		t.Fatal("automatic regions")
	}
	// Left beads drawn at 1.7 px per reference unit, 250 px down.
	const s = 1.7
	pitch := 77.0 / 3 * s
	box := &config.NormalizedRect{X: (330 - pitch/2) / 2388, Y: (250 - 10) / 1668.0, Width: 4 * pitch / 2388, Height: 20 / 1668.0}
	inner := &calibratedFake{}
	e := New(inner, cfg, &fakeReader{called: make(chan struct{}), replies: make(chan reply), closed: make(chan struct{})}, nil)
	defer e.Close()
	e.SetCalibration(&config.RegionCalibration{Profile: "duel", LeftBeads: box})
	if inner.got == nil || inner.got.LeftBeads == box || *inner.got.LeftBeads != *box || e.CalibrationRevision() != 1 {
		t.Fatalf("calibration not forwarded as a copy: %+v", inner.got)
	}
	after, ok := nameRegions(img, e.cfg.Layout, "duel")
	if !ok {
		t.Fatal("calibrated regions")
	}
	scale := s * 1600 / 960
	round := func(v float64) int { return int(math.Round(v)) }
	want := image.Rect(330+round(-4*scale), round(250+s*(.020*900-103)), 330+round(330*scale), round(250+s*(.073*900-103)))
	if after[0] != want || after[1] != before[1] {
		t.Fatalf("regions %v, want left %v and right unchanged %v", after, want, before[1])
	}
	var _ engine.Calibrator = e
}
