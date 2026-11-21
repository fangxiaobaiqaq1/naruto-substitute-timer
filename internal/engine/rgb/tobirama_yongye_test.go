package rgb

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

// Real HUD pixels of the 2026-11-21 1061x598 duel screenshot (top 112 rows,
// account names masked). 千手扉间[永夜逐光]'s blue energy gauge pushes the
// four ordinary beans 9px (960x540) lower; this frame shows 3/4 lit while its
// portrait is covered by an orange hit mark, so only the title identifies it.
func TestTobiramaYongyeEnergyRowUserHUD(t *testing.T) {
	crop := loadRGBA(t, "testdata/tobirama-yongye/left-three.png")
	src := image.NewRGBA(image.Rect(0, 0, 1061, 598))
	draw.Draw(src, src.Bounds(), image.Black, image.Point{}, draw.Src)
	draw.Draw(src, crop.Bounds(), crop, crop.Bounds().Min, draw.Src)
	for _, width := range []int{960, 1061, 1280, 1920} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			height := int(math.Round(float64(width) * 598 / 1061))
			img := image.NewRGBA(image.Rect(0, 0, width, height))
			xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
			cfg := config.Default()
			layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
			e := NewConfigured(layout, cfg.Vision)
			e.Prefer("duel")
			var got engine.Result
			for i := 0; i < 3; i++ {
				got = e.AnalyzeAt(img, time.Unix(100, 0).Add(time.Duration(i)*250*time.Millisecond))
				left, right := countReady(got.Beads)
				if got.LeftNinja != ninja.TobiramaYongye || got.LeftSlots != 4 || got.RightSlots != 4 ||
					left != 3 || right != 2 || knownCount(got.Beads) != 8 {
					t.Fatalf("name=%q slots=%d/%d ready=%d/%d beads=%+v", got.LeftNinja, got.LeftSlots, got.RightSlots, left, right, got.Beads)
				}
			}
			// Only the left row moves, by 9 reference pixels.
			if dy := float64(got.Beads[0].Y-got.Beads[4].Y) * 540 / float64(height); math.Abs(dy-9) > 1.2 {
				t.Fatalf("left row offset %.2f ref px, want 9: %+v", dy, got.Beads)
			}
			// Without a current title or portrait the gauge must not move the
			// row or be read as beans.
			for _, g := range got.Geometry[:1] {
				draw.Draw(img, g.NameROI, image.Black, image.Point{}, draw.Src)
				draw.Draw(img, g.AvatarROI, image.Black, image.Point{}, draw.Src)
			}
			fresh := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
			fresh.Prefer("duel")
			unknown := fresh.AnalyzeAt(img, time.Unix(101, 0))
			if unknown.LeftNinja != "" || unknown.Beads[0].Y != got.Beads[4].Y {
				t.Fatalf("unidentified left HUD guessed the gauge row: %+v", unknown)
			}
		})
	}
}
