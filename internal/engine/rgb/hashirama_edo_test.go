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

// Real HUD pixels from the two reported 1266x712 camp screenshots. Only the
// top 112 rows are retained; black padding restores the original geometry.
// This checks recognition/sampling, not scene gating on a fabricated frame.
func TestHashiramaEdoSixSlotUserHUD(t *testing.T) {
	for _, tc := range []struct {
		file string
		left int
	}{{"red", 5}, {"blue-three", 3}} {
		crop := loadRGBA(t, "testdata/hashirama-edo/"+tc.file+".png")
		src := image.NewRGBA(image.Rect(0, 0, 1266, 712))
		draw.Draw(src, src.Bounds(), image.Black, image.Point{}, draw.Src)
		draw.Draw(src, crop.Bounds(), crop, crop.Bounds().Min, draw.Src)
		for _, width := range []int{960, 1266, 1280, 1920} {
			t.Run(fmt.Sprintf("%s/%d", tc.file, width), func(t *testing.T) {
				height := int(math.Round(float64(width) * 712 / 1266))
				img := image.NewRGBA(image.Rect(0, 0, width, height))
				xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
				cfg := config.Default()
				e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
				e.Prefer("camp")
				var got engine.Result
				for i := 0; i < 3; i++ {
					got = e.AnalyzeAt(img, time.Unix(100, 0).Add(time.Duration(i)*250*time.Millisecond))
					left, right := countReady(got.Beads)
					if got.LeftNinja != ninja.HashiramaEdo || got.RightNinja != ninja.HashiramaEdo ||
						got.LeftSlots != 6 || got.RightSlots != 6 || left != tc.left || right != 6 || knownCount(got.Beads) != 12 {
						t.Fatalf("names=%q/%q slots=%d/%d ready=%d/%d want=%d/6 beads=%+v",
							got.LeftNinja, got.RightNinja, got.LeftSlots, got.RightSlots, left, right, tc.left, got.Beads)
					}
				}
				// With no current title or portrait, red pixels alone must not
				// enable six slots (including at the fifth/sixth bead positions).
				for _, g := range got.Geometry {
					draw.Draw(img, g.NameROI, image.Black, image.Point{}, draw.Src)
					draw.Draw(img, g.AvatarROI, image.Black, image.Point{}, draw.Src)
				}
				fresh := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
				fresh.Prefer("camp")
				unknown := fresh.AnalyzeAt(img, time.Unix(101, 0))
				if unknown.LeftNinja != "" || unknown.RightNinja != "" || unknown.LeftSlots != 4 || unknown.RightSlots != 4 {
					t.Fatalf("unidentified HUD enabled a six-slot policy: %+v", unknown)
				}
			})
		}
	}
}
