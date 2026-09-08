package rgb

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

// Opt-in inspection of an existing local recording; never captures the game.
func TestStabilityDiagnostic(t *testing.T) {
	dir := os.Getenv("TIMER_STABILITY_FRAMES")
	if dir == "" {
		t.Skip("set TIMER_STABILITY_FRAMES to an extracted recording")
	}
	cfg := config.Default()
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	for _, i := range []int{30, 31, 32, 92, 153, 176} {
		img := loadRGBA(t, filepath.Join(dir, fmt.Sprintf("frame-%04d.png", i)))
		area, _ := layout.ContentArea(img)
		vision := cfg.Vision
		positions := layout.PositionsIn("duel", area)
		for _, p := range positions[:4] {
			b := sampleCalibrated(img, []detect.BeadPosition{p}, area, vision)
			t.Logf("frame%d %s%d (%d,%d) glint=%v sampled=%+v", i, p.Side, p.Idx+1, p.X, p.Y, goldGlintBody(img, p, 4, 5), b)
			if b[0].Unknown {
				for dy := -5; dy <= 5; dy++ {
					t.Logf("dy%d center=%v left=%v right=%v", dy, img.RGBAAt(p.X, p.Y+dy), img.RGBAAt(p.X-8, p.Y+dy), img.RGBAAt(p.X+8, p.Y+dy))
				}
			}
		}
	}
}
