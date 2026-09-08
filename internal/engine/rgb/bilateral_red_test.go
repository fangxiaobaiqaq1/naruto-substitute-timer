package rgb

import (
	"image"
	"image/draw"
	"testing"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

func TestLiveBothNarutoRightOne(t *testing.T) {
	cfg := config.Default()
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	e.Prefer("camp")
	img := loadRGBA(t, "../../../inbox/regressions/camp-both-naruto-right-one-20260907.png")
	for _, width := range []int{800, 960, 1280, 1600, 1920, 2560} {
		scaled := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
		xdraw.BiLinear.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Src, nil)
		got := e.Analyze(scaled)
		l, r := countReady(got.Beads)
		if got.LeftNinja != ninja.Naruto || got.RightNinja != ninja.Naruto || l != 4 || r != 1 || knownCount(got.Beads) != 8 {
			t.Fatalf("%dpx both Naruto: names %q/%q count %d/%d beads %+v", width, got.LeftNinja, got.RightNinja, l, r, got.Beads)
		}
	}
}
