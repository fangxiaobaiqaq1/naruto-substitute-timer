package engine

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
)

func TestConfiguredLayoutOwnsIndependentProfileCoordinates(t *testing.T) {
	cfg := config.Default().Layout
	camp, duel := cfg.Profile("camp"), cfg.Profile("duel")
	camp.Left.NominalCenters[0] = config.NormalizedPoint{X: .1, Y: .2}
	duel.Left.NominalCenters[0] = config.NormalizedPoint{X: .3, Y: .4}
	cfg.Profiles = map[string]config.LayoutProfile{"camp": camp, "duel": duel}
	cfg.PreferredProfile = "duel"
	layout := NewConfiguredLayout(detect.ModeStretch, cfg)
	// Editing a config after construction must not silently recalibrate an engine.
	camp.Left.NominalCenters[0] = config.NormalizedPoint{X: .9, Y: .9}
	duel.Left.NominalCenters[0] = config.NormalizedPoint{X: .9, Y: .9}
	for _, tc := range []struct {
		name string
		x, y int
	}{{"camp", 160, 180}, {"duel", 480, 360}} {
		got := layout.PositionsFor(tc.name, 1600, 900)
		if len(got) != cfg.BeadsPerSide*2 || got[0].X != tc.x || got[0].Y != tc.y {
			t.Fatalf("profile %s coordinates changed: %+v", tc.name, got)
		}
	}
	if layout.PreferredProfile() != "duel" || layout.Positions(1600, 900)[0].X != 480 {
		t.Fatal("configured manual profile was not retained")
	}
	if got := layout.PositionsFor("unknown", 1600, 900); got != nil {
		t.Fatalf("unknown profile synthesized coordinates: %+v", got)
	}
}

func TestConfiguredLayoutWideAnchorsPreserveExplicitCalibration(t *testing.T) {
	cfg := config.Default().Layout
	img := image.NewRGBA(image.Rect(0, 0, 2400, 1080))
	// Only the left health-bar end is visible. Do not mirror it to the right.
	for y := 88; y <= 90; y++ {
		draw.Draw(img, image.Rect(314-(y-89), y, 600, y+1), image.NewUniform(color.RGBA{240, 70, 10, 255}), image.Point{}, draw.Src)
	}
	for _, custom := range []bool{false, true} {
		if custom {
			cfg.Left.NominalCenters[0].X += .001
		}
		l := NewConfiguredLayout(detect.ModeStretch, cfg)
		area, _ := l.ContentArea(img)
		before := l.PositionsIn("duel", area)
		after := l.PositionsInImage("duel", img, area)
		for i, b := range after {
			wantX := before[i].X
			if !custom && b.Side == "left" {
				wantX += 314 - before[0].X
			}
			if b.X != wantX || b.Y != before[i].Y || b.Bead != before[i].Bead {
				t.Fatalf("custom=%v: %v => %v, want x=%d and unchanged calibration", custom, before[i], b, wantX)
			}
		}
	}
}

func TestConfiguredLayoutMissingSlotsCannotSupplyPartialCalibration(t *testing.T) {
	cfg := config.Default().Layout
	p := cfg.Profile("camp")
	p.Right.NominalCenters = p.Right.NominalCenters[:3]
	cfg.Profiles = map[string]config.LayoutProfile{"camp": p}
	if got := NewConfiguredLayout(detect.ModeStretch, cfg).PositionsFor("camp", 1600, 900); got != nil {
		t.Fatalf("partial calibration must not supply beads: %+v", got)
	}
}
