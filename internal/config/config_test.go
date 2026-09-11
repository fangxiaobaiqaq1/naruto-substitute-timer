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

func TestLegacyConfigInheritsNewAppearanceAndUpdateDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"ui":{"autoTextRecognition":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UI.AutoCheckUpdates || cfg.UI.WindowOpacity != 1 || cfg.UI.FontScale != 1 {
		t.Fatalf("legacy UI defaults were not preserved: %+v", cfg.UI)
	}
}

func TestValidateAppearanceBoundsAreInclusiveAndRejectInvalidValues(t *testing.T) {
	cfg := Default()
	cfg.UI.WindowOpacity, cfg.UI.FontScale = 0.40, 0.80
	if err := Validate(cfg); err != nil {
		t.Fatalf("lower bounds should be valid: %v", err)
	}
	cfg.UI.WindowOpacity, cfg.UI.FontScale = 1.00, 1.60
	if err := Validate(cfg); err != nil {
		t.Fatalf("upper bounds should be valid: %v", err)
	}
	for _, tc := range []struct {
		name string
		set  func(*Config)
		want string
	}{
		{"opacity too low", func(c *Config) { c.UI.WindowOpacity = 0.39 }, "windowOpacity"},
		{"opacity too high", func(c *Config) { c.UI.WindowOpacity = 1.01 }, "windowOpacity"},
		{"font too low", func(c *Config) { c.UI.FontScale = 0.79 }, "fontScale"},
		{"font too high", func(c *Config) { c.UI.FontScale = 1.61 }, "fontScale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := Default()
			tc.set(&bad)
			if err := Validate(bad); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
}
