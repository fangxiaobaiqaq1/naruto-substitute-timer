package engine

import (
	"fmt"
	"image"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
)

// MuMu SDK frames are native game content at several output sizes. All use the
// one normalized profile; this guards against accidentally adding tables per
// emulator resolution.
func TestConfiguredLayoutMapsMuMuReferenceAcrossResolutions(t *testing.T) {
	cfg := config.Default().Layout
	layout := NewConfiguredLayout(detect.ModeStretch, cfg)
	for _, size := range []image.Point{{1280, 720}, {1600, 900}, {1920, 1080}, {2560, 1440}} {
		t.Run(fmt.Sprintf("%dx%d", size.X, size.Y), func(t *testing.T) {
			got := layout.PositionsFor("camp", size.X, size.Y)
			if len(got) != 8 {
				t.Fatalf("got %d positions", len(got))
			}
			wantX := int(float64(size.X)*cfg.Left.NominalCenters[0].X + .5)
			wantY := int(float64(size.Y)*cfg.Left.NominalCenters[0].Y + .5)
			if got[0].X != wantX || got[0].Y != wantY {
				t.Fatalf("first camp bead=%d,%d want normalized=%d,%d", got[0].X, got[0].Y, wantX, wantY)
			}
			if got[1].X-got[0].X <= 0 || got[1].Y != got[0].Y {
				t.Fatalf("camp pitch is not normalized: %+v", got[:2])
			}
		})
	}

	// A 1280x800 MuMu client is centered letterbox content, not a second
	// coordinate table. The same reference point must include the 40px bar.
	letterbox := NewConfiguredLayout(detect.ModeLetterbox, cfg)
	got := letterbox.PositionsFor("camp", 1280, 800)
	if got[0].X != 124 || got[0].Y != 121 {
		t.Fatalf("letterbox mapping=%d,%d want 124,121", got[0].X, got[0].Y)
	}
}
