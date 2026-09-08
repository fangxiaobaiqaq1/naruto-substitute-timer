package scene

import (
	"image"
	"image/color"
	"image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
	"path/filepath"
	"testing"

	xdraw "golang.org/x/image/draw"
)

func TestSpecialNarutoHasBattleContinuityControl(t *testing.T) {
	cfg := config.Default()
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cat, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	img := recordedPage(t, "../../inbox/regressions/camp-both-naruto-right-one-20260907.png")
	if !cat.SupportsFight(img, "camp") {
		t.Fatalf("special attack art has no continuation control; scores=%+v", cat.ScoreAll(img))
	}
}

func TestSubstituteContinuityIgnoresAttackArtAndCentralOverlay(t *testing.T) {
	cfg := config.Default()
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cat, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	src := recordedPage(t, "../../inbox/regressions/camp-both-naruto-right-one-20260907.png")
	for _, variant := range []string{"no-attack", "darkened", "center-overlay"} {
		img := match.CropRGBA(src, src.Bounds())
		// All primary training markers and the variable attack artwork removed.
		for _, r := range []image.Rectangle{image.Rect(708, 12, 892, 101), image.Rect(720, 780, 880, 895), image.Rect(1350, 670, 1590, 890)} {
			draw.Draw(img, r, image.NewUniform(color.RGBA{50, 60, 70, 255}), image.Point{}, draw.Src)
		}
		if variant == "darkened" {
			for y := 752; y < 846; y++ {
				for x := 1034; x < 1126; x++ {
					c := img.RGBAAt(x, y)
					c.R /= 2
					c.G /= 2
					c.B /= 2
					img.SetRGBA(x, y, c)
				}
			}
		}
		if variant == "center-overlay" {
			// Synthetic center obstruction, NOT a captured cooldown animation.
			draw.Draw(img, image.Rect(1070, 781, 1090, 814), image.White, image.Point{}, draw.Src)
		}
		for _, w := range []int{800, 960, 1280, 1600, 1920, 2560} {
			scaled := image.NewRGBA(image.Rect(0, 0, w, w*9/16))
			xdraw.BiLinear.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Src, nil)
			if !cat.SupportsFight(scaled, "camp") {
				t.Fatalf("%s %dpx missing substitute support: %+v", variant, w, cat.ScoreAll(scaled))
			}
			if cat.Decide(scaled).Kind == engine.GateFight {
				t.Fatalf("continuation button opened a new scene: %s %dpx", variant, w)
			}
		}
		// With the substitute button also missing there is NO control evidence.
		draw.Draw(img, image.Rect(1010, 730, 1160, 880), image.Black, image.Point{}, draw.Src)
		if cat.SupportsFight(img, "camp") {
			t.Fatalf("missing both controls accepted: %s", variant)
		}
	}
}
