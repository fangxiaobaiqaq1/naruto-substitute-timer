package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/assets"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/match"
	"narutotimer/internal/ninja"
)

// Synthetic NGUI HUDs, NOT native screenshots of these ninja/device combinations.
// Only pixels are constructed. Every case traverses the real scene/identity/RGB
// engine and desktop session; no Fighting, palette, slot-count or bean flags are injected.
func TestSpecialHUDResolutionMatrix(t *testing.T) {
	type variant struct {
		file, name string
		lit, dark  color.RGBA
		slots      int
		offset     float64 // 960x540 title/HUD units
	}
	purple, purpleDark := color.RGBA{220, 60, 250, 255}, color.RGBA{40, 15, 80, 255}
	red, redDark := color.RGBA{240, 95, 80, 255}, color.RGBA{40, 10, 15, 255}
	blue, dark := color.RGBA{40, 200, 255, 255}, color.RGBA{28, 54, 98, 255}
	warm := color.RGBA{240, 65, 18, 255}
	variants := []variant{
		{"sasuke_xiayin", ninja.SasukeXiayin, purple, purpleDark, 4, 9},
		{"sasuke_xiayin", ninja.SasukeXiayin, red, redDark, 4, 9},
		{"obito", ninja.Obito, purple, purpleDark, 4, 0},
		{"naruto", ninja.Naruto, red, redDark, 4, 0},
		{"hashirama", ninja.Hashirama, warm, dark, 6, 0},
		{"madara", ninja.Madara, warm, dark, 4, 0},
		{"minato_kyubi", ninja.MinatoKyubi, blue, dark, 4, 13},
	}
	images := map[string]*image.Gray{}
	load := func(name string) *image.Gray {
		if img := images[name]; img != nil {
			return img
		}
		var b []byte
		var err error
		if name == "substitute_continuity" || name == "raw_attack_core" || name == "camp_reset_top" {
			b, err = assets.Templates.ReadFile("templates/" + name + ".png")
		} else {
			b, err = os.ReadFile("../ninja/templates/" + name + ".png")
		}
		if err != nil {
			t.Fatal(err)
		}
		src, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		images[name] = match.ToGray(src)
		return images[name]
	}
	for _, size := range []image.Point{{1280, 720}, {1784, 1004}, {1516, 948}, {2880, 1800}, {2160, 1080}, {2800, 1752}, {1600, 900}, {1920, 1200}, {2400, 1080}, {963, 541}, {2377, 1080}, {1024, 768}} {
		for _, profile := range []string{"camp", "duel"} {
			for _, specialSide := range []string{"left", "right"} {
				for vi, v := range variants {
					t.Run(fmt.Sprintf("%dx%d/%s/%s/%s-%d", size.X, size.Y, profile, specialSide, v.file, vi), func(t *testing.T) {
						cfg := config.Default()
						cfg.UI.PlayerSide = "right"
						if specialSide == "right" {
							cfg.UI.PlayerSide = "left"
						}
						cfg.UI.AutoTextRecognition = false
						eng, err := factory.New(factory.FromApp(cfg))
						if err != nil {
							t.Fatal(err)
						}
						s := testSession(nil)
						s.cfg, s.side = cfg, cfg.UI.PlayerSide
						// Independent fixture geometry: fit the 1600x900 root and
						// keep asymmetric safe margins only on wide native surfaces.
						fit := math.Min(float64(size.X)/1600, float64(size.Y)/900)
						nameScale := fit * 1600 / 960
						rootScale := fit * 1600 / 1920
						base := image.NewRGBA(image.Rectangle{Max: size})
						draw.Draw(base, base.Bounds(), image.NewUniform(color.RGBA{32, 32, 32, 255}), image.Point{}, draw.Src)
						stamp := func(name string, scale float64, cx, cy int) {
							g := load(name)
							g = match.ScaleGray(g, max(4, int(math.Round(float64(g.Bounds().Dx())*scale))), max(4, int(math.Round(float64(g.Bounds().Dy())*scale))))
							r := image.Rect(cx-g.Bounds().Dx()/2, cy-g.Bounds().Dy()/2, cx-g.Bounds().Dx()/2+g.Bounds().Dx(), cy-g.Bounds().Dy()/2+g.Bounds().Dy())
							draw.Draw(base, r, g, g.Bounds().Min, draw.Src)
						}
						// Real shared controls in independently chosen screen positions.
						// No primary round label: this also exercises the fallback gate.
						stamp("substitute_continuity", rootScale, int(.70*float64(size.X)), int(.9*float64(size.Y)))
						stamp("raw_attack_core", rootScale, int(.92*float64(size.X)), int(.86*float64(size.Y)))
						if profile == "camp" {
							stamp("camp_reset_top", rootScale, size.X/2+int((778-960)*rootScale), int(70*rootScale))
						}
						var last image.Point
						for sideIndex, side := range []config.SideLayout{cfg.Layout.Profile(profile).Left, cfg.Layout.Profile(profile).Right} {
							isSpecial := (specialSide == "left") == (sideIndex == 0)
							xs := make([]int, 4)
							y := int(math.Round(side.NominalCenters[0].Y * 900 * fit))
							margin := 0.0
							if float64(size.X)/float64(size.Y) >= 1.9 {
								margin = 70 * fit
								if sideIndex == 1 {
									margin = -20 * fit
								}
							}
							for i, p := range side.NominalCenters {
								x := p.X * 1600 * fit
								if sideIndex == 1 {
									x = float64(size.X) - (1600-p.X*1600)*fit
								}
								xs[i] = int(math.Round(x + margin))
							}
							// Sloping HP endpoint: geometry proof, not bead brightness.
							barY := 74.0
							if profile == "camp" {
								barY = 70
							}
							cy, step := int(math.Round(barY*fit)), max(1, int(math.Round(fit)))
							for row := -1; row <= 1; row++ {
								x0, x1 := xs[0]-row*step, xs[0]+int(200*fit)
								if sideIndex == 1 {
									x0, x1 = xs[0]-int(200*fit), xs[0]+row*step+1
								}
								c := color.RGBA{240, 70, 10, 255}
								if isSpecial && v.offset == 9 {
									c.B = 240
								}
								draw.Draw(base, image.Rect(x0, cy+row*step, x1, cy+row*step+1), image.NewUniform(c), image.Point{}, draw.Src)
							}
							lit, slots := blue, 4
							if isSpecial {
								lit, slots = v.lit, v.slots
								g := load(v.file)
								tw := int(math.Round(float64(g.Bounds().Dx()) * nameScale))
								cx := xs[0] + int(8*nameScale) + tw/2
								if sideIndex == 1 {
									cx = xs[0] - int(8*nameScale) - tw/2
								}
								stamp(v.file, nameScale, cx, int(8*nameScale)+int(math.Round(float64(g.Bounds().Dy())*nameScale))/2)
								y = int(math.Round(side.NominalCenters[0].Y*900*fit + v.offset*nameScale))
							}
							pitch := (side.NominalCenters[3].X - side.NominalCenters[0].X) * 1600 * fit / 3
							for i := 0; i < slots; i++ {
								x := int(math.Round(float64(xs[0]) + float64(i)*pitch))
								if i < 4 {
									x = xs[i]
								}
								rx, ry := max(2, int(math.Ceil(4.5*fit))), max(3, int(math.Ceil(5.5*fit)))
								draw.Draw(base, image.Rect(x-rx, y-ry, x+rx+1, y+ry+1), image.NewUniform(lit), image.Point{}, draw.Src)
								if isSpecial && i == slots-1 {
									last = image.Pt(x, y)
								}
							}
						}
						// Optional export lets the Android JNI check consume these exact
						// pixels, rather than independently recreating an easier fixture.
						out := ""
						if root := os.Getenv("NARUTO_MATRIX_OUT"); root != "" {
							out = filepath.Join(root, fmt.Sprintf("%dx%d-%s-%s-%d", size.X, size.Y, profile, specialSide, vi))
							if err := os.MkdirAll(out, 0755); err != nil {
								t.Fatal(err)
							}
							meta := fmt.Sprintf("%s\t%s\t%s\t%d\n", profile, specialSide, v.name, v.slots)
							if err := os.WriteFile(filepath.Join(out, "case.tsv"), []byte(meta), 0644); err != nil {
								t.Fatal(err)
							}
						}
						at := time.Unix(1700000000, 0)
						for i := 0; i < 3; i++ {
							img := image.NewRGBA(base.Bounds())
							draw.Draw(img, img.Bounds(), base, image.Point{}, draw.Src)
							if i > 0 {
								rx, ry := max(2, int(math.Ceil(4.5*fit))), max(3, int(math.Ceil(5.5*fit)))
								draw.Draw(img, image.Rect(last.X-rx, last.Y-ry, last.X+rx+1, last.Y+ry+1), image.NewUniform(v.dark), image.Point{}, draw.Src)
							}
							img.SetRGBA(size.X/2, size.Y/2, color.RGBA{uint8(50 + i), 40, 30, 255})
							if out != "" {
								f, err := os.Create(filepath.Join(out, fmt.Sprintf("%d.png", i)))
								if err != nil {
									t.Fatal(err)
								}
								err = png.Encode(f, img)
								closeErr := f.Close()
								if err != nil {
									t.Fatal(err)
								}
								if closeErr != nil {
									t.Fatal(closeErr)
								}
							}
							stamp := at.Add(time.Duration(i) * 100 * time.Millisecond)
							fr := frame.AnalyzeImage(img, eng, detect.ModeStretch, stamp, stamp, "synthetic-special-matrix")
							gotName, gotSlots := fr.LeftNinja, fr.LeftSlots
							if specialSide == "right" {
								gotName, gotSlots = fr.RightNinja, fr.RightSlots
							}
							l, r := countReady(fr.Beads)
							got, other := l, r
							if specialSide == "right" {
								got, other = r, l
							}
							want := v.slots
							if i > 0 {
								want--
							}
							if fr.Hold || !fr.Fighting || fr.LayoutProfile != profile || gotName != v.name || gotSlots != v.slots || got != want || other != 4 || !sideObservable(fr.Beads, true) || !sideObservable(fr.Beads, false) {
								t.Fatalf("frame %d: fight=%v hold=%v profile=%s name=%s slots=%d beans=%d/%d want=%d/4; %+v", i, fr.Fighting, fr.Hold, fr.LayoutProfile, gotName, gotSlots, got, other, want, fr.Beads)
							}
							s.provider = func() frame.Frame { return fr }
							s.captureOnce()
						}
						clock := &s.left
						if specialSide == "right" {
							clock = &s.right
						}
						if event, _ := clock.LastEvent(); clock.EventCount() != 1 || !event.Equal(at.Add(100*time.Millisecond)) {
							t.Fatalf("missing/incorrect substitute: events=%d at=%v", clock.EventCount(), event)
						}
						if remaining := s.oppRemaining(at.Add(200 * time.Millisecond)); len(remaining) != 1 || math.Abs(remaining[0]-14.9) > .001 {
							t.Fatalf("opponent countdown=%v", remaining)
						}
					})
				}
			}
		}
	}
}
