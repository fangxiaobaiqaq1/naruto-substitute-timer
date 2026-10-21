package detect

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
)

func TestHUDRowOffsetRequiresCurrentSlopingBar(t *testing.T) {
	for _, size := range []image.Point{{2400, 1080}, {2160, 1080}, {1280, 576}} {
		for _, profile := range []string{"camp", "duel"} {
			for _, right := range []bool{false, true} {
				ca := ContentArea{X: 17, Y: 23, W: size.X, H: size.Y}
				scale := ca.HUDScale(1600, 900)
				step := max(1, int(math.Round(scale)))
				y := 74.0
				if profile == "camp" {
					y = 70
				}
				cy := ca.Y + int(math.Round(y*scale))
				first := ca.MapHUDPixel(172, 103, "left", 1600, 900)
				direction := -1
				if right {
					first = ca.MapHUDPixel(1423, 103, "right", 1600, 900)
					direction = 1
				}
				delta := -direction * int(math.Round(90*scale))
				for _, kind := range []string{"bar", "purple-tinted bar", "blue effect", "missing", "flat effect", "purple flat effect", "one line covered", "short sparkle", "wrong slope", "wrong height"} {
					img := image.NewRGBA(image.Rect(ca.X, ca.Y, ca.X+ca.W, ca.Y+ca.H))
					for row := -1; row <= 1; row++ {
						if kind == "missing" || (kind == "one line covered" && row == 0) {
							continue
						}
						x := first.X + delta + direction*row*step
						if kind == "flat effect" || kind == "purple flat effect" {
							x = first.X + delta
						}
						if kind == "wrong slope" {
							x = first.X + delta - direction*row*step
						}
						length := int(200 * scale)
						if kind == "short sparkle" {
							length = int(20 * scale)
						}
						x0, x1 := x, x+length
						if right {
							x0, x1 = x-length+1, x+1
						}
						py := cy + row*step
						if kind == "wrong height" {
							py += 10 * step
						}
						c := color.RGBA{240, 70, 10, 255}
						if kind == "purple-tinted bar" || kind == "purple flat effect" {
							c.B = 240
						}
						if kind == "blue effect" {
							c.R, c.B = 180, 250
						}
						draw.Draw(img, image.Rect(x0, py, x1, py+1), image.NewUniform(c), image.Point{}, draw.Src)
					}
					want := 0
					if kind == "bar" || kind == "purple-tinted bar" {
						want = delta
					}
					if got := HUDRowOffset(img, ca, first, right, profile); got != want {
						t.Fatalf("%v/%s/right=%v/%s offset=%d want=%d", size, profile, right, kind, got, want)
					}
					// A similar red object cannot recalibrate a tablet or 16:9 HUD.
					narrow := ca
					narrow.W = narrow.H * 16 / 9
					if HUDRowOffset(img, narrow, first, right, profile) != 0 {
						t.Fatal("ordinary HUD was recalibrated")
					}
				}
			}
		}
	}
	if HUDRowOffset(nil, ContentArea{W: 2400, H: 1080}, image.Point{}, false, "duel") != 0 {
		t.Fatal("missing image supplied an anchor")
	}
}
