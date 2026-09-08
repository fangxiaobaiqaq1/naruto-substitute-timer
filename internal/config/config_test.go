package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultValid(t *testing.T) {
	if err := Validate(Default()); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestValidateRejectsWrongCenterCount(t *testing.T) {
	cfg := Default()
	cfg.Layout.Left.NominalCenters = cfg.Layout.Left.NominalCenters[:3]
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "layout.left.nominalCenters") {
		t.Fatalf("expected center count error, got %v", err)
	}
}

func TestValidateRejectsWeightsThatDoNotSumToOne(t *testing.T) {
	cfg := Default()
	cfg.Vision.ColorWeight = 0.1
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "vision weights") {
		t.Fatalf("expected weight error, got %v", err)
	}
}

func TestValidateRejectsBadPlayerSide(t *testing.T) {
	cfg := Default()
	cfg.UI.PlayerSide = "middle"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "playerSide") {
		t.Fatalf("expected playerSide error, got %v", err)
	}
}

func TestLoadOrCreateWritesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.PlayerSide != "auto" {
		t.Fatalf("playerSide = %q", cfg.UI.PlayerSide)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.UI.MiniWidth != cfg.UI.MiniWidth {
		t.Fatalf("round trip mini width %d vs %d", again.UI.MiniWidth, cfg.UI.MiniWidth)
	}
}
