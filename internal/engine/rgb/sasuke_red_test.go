package rgb

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"os"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

// Only the native 1787x1006 frame is captured; scaling and hiding the title
// explicitly exercise the same skin's current portrait, not new gameplay.
func TestSasukeJinyingUserScreenshot(t *testing.T) {
	path := "../../../inbox/regressions/camp-sasuke-jinying-20261106/game.png"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("private user screenshot not distributed")
	}
	src := loadRGBA(t, path)
	for _, width := range []int{800, 960, 1280, 1600, 1787, 1920, 2560} {
		for _, hideTitle := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/hideTitle=%v", width, hideTitle), func(t *testing.T) {
				height := int(math.Round(float64(width) * float64(src.Bounds().Dy()) / float64(src.Bounds().Dx())))
				img := image.NewRGBA(image.Rect(0, 0, width, height))
				xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
				cfg := config.Default()
				layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
				if hideTitle {
					area, _ := layout.ContentArea(img)
					row := layout.PositionsInImage("camp", img, area)[4:]
					roi := ninja.NameRegion(image.Pt(row[0].X, row[0].Y), detect.RowHUDScale(row, area, 960, 540), false)
					draw.Draw(img, roi, image.Black, image.Point{}, draw.Src)
				}
				e := NewConfigured(layout, cfg.Vision)
				e.Prefer("camp")
				got := e.AnalyzeAt(img, time.Unix(100, 0))
				if got.RightSlots != 4 || (got.RightNinja != ninja.SasukeXiayin && got.RightNinja != ninja.SasukeJinying) || (hideTitle && got.RightNinja != ninja.SasukeJinying) {
					t.Fatalf("right identity/slots: %+v", got)
				}
				for _, b := range got.Beads[4:] {
					if b.Unknown || b.Lit != (b.Label == "R1" || b.Label == "R2") {
						t.Fatalf("want right 2/4, got %+v", got.Beads)
					}
				}
				// The downscaled 800px ordinary L1 is ambiguous independently of this skin.
				if left, _ := countReady(got.Beads); width >= 960 && (left != 4 || knownCount(got.Beads) != 8) {
					t.Fatalf("want left 4/4, got %+v", got.Beads)
				}
			})
		}
	}
}

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
