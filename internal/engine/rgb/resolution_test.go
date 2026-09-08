package rgb

import (
	"fmt"
	"image"
	"image/draw"
	"testing"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

func TestConfiguredHUDResolutionMatrix(t *testing.T) {
	cfg := config.Default()
	for _, fixture := range []struct {
		name, path  string
		left, right int
	}{
		{"camp", "../../../inbox/fight/duel_live_20260821.png", 4, 2},
		{"duel", "../../../inbox/fight/duel_20260821.png", 2, 4},
		{"duel-user", "../../../inbox/regressions/duel-user-20260906.png", 2, 2},
		{"duel-second", "../../../inbox/regressions/duel-second-round-20260906.png", 2, 4},
	} {
		src := loadRGBA(t, fixture.path)
		for _, dimensions := range []image.Point{{640, 360}, {800, 450}, {950, 534}, {1280, 720}, {1600, 900}, {1920, 1080}, {2560, 1440}} {
			for _, interpolation := range []struct {
				name   string
				scaler xdraw.Scaler
			}{{"nearest", xdraw.NearestNeighbor}, {"bilinear", xdraw.BiLinear}} {
				t.Run(fmt.Sprintf("%s/%dx%d/%s", fixture.name, dimensions.X, dimensions.Y, interpolation.name), func(t *testing.T) {
					img := image.NewRGBA(image.Rectangle{Max: dimensions})
					interpolation.scaler.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
					e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
					profile := "duel"
					if fixture.name == "camp" {
						profile = "camp"
					}
					e.Prefer(profile)
					res := e.Analyze(img)
					l, r := countReady(res.Beads)
					if l != fixture.left || r != fixture.right || knownCount(res.Beads) != 8 {
						t.Fatalf("got %d/%d, known %d; want %d/%d; %+v", l, r, knownCount(res.Beads), fixture.left, fixture.right, res.Beads)
					}
				})
			}
		}
	}
}

func TestConfiguredHUDUsesPixelBarsAndImageOrigin(t *testing.T) {
	cfg := config.Default()
	src := loadRGBA(t, "../../../inbox/regressions/duel-user-20260906.png")
	for _, tc := range []struct {
		name            string
		bounds, content image.Rectangle
	}{
		{"horizontal", image.Rect(0, 0, 1280, 960), image.Rect(0, 120, 1280, 840)},
		{"vertical", image.Rect(0, 0, 1920, 900), image.Rect(160, 0, 1760, 900)},
		{"origin", image.Rect(30, 50, 1630, 950), image.Rect(30, 50, 1630, 950)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(tc.bounds)
			xdraw.BiLinear.Scale(img, tc.content, src, src.Bounds(), draw.Src, nil)
			e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
			e.Prefer("duel")
			res := e.Analyze(img)
			l, r := countReady(res.Beads)
			if l != 2 || r != 2 || knownCount(res.Beads) != 8 {
				t.Fatalf("got %d/%d, known %d; %+v", l, r, knownCount(res.Beads), res)
			}
			if tc.name == "origin" && (res.Beads[0].X != 202 || res.Beads[0].Y != 153) {
				t.Fatalf("origin missing in bead coordinates: %+v", res.Beads[0])
			}
		})
	}
	// Deliberately stretch a valid HUD into a different native aspect ratio.
	// Auto must not quietly return a guessed ready count from wrong coordinates.
	wide := image.NewRGBA(image.Rect(0, 0, 1920, 900))
	xdraw.BiLinear.Scale(wide, wide.Bounds(), src, src.Bounds(), draw.Src, nil)
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	e.Prefer("duel")
	res := e.Analyze(wide)
	if !res.Uncertain || res.Scene != "unsupported-resolution" || len(res.Beads) != 0 {
		t.Fatalf("uncalibrated native aspect returned bead votes: %+v", res)
	}
}
