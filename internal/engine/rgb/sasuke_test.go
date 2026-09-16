package rgb

import (
	"fmt"
	"image"
	"image/color"
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

func sasukeScreenshot(t *testing.T, name string) *image.RGBA {
	t.Helper()
	path := "../../../inbox/regressions/camp-sasuke-xiayin-20260916/" + name + ".png"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("local user screenshots are not distributed")
	}
	return loadRGBA(t, path)
}

// Only the 1308x734 case is the untouched user image; other sizes are explicit
// resampling regressions, not additional captured gameplay evidence.
func TestSasukeXiayinUserScreenshots(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ready int
	}{{"three", 3}, {"glint", 4}} {
		src := sasukeScreenshot(t, tc.name)
		for _, width := range []int{800, 960, 1280, 1308, 1600, 1920, 2560} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				img := src
				if width != src.Bounds().Dx() {
					img = image.NewRGBA(image.Rect(0, 0, width, width*9/16))
					xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
				}
				cfg := config.Default()
				layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
				e := NewConfigured(layout, cfg.Vision)
				e.Prefer("camp")
				for i := range 3 {
					got := e.AnalyzeAt(img, time.Unix(100, 0).Add(time.Duration(i)*20*time.Millisecond))
					left, right := countReady(got.Beads)
					if got.LeftNinja != ninja.SasukeXiayin || got.RightNinja != ninja.Madara || got.LeftSlots != 4 || got.RightSlots != 6 || left != tc.ready || right != 2 || knownCount(got.Beads) != 10 {
						// Include bounded body/wash diagnostics in failures.
						area, _ := layout.ContentArea(img)
						positions, _ := e.specialPositions(img, layout.PositionsIn("camp", area), area)
						w := max(2, int(math.Round(cfg.Vision.SampleWidthReferencePX*cfg.Vision.CoreScale*float64(area.W)/detect.LogicWidth)))
						h := max(2, int(math.Round(cfg.Vision.SampleHeightReferencePX*cfg.Vision.CoreScale*float64(area.H)/detect.LogicHeight)))
						guard := max(3, int(math.Round(cfg.Vision.SampleHeightReferencePX*float64(area.H)/detect.LogicHeight*1.2)))
						for _, p := range positions[:4] {
							t.Logf("slot %d: glintBody=%v wash=%v core=%v", p.Idx+1, purpleGlintBody(img, p, w, h), specialWash(img, p, ninja.Purple, guard), img.RGBAAt(p.X, p.Y))
						}
						t.Fatalf("frame %d: names %q/%q slots %d/%d counts %d/%d: %+v", i, got.LeftNinja, got.RightNinja, got.LeftSlots, got.RightSlots, left, right, got.Beads)
					}
				}
			})
		}
	}
}

// Portable synthetic coverage: names use the shipped crop, while rectangles
// represent explicit lit/dark cores. This is not a claim of native right/duel
// Sasuke screenshot coverage.
func TestSasukeXiayinRowBothSidesAndLayouts(t *testing.T) {
	name := loadRGBA(t, "../../ninja/templates/sasuke_xiayin.png")
	for _, profile := range []string{"camp", "duel"} {
		for _, width := range []int{800, 960, 1920} {
			for _, side := range []string{"left", "right"} {
				t.Run(fmt.Sprintf("%s/%d/%s", profile, width, side), func(t *testing.T) {
					cfg := config.Default()
					layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
					img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
					area, _ := layout.ContentArea(img)
					positions := layout.PositionsIn(profile, area)
					scale := float64(width) / 960
					var box image.Rectangle
					for _, p := range positions {
						if p.Side != side {
							continue
						}
						if p.Idx == 0 {
							roi := ninja.NameRegion(image.Pt(p.X, p.Y), scale, side == "left")
							at := roi.Min.Add(image.Pt(int(12*scale), int(7*scale)))
							box = image.Rectangle{Min: at, Max: at.Add(image.Pt(int(math.Round(float64(name.Bounds().Dx())*scale)), int(math.Round(float64(name.Bounds().Dy())*scale))))}
							xdraw.BiLinear.Scale(img, box, name, name.Bounds(), draw.Src, nil)
						}
						// Extra energy bar at the old row, beans below it.
						draw.Draw(img, image.Rect(p.X-int(7*scale), p.Y-1, p.X+int(7*scale)+1, p.Y+2), image.NewUniform(color.RGBA{180, 50, 240, 255}), image.Point{}, draw.Src)
						y := int(math.Round((p.LY + 18) * float64(area.H) / detect.LogicHeight))
						c := color.RGBA{200, 50, 240, 255}
						if p.Idx == 3 {
							c = color.RGBA{40, 15, 80, 255}
						}
						draw.Draw(img, image.Rect(p.X-int(3*scale), y-int(4*scale), p.X+int(3*scale)+1, y+int(4*scale)+1), image.NewUniform(c), image.Point{}, draw.Src)
					}
					e := NewConfigured(layout, cfg.Vision)
					e.Prefer(profile)
					at := time.Unix(100, 0)
					for i := range 2 {
						got := e.AnalyzeAt(img, at.Add(time.Duration(i)*20*time.Millisecond))
						for j, b := range got.Beads {
							p := positions[j]
							if p.Side != side {
								if b.Y != p.Y {
									t.Fatal("other side inherited row shift")
								}
								continue
							}
							wantY := int(math.Round((p.LY + 18) * float64(area.H) / detect.LogicHeight))
							if b.X != p.X || b.Y != wantY || b.Unknown || b.Lit != (p.Idx < 3) {
								t.Fatalf("frame %d: shifted core %+v; expected y=%d", i, b, wantY)
							}
						}
						// A brief name gap must retain geometry, not cached bean votes.
						draw.Draw(img, box, image.Black, image.Point{}, draw.Src)
					}
					fresh := NewConfigured(layout, cfg.Vision)
					fresh.Prefer(profile)
					for j, b := range fresh.AnalyzeAt(img, at).Beads {
						if b.Y != positions[j].Y {
							t.Fatal("unverified fresh name shifted the row")
						}
					}
				})
			}
		}
	}
}

