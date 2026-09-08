package rgb

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

// Six actual idle frames: ordinary Pain left (4 gold), six-tail Naruto right
// (2 red). Lack of a special-name template for Pain is expected, not name loss.
func TestIdleGoldGlintFrames(t *testing.T) {
	cfg := config.Default()
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	e.Prefer("camp")
	for i := 1; i <= 6; i++ {
		source := loadRGBA(t, fmt.Sprintf("../../../inbox/regressions/camp-idle-glint-20260907/diagnostic-%02d.png", i))
		for _, width := range []int{800, 960, 1280, 1600, 1920, 2560} {
			img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
			xdraw.BiLinear.Scale(img, img.Bounds(), source, source.Bounds(), draw.Src, nil)
			got := e.AnalyzeAt(img, time.Unix(1700000000, 0).Add(time.Duration(i)*20*time.Millisecond))
			l, r := countReady(got.Beads)
			if got.LeftNinja != "" || got.RightNinja != ninja.Naruto || l != 4 || r != 2 || knownCount(got.Beads) != 8 {
				t.Errorf("frame%d width%d: names %q/%q count %d/%d beads %+v", i, width, got.LeftNinja, got.RightNinja, l, r, got.Beads)
			}
		}
	}
}

func TestGoldGlintDoesNotTurnEffectsIntoVotes(t *testing.T) {
	cfg := config.Default().Vision
	area := detect.ComputeContentArea(1600, 900, detect.ModeStretch)
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left", Idx: 0}, X: 100, Y: 100}
	gold := color.RGBA{255, 200, 0, 255}
	for _, tc := range []string{"white", "flat gold wash", "gold wash with white center", "gold rim white center", "gold top only", "gold bottom only"} {
		t.Run(tc, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
			draw.Draw(img, image.Rect(80, 80, 121, 121), image.NewUniform(gold), image.Point{}, draw.Src)
			switch tc {
			case "white":
				draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
			case "gold wash with white center", "gold rim white center":
				draw.Draw(img, image.Rect(96, 96, 105, 105), image.White, image.Point{}, draw.Src)
				if tc == "gold rim white center" {
					draw.Draw(img, image.Rect(97, 93, 104, 108), image.White, image.Point{}, draw.Src)
				}
			case "gold top only":
				draw.Draw(img, image.Rect(80, 99, 121, 121), image.White, image.Point{}, draw.Src)
			case "gold bottom only":
				draw.Draw(img, image.Rect(80, 80, 121, 102), image.White, image.Point{}, draw.Src)
			}
			// Also test a finite overlay that already fades below the body:
			// the lateral/body checks must reject it, not just the far guard.
			img.SetRGBA(100, 114, color.RGBA{0, 0, 0, 255})
			// Both white-glint and ordinary saturated-gold paths must reject
			// these unbounded/one-sided effects.
			if goldGlintBody(img, p, 6, 7) {
				t.Fatal("unbounded/one-sided color admitted as a bean body")
			}
			got := sampleCalibrated(img, []detect.BeadPosition{p}, area, cfg)
			if !got[0].Unknown {
				t.Fatalf("effect voted: %+v", got)
			}
		})
	}
}
