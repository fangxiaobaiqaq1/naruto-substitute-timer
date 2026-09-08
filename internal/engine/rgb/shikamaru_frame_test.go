package rgb

import (
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"testing"
)

func TestShikamaruUserFrame(t *testing.T) {
	cfg := config.Default()
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	e.Prefer("duel")
	img := loadRGBA(t, "../../../inbox/regressions/duel-shikamaru-name-20260907.png")
	got := e.Analyze(img)
	l, r := countReady(got.Beads)
	t.Logf("counts %d/%d beads %+v", l, r, got.Beads)
	if l != 3 || r != 2 || knownCount(got.Beads) != 8 {
		t.Fatalf("expected 3/2 known: %+v", got)
	}
}
