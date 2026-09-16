package rgb

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

func TestSasukePurpleAndRedUserScreenshot(t *testing.T) {
	path := "../../../inbox/regressions/duel-sasuke-red-20260916/game.png"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("private user screenshot not distributed")
	}
	src := loadRGBA(t, path)
	for _, width := range []int{800, 960, 1280, 1308, 1600, 1920, 2560} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			img := src
			if width != src.Bounds().Dx() {
				img = image.NewRGBA(image.Rect(0, 0, width, width*9/16))
				xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
			}
			cfg := config.Default()
			e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
			e.Prefer("duel")
			for i := 0; i < 3; i++ {
				got := e.AnalyzeAt(img, time.Unix(100, 0).Add(time.Duration(i)*20*time.Millisecond))
				left, right := countReady(got.Beads)
				if got.LeftNinja != ninja.SasukeXiayin || got.RightNinja != ninja.SasukeXiayin || got.LeftSlots != 4 || got.RightSlots != 4 || left != 2 || right != 3 || knownCount(got.Beads) != 8 {
					t.Fatalf("names %q/%q slots %d/%d counts %d/%d: %+v", got.LeftNinja, got.RightNinja, got.LeftSlots, got.RightSlots, left, right, got.Beads)
				}
			}
		})
	}
}
