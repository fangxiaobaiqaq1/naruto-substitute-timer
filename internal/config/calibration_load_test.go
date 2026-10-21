package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRawConfig writes cfg as config.json without Validate (Save refuses
// what versions before the size limits could save).
func writeRawConfig(t *testing.T, cfg Config) string {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A config.json saved by v0.24beta/v0.25 may hold a calibration box outside
// the size limits. The engine never uses it, so the timer must still start:
// Load drops the box (that region is automatic) and says so, instead of
// refusing the whole file.
func TestLoadDropsSavedCalibrationBoxesOutsideSizeLimits(t *testing.T) {
	cfg := Default()
	usable := NormalizedRect{X: 0.62, Y: 0.05, Width: 0.0625, Height: 1.0 / 36}
	cfg.Layout.Calibration = &RegionCalibration{Profile: "duel",
		LeftAvatar: &NormalizedRect{X: 0, Y: 0, Width: 0.5, Height: 0.95}, // 7 times the automatic box
		RightBeads: &usable}
	path := writeRawConfig(t, cfg)

	got, notes, err := LoadWithNotes(path)
	if err != nil {
		t.Fatalf("LoadWithNotes refused a config the engine can run: %v", err)
	}
	cal := got.Layout.Calibration
	if cal == nil || cal.LeftAvatar != nil || cal.RightBeads == nil || *cal.RightBeads != usable || cal.Profile != "duel" {
		t.Fatalf("calibration after load = %+v, want only the usable right bead box", cal)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "layout.calibration.leftAvatar") ||
		!strings.Contains(notes[0], "太大") || !strings.Contains(notes[0], "按自动识别") {
		t.Fatalf("notes = %q, want one warning naming leftAvatar", notes)
	}
	if err := Validate(got); err != nil {
		t.Fatalf("repaired config does not validate: %v", err)
	}
	// Load (tools) and LoadOrCreate (the timer) accept it the same way.
	if got, err := Load(path); err != nil || got.Layout.Calibration.LeftAvatar != nil {
		t.Fatalf("Load = %+v, %v", got.Layout.Calibration, err)
	}
	if got, notes, err := LoadOrCreateWithNotes(path); err != nil || got.Layout.Calibration.LeftAvatar != nil || len(notes) != 1 {
		t.Fatalf("LoadOrCreateWithNotes = %+v, %q, %v", got.Layout.Calibration, notes, err)
	}
	// The file itself is left as it was; the next settings save stores the repair.
	if raw, err := os.ReadFile(path); err != nil || !strings.Contains(string(raw), `"leftAvatar"`) {
		t.Fatalf("config.json was rewritten by Load: %v", err)
	}
}

func TestLoadDropsTheLastUnusableBoxToAutomatic(t *testing.T) {
	cfg := Default()
	cfg.Layout.Calibration = &RegionCalibration{Profile: "camp",
		LeftBeads:  &NormalizedRect{X: 0.1, Y: 0.1, Width: 0.0625 * 3, Height: 1.0 / 36 * 3}, // too large
		RightBeads: &NormalizedRect{X: 0.6, Y: 0.1, Width: 0.0625 / 3, Height: 1.0 / 36 / 3}} // too small
	got, notes, err := LoadWithNotes(writeRawConfig(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	if got.Layout.Calibration != nil {
		t.Fatalf("calibration = %+v, want nil (all automatic)", got.Layout.Calibration)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "leftBeads") || !strings.Contains(notes[1], "rightBeads") {
		t.Fatalf("notes = %q", notes)
	}
}

// Only what Validate accepted before the limits is repaired; files that were
// never valid are still refused, and usable boxes produce no note.
func TestLoadStillRefusesCalibrationsThatWereNeverValid(t *testing.T) {
	big := NormalizedRect{X: 0, Y: 0, Width: 0.5, Height: 0.95}
	for name, cal := range map[string]*RegionCalibration{
		"box outside the frame": {Profile: "duel", LeftAvatar: &big, RightAvatar: &NormalizedRect{X: 0.9, Y: 0, Width: 0.2, Height: 0.1}},
		"unknown profile":       {Profile: "arena", LeftAvatar: &big},
	} {
		cfg := Default()
		cfg.Layout.Calibration = cal
		if _, _, err := LoadWithNotes(writeRawConfig(t, cfg)); err == nil {
			t.Fatalf("%s: LoadWithNotes accepted it", name)
		}
	}
	cfg := Default()
	cfg.Layout.Calibration = &RegionCalibration{Profile: "duel",
		LeftBeads: &NormalizedRect{X: 0.05, Y: 0.05, Width: 0.0625, Height: 1.0 / 36}}
	got, notes, err := LoadWithNotes(writeRawConfig(t, cfg))
	if err != nil || len(notes) != 0 || got.Layout.Calibration == nil || got.Layout.Calibration.LeftBeads == nil {
		t.Fatalf("usable calibration: %+v, %q, %v", got.Layout.Calibration, notes, err)
	}
}
