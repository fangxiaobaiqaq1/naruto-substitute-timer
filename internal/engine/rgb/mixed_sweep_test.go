package rgb

import (
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

// Native SDK evidence of simultaneous full purple / canonical four-slot warm rows.
// The user's compressed desktop MP4 identifies the symptom, but is not a
// substitute for these original pixels when checking thresholds and geometry.
func TestNativeMixedSweepCurrentPixels(t *testing.T) {
	for _, directory := range []string{"mixed-bead-flicker-20260916-before", "mixed-bead-flicker-20260916-native", "mixed-bead-flicker-20260916-after"} {
		t.Run(directory, func(t *testing.T) {
			files, err := filepath.Glob("../../../debug/" + directory + "/*.png")
			if err != nil {
				t.Fatal(err)
			}
			if len(files) == 0 {
				t.Skip("local native evidence is not distributed")
			}
			cfg := config.Default()
			e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
			e.Prefer("camp")
			for i, path := range files {
				got := e.AnalyzeAt(loadRGBA(t, path), time.Unix(100, 0).Add(time.Duration(i)*20*time.Millisecond))
				left, right := countReady(got.Beads)
				if got.LeftNinja != ninja.SasukeXiayin || got.RightNinja != ninja.Madara || got.LeftSlots != 4 || got.RightSlots != 4 || knownCount(got.Beads) != 8 || left != 4 || right != 4 {
					t.Errorf("%s: %q/%q counts %d/%d: %+v", filepath.Base(path), got.LeftNinja, got.RightNinja, left, right, got.Beads)
				}
			}
		})
	}
}