func TestSasukeXiayinHaloDoesNotAdmitEffects(t *testing.T) {
	area := detect.ContentArea{W: 960, H: 540}
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left"}, X: 100, Y: 100}
	readout := ninja.Readout{Name: ninja.SasukeXiayin, Slots: 4, Palette: ninja.Purple, RowOffsetY: 9}
	purple := color.RGBA{200, 50, 240, 255}
	for _, kind := range []string{"wash", "band", "white", "rim", "rim white core", "one-sided glow"} {
		t.Run(kind, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 200, 200))
			switch kind {
			case "wash":
				draw.Draw(img, img.Bounds(), image.NewUniform(purple), image.Point{}, draw.Src)
			case "band":
				draw.Draw(img, image.Rect(50, 89, 151, 112), image.NewUniform(purple), image.Point{}, draw.Src)
			case "white":
				draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
			case "one-sided glow":
				draw.Draw(img, image.Rect(50, 89, 106, 112), image.NewUniform(purple), image.Point{}, draw.Src)
			case "rim", "rim white core":
				for dy := -10; dy <= 10; dy++ {
					for dx := -8; dx <= 8; dx++ {
						if detect.InBeadDiamond(0, 0, 16, 20, dx, dy) && !detect.InBeadDiamond(0, 0, 12, 16, dx, dy) {
							img.SetRGBA(p.X+dx, p.Y+dy, purple)
						}
					}
				}
				if kind == "rim white core" {
					draw.Draw(img, image.Rect(97, 97, 104, 104), image.White, image.Point{}, draw.Src)
				}
			}
			if isolatedPurpleHalo(img, p, 4, 4, 10) {
				t.Fatal("unbounded/unfilled effect passed isolation proof")
			}
			bead := sampleCalibratedSpecial(img, []detect.BeadPosition{p}, area, config.Default().Vision, [2]ninja.Readout{readout, {}})[0]
			if bead.Lit || !bead.Unknown {
				t.Fatalf("effect became bean evidence: %+v", bead)
			}
		})
	}
	// An isolated saturated sparkle is an exception only for the current exact
	// Sasuke identity, not Obito, an unverified hint, or a bare purple color.
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(img, image.Rect(70, 89, 131, 112), image.NewUniform(color.RGBA{160, 50, 220, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(98, 96, 103, 105), image.NewUniform(color.RGBA{255, 120, 255, 255}), image.Point{}, draw.Src)
	for _, id := range []ninja.Readout{readout, {Name: ninja.Obito, Slots: 4, Palette: ninja.Purple}, {Slots: 4, Palette: ninja.Purple}, {Slots: 4, Unverified: true, PaletteHint: ninja.Purple, RowOffsetY: 9}} {
		bead := sampleCalibratedSpecial(img, []detect.BeadPosition{p}, area, config.Default().Vision, [2]ninja.Readout{id, {}})[0]
		want := id.Name == ninja.SasukeXiayin
		if bead.Lit != want || bead.Unknown == want {
			t.Fatalf("identity gating %+v: %+v", id, bead)
		}
	}
}
