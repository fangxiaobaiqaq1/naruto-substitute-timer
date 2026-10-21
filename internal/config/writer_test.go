package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// GE-12: snapshots are taken under the owner's lock and written after it. A
// slow older write must never put older settings over newer ones.
func TestWriterNeverWritesAnOlderSnapshotOverANewerOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	var w Writer
	cfg := Default()
	cfg.UI.PlayerSide = "left"
	older := w.Snapshot(path, cfg)
	cfg.UI.PlayerSide = "right"
	newer := w.Snapshot(path, cfg)
	if err := newer.Save(); err != nil {
		t.Fatal(err)
	}
	if err := older.Save(); err != nil {
		t.Fatal(err)
	}
	stored, err := Load(path)
	if err != nil || stored.UI.PlayerSide != "right" {
		t.Fatalf("stored %q, %v", stored.UI.PlayerSide, err)
	}
	cfg.UI.PlayerSide = "auto"
	if err := w.Snapshot(path, cfg).Save(); err != nil {
		t.Fatal(err)
	}
	if stored, _ := Load(path); stored.UI.PlayerSide != "auto" {
		t.Fatalf("a later snapshot was not written: %q", stored.UI.PlayerSide)
	}
}

// The snapshot is encoded when it is taken: later changes to the owner's
// slices (still shared with the copied Config) cannot leak into the write.
func TestWriterSnapshotIsEncodedWhenTaken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	var w Writer
	cfg := Default()
	cfg.UI.PlayerNames = []string{"白方小"}
	snapshot := w.Snapshot(path, cfg)
	cfg.UI.PlayerNames[0] = "changed later"
	if err := snapshot.Save(); err != nil {
		t.Fatal(err)
	}
	if stored, err := Load(path); err != nil || stored.UI.PlayerNames[0] != "白方小" {
		t.Fatalf("stored %q, %v", stored.UI.PlayerNames, err)
	}
}

func TestWriterReportsInvalidSnapshotOnSave(t *testing.T) {
	var w Writer
	cfg := Default()
	cfg.UI.OverlayMode = "huge"
	if err := w.Snapshot(filepath.Join(t.TempDir(), "config.json"), cfg).Save(); err == nil || !strings.Contains(err.Error(), "overlayMode") {
		t.Fatalf("Save = %v", err)
	}
}
