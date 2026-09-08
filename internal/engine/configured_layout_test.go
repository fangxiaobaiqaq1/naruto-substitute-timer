package engine

import (
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

func TestConfiguredLayoutMissingSlotsCannotSupplyPartialCalibration(t *testing.T) {
	cfg := config.Default().Layout
	p := cfg.Profile("camp")
	p.Right.NominalCenters = p.Right.NominalCenters[:3]
	cfg.Profiles = map[string]config.LayoutProfile{"camp": p}
	if got := NewConfiguredLayout(detect.ModeStretch, cfg).PositionsFor("camp", 1600, 900); got != nil {
		t.Fatalf("partial calibration must not supply beads: %+v", got)
	}
}
