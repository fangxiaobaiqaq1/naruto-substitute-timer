package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GE-03: schema-valid values that size per-frame work or durations must be
// rejected at load time with a message, not crash or wedge the capture loop.
// sampleHeightReferencePx 1e9 panicked the engine ("makeslice: len out of
// range") on the first fight frame; referenceHeight 1 took ~16 s per frame;
// pollIntervalMs 1e13 overflowed time.Duration and busy-spun capture.
func TestValidateRejectsRangesThatBreakTheCapturePath(t *testing.T) {
	for _, tc := range []struct {
		field  string
		change func(*Config)
	}{
		{"vision.sampleHeightReferencePx", func(c *Config) { c.Vision.SampleHeightReferencePX = 1e9 }},
		{"vision.sampleHeightReferencePx", func(c *Config) { c.Vision.SampleHeightReferencePX = 0 }},
		{"vision.sampleWidthReferencePx", func(c *Config) { c.Vision.SampleWidthReferencePX = -50 }},
		{"vision.sampleWidthReferencePx", func(c *Config) { c.Vision.SampleWidthReferencePX = 64.5 }},
		{"vision.coreScale", func(c *Config) { c.Vision.CoreScale = 1e9 }},
		{"vision.coreScale", func(c *Config) { c.Vision.CoreScale = 0 }},
		{"layout.referenceWidth", func(c *Config) { c.Layout.ReferenceWidth = 1 }},
		{"layout.referenceHeight", func(c *Config) { c.Layout.ReferenceHeight = 1 }},
		{"layout.referenceWidth", func(c *Config) { c.Layout.ReferenceWidth = 1 << 62 }},
		{"layout.referenceHeight", func(c *Config) { c.Layout.ReferenceHeight = 16385 }},
		{"ui.pollIntervalMs", func(c *Config) { c.UI.PollIntervalMS = 10_000_000_000_000 }},
		{"ui.idlePollIntervalMs", func(c *Config) { c.UI.IdlePollIntervalMS = 60_001 }},
		{"capture.timeoutMs", func(c *Config) { c.Capture.TimeoutMS = 10_000_000_000_000 }},
	} {
		cfg := Default()
		tc.change(&cfg)
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Errorf("%s out of range: Validate = %v", tc.field, err)
		}
	}
}

// The bounds are generous: every value up to them was measured to analyse a
// frame in well under 100 ms.
func TestValidateAcceptsRangeBoundaries(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"max samples": func(c *Config) {
			c.Vision.SampleWidthReferencePX, c.Vision.SampleHeightReferencePX, c.Vision.CoreScale = 64, 64, 1
		},
		"small samples": func(c *Config) {
			c.Vision.SampleWidthReferencePX, c.Vision.SampleHeightReferencePX, c.Vision.CoreScale = 0.5, 0.5, 0.01
		},
		"min reference":  func(c *Config) { c.Layout.ReferenceWidth, c.Layout.ReferenceHeight = 400, 250 },
		"max reference":  func(c *Config) { c.Layout.ReferenceWidth, c.Layout.ReferenceHeight = 16384, 16384 },
		"720p reference": func(c *Config) { c.Layout.ReferenceWidth, c.Layout.ReferenceHeight = 1280, 720 },
		"slow intervals": func(c *Config) {
			c.UI.PollIntervalMS, c.UI.IdlePollIntervalMS, c.Capture.TimeoutMS = 60_000, 60_000, 60_000
		},
		"fast intervals":  func(c *Config) { c.UI.PollIntervalMS, c.UI.IdlePollIntervalMS, c.Capture.TimeoutMS = 1, 1, 1 },
		"example timings": func(c *Config) { c.UI.PollIntervalMS, c.UI.IdlePollIntervalMS, c.Capture.TimeoutMS = 33, 1000, 5000 },
	} {
		cfg := Default()
		change(&cfg)
		if err := Validate(cfg); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The auditor's file: a config.json that passed Load before.
func TestLoadRejectsHugeSampleSize(t *testing.T) {
	good, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data := strings.Replace(string(good), `"sampleHeightReferencePx": 16`, `"sampleHeightReferencePx": 1e9`, 1)
	if data == string(good) {
		t.Fatal("fixture did not change the sample height")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "sampleHeightReferencePx") {
		t.Fatalf("Load = %v", err)
	}
}
