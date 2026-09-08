package rgb

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"testing"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

// The September 8 recording ended before Obito entered. These are deliberately
// composed from the older supplied HUD crops, not labelled as session frames.
// Exercise left/right name search and purple sampling in each current layout.
func TestObitoNameAndBeansBothLayouts(t *testing.T) {
	for _, tc := range []struct {
		file        string
		x, y, ready int
	}{{"obito-full", 86, 55, 4}, {"obito-two", 87, 56, 2}} {
		src := loadRGBA(t, "../../../inbox/regressions/special-ninjas-20260907/"+tc.file+".png")
		for _, profile := range []string{"camp", "duel"} {
			for _, width := range []int{800, 960, 1280, 1600, 1920, 2560} {
				t.Run(fmt.Sprintf("%s/%s/%d", tc.file, profile, width), func(t *testing.T) {
					cfg := config.Default()
					layout := engine.NewConfiguredLayout(detect.ModeStretch, cfg.Layout)
					img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
					scale := float64(width) / 960
					for _, side := range []string{"left", "right"} {
						var row []detect.BeadPosition
						for _, p := range layout.PositionsFor(profile, width, width*9/16) {
							if p.Side == side {
								row = append(row, p)
							}
						}
						for i, p := range row {
							body := image.Rect(tc.x+15*i-7, tc.y-10, tc.x+15*i+8, tc.y+12)
							dst := image.Rect(p.X-int(math.Round(7*scale)), p.Y-int(math.Round(10*scale)), p.X+int(math.Round(8*scale)), p.Y+int(math.Round(12*scale)))
							xdraw.BiLinear.Scale(img, dst, src, body, draw.Src, nil)
						}
						name := image.Rect(tc.x, 0, src.Bounds().Max.X, tc.y-13)
						nameWidth := int(math.Round(float64(name.Dx()) * scale))
						x := row[0].X
						if side == "right" {
							x -= nameWidth
						}
						y := row[0].Y - int(math.Round(float64(tc.y)*scale))
						dst := image.Rect(x, y, x+nameWidth, y+int(math.Round(float64(name.Dy())*scale)))
						xdraw.BiLinear.Scale(img, dst, src, name, draw.Src, nil)
					}
					e := NewConfigured(layout, cfg.Vision)
					e.Prefer(profile)
					got := e.Analyze(img)
					l, r := countReady(got.Beads)
					if got.LeftNinja != ninja.Obito || got.RightNinja != ninja.Obito || l != tc.ready || r != tc.ready || knownCount(got.Beads) != 8 {
						t.Fatalf("names %q/%q, counts %d/%d: %+v", got.LeftNinja, got.RightNinja, l, r, got.Beads)
					}
				})
			}
		}
	}
}
