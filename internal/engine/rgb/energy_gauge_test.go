package rgb

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
	"narutotimer/internal/ninja"
)

var (
	gaugeOrange = color.RGBA{245, 150, 40, 255}
	beanGold    = color.RGBA{255, 200, 0, 255}
	beanBlue    = color.RGBA{40, 200, 255, 255}
	beanDark    = color.RGBA{28, 54, 98, 255}
	// Measured below real ordinary rows; it reads as a dark bean.
	sceneDark = color.RGBA{21, 40, 57, 255}
)

// energyHUD is a synthetic HUD (NOT a native capture) on the shipped default
// layout. Pixel sizes are 960x540 reference units at the frame's HUD scale.
type energyHUD struct {
	img  *image.RGBA
	area detect.ContentArea
	rows [2][]detect.BeadPosition
	s540 float64
}

func newEnergyHUD(t *testing.T, size image.Point, profile string) energyHUD {
	t.Helper()
	img := image.NewRGBA(image.Rectangle{Max: size})
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{32, 32, 32, 255}), image.Point{}, draw.Src)
	layout := engine.NewConfiguredLayout(detect.ModeAuto, config.Default().Layout)
	area, ok := layout.ContentArea(img)
	if !ok {
		t.Fatal("content area")
	}
	h := energyHUD{img: img, area: area, s540: area.HUDScale(960, 540)}
	for _, p := range layout.PositionsIn(profile, area) {
		side := 0
		if p.Side == "right" {
			side = 1
		}
		h.rows[side] = append(h.rows[side], p)
	}
	return h
}

func (h energyHUD) px(v float64) int { return int(math.Round(v * h.s540)) }

func (h energyHUD) fill(r image.Rectangle, c color.RGBA) {
	draw.Draw(h.img, r, image.NewUniform(c), image.Point{}, draw.Src)
}

// band fills [dy0, dy1]@540 relative to the default row, slightly wider than it.
func (h energyHUD) band(side int, dy0, dy1 float64, c color.RGBA) {
	row := h.rows[side]
	x0, x1 := min(row[0].X, row[3].X)-h.px(10), max(row[0].X, row[3].X)+h.px(10)
	h.fill(image.Rect(x0, row[0].Y+h.px(dy0), x1+1, row[0].Y+h.px(dy1)+1), c)
}

// gauge is the orange bar centered on the default row (2*half px@540 tall),
// from 8px@540 outside slot 1 to `beyond` px@540 past slot 4.
func (h energyHUD) gauge(side int, beyond, half float64) {
	row := h.rows[side]
	x0, x1 := row[0].X-h.px(8), row[3].X+h.px(beyond)
	if side == 1 {
		x0, x1 = row[3].X-h.px(beyond), row[0].X+h.px(8)
	}
	h.fill(image.Rect(x0, row[0].Y-h.px(half), x1+1, row[0].Y+h.px(half)+1), gaugeOrange)
}

// beans draws ~9.6x13px@540 diamonds dy@540 below the default row and
// returns their centers' y. The shift uses the identity row mapping.
func (h energyHUD) beans(side int, dy float64, colors [4]color.RGBA) [4]int {
	var ys [4]int
	rx, ry := h.px(4.8), h.px(6.5)
	for i, p := range h.rows[side] {
		y := h.area.MapHUDPixel(p.LX, p.LY+dy*detect.HUDReferenceHeight/540, p.Side, detect.HUDReferenceWidth, detect.HUDReferenceHeight).Y
		for yy := -ry; yy <= ry; yy++ {
			for xx := -rx; xx <= rx; xx++ {
				if detect.InBeadDiamond(0, 0, 2*rx, 2*ry, xx, yy) {
					h.img.SetRGBA(p.X+xx, y+yy, colors[i])
				}
			}
		}
		ys[i] = y
	}
	return ys
}

// title stamps the shipped complete title like the special HUD matrix does.
func (h energyHUD) title(t *testing.T, side int) {
	t.Helper()
	g := match.ToGray(decodeRGBA(t, "../../ninja/templates/minato_kyubi.png"))
	g = match.ScaleGray(g, h.px(float64(g.Bounds().Dx())), h.px(float64(g.Bounds().Dy())))
	first := h.rows[side][0]
	x := first.X + h.px(8)
	if side == 1 {
		x = first.X - h.px(8) - g.Bounds().Dx()
	}
	at := image.Pt(x, h.px(8))
	draw.Draw(h.img, image.Rectangle{Min: at, Max: at.Add(g.Bounds().Size())}, g, image.Point{}, draw.Src)
}

// portrait stamps catalog avatar 90316 (波风水门[九喇嘛连结]) like syntheticHUDFrame.
func (h energyHUD) portrait(t *testing.T, side int) {
	t.Helper()
	avatar := decodeRGBA(t, "../../../assets/avatars/90316.png")
	first := h.rows[side][0]
	roi := ninja.AvatarRegion(image.Pt(first.X, first.Y), h.s540, side == 0)
	size := int(math.Round(float64(avatar.Bounds().Dx()) * h.s540 * .6))
	center := roi.Min.Add(image.Pt(h.px(35.5), h.px(48)))
	box := image.Rectangle{Min: center.Sub(image.Pt(size/2, size/2)), Max: center.Add(image.Pt(size-size/2, size-size/2))}
	xdraw.BiLinear.Scale(h.img, box, avatar, avatar.Bounds(), draw.Over, nil)
}

// Expected reading of one side: every slot, only current dark slots (a
// retained title-gap hint never votes lit), or no slot at all.
const (
	readAll = iota
	readDarkOnly
	readNone
)

func checkSide(t *testing.T, res engine.Result, side int, colors [4]color.RGBA, ys [4]int, mode int) {
	t.Helper()
	prefix := "LR"[side]
	n := 0
	for _, b := range res.Beads {
		if b.Label == "" || b.Label[0] != prefix {
			continue
		}
		i := int(b.Label[1] - '1')
		if i < 0 || i >= 4 {
			t.Fatalf("unexpected slot %+v", b)
		}
		n++
		lit, gold := colors[i] != beanDark, colors[i] == beanGold
		if mode == readNone || mode == readDarkOnly && lit {
			if !b.Unknown || b.Lit {
				t.Fatalf("%s=%+v, want unknown\n%+v", b.Label, b, res.Beads)
			}
		} else if b.Unknown || b.Lit != lit || b.Gold != gold {
			t.Fatalf("%s=%+v, want lit=%v gold=%v\n%+v", b.Label, b, lit, gold, res.Beads)
		}
		if d := b.Y - ys[i]; mode != readNone && (d < -1 || d > 1) {
			t.Fatalf("%s sampled y=%d, want %d±1", b.Label, b.Y, ys[i])
		}
	}
	if n != 4 {
		t.Fatalf("side %c has %d slots: %+v", prefix, n, res.Beads)
	}
}

// Minato's orange energy gauge sits on the DEFAULT row and pushes the four
// beans 13px@540 lower. A verified title or the exact portrait reads the lower
// row for any gauge; without identity only a thin gauge band permits the
// re-read. A title gap keeps the row (dark votes only) until the hint expires.
// The other side is an ordinary row of real-shaped diamonds and never moves.
func TestEnergyGaugeRowWithAndWithoutIdentity(t *testing.T) {
	cfg := config.Default()
	G, B, D := beanGold, beanBlue, beanDark
	minato := [][4]color.RGBA{{G, B, D, D}, {G, G, G, D}, {D, D, D, D}, {B, B, B, B}}
	ordinary := [][4]color.RGBA{{G, G, G, G}, {B, G, D, D}}
	gauges := []struct {
		name         string
		beyond, half float64
	}{
		{"thin", 10, 2}, // blanks all four default cores
		{"edge", 3, 2},  // ends inside slot 4's gap probe: a false gold bean
		{"thick", 10, 4},
	}
	// Optional export for the Android Go/C++ parity drivers (gocheck/hostcheck).
	out := os.Getenv("NARUTO_ENERGY_OUT")
	n := 0
	for _, size := range []image.Point{{1280, 720}, {1600, 900}, {1920, 1080}, {2400, 1080}, {2880, 1800}} {
		for _, profile := range []string{"camp", "duel"} {
			for side := range 2 {
				for _, identity := range []string{"title", "portrait", "none"} {
					for _, gauge := range gauges {
						n++
						pattern, other := minato[n%len(minato)], ordinary[n%len(ordinary)]
						name := fmt.Sprintf("%dx%d-%s-%s-%s-%s", size.X, size.Y, profile, []string{"left", "right"}[side], identity, gauge.name)
						t.Run(name, func(t *testing.T) {
							paint := func(stamp bool) (energyHUD, [4]int, [4]int) {
								h := newEnergyHUD(t, size, profile)
								h.band(side, 8, 22, sceneDark)
								h.band(1-side, 8, 22, sceneDark)
								h.gauge(side, gauge.beyond, gauge.half)
								ys := h.beans(side, ninja.EnergyGaugeRowOffset, pattern)
								otherYs := h.beans(1-side, 0, other)
								if stamp && identity == "title" {
									h.title(t, side)
								}
								if stamp && identity == "portrait" {
									h.portrait(t, side)
								}
								return h, ys, otherYs
							}
							h, ys, otherYs := paint(true)
							gap, _, _ := paint(false)
							if out != "" {
								writeEnergyPNG(t, filepath.Join(out, name, "0.png"), h.img)
								if identity == "title" {
									writeEnergyPNG(t, filepath.Join(out, name, "1.png"), gap.img)
								}
							}
							// The default row alone is unusable: blank, or a false bean.
							_, plain := sampleCalibratedRow(gap.img, gap.rows[side], gap.area, cfg.Vision, "", false, ninja.Readout{}, image.Point{})
							if (gauge.name == "edge") == allUnknownStates(plain) || (gauge.name == "edge") != (detect.CountLight(plain) > 0) {
								t.Fatalf("%s gauge default row=%v", gauge.name, plain)
							}
							unidentified := readAll
							if gauge.name == "thick" {
								unidentified = readNone
							}
							e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
							at := time.Unix(1700000000, 0)
							res := e.AnalyzeAt(h.img, at)
							gotName := []string{res.LeftNinja, res.RightNinja}[side]
							wantName, mode := ninja.MinatoKyubi, readAll
							if identity == "none" {
								wantName, mode = "", unidentified
							}
							if res.LayoutProfile != profile || gotName != wantName || res.LeftSlots != 4 || res.RightSlots != 4 {
								t.Fatalf("profile=%s name=%q slots=%d/%d: %+v", res.LayoutProfile, gotName, res.LeftSlots, res.RightSlots, res)
							}
							checkSide(t, res, side, pattern, ys, mode)
							checkSide(t, res, 1-side, other, otherYs, readAll)
							if identity != "title" {
								return
							}
							// A brief title gap keeps the verified row like other four-slot
							// specials: only current dark cores vote there.
							res = e.AnalyzeAt(gap.img, at.Add(100*time.Millisecond))
							if res.LayoutProfile != profile || res.LeftNinja != "" || res.RightNinja != "" {
								t.Fatalf("title gap: %+v", res)
							}
							checkSide(t, res, side, pattern, ys, readDarkOnly)
							checkSide(t, res, 1-side, other, otherYs, readAll)
							// After the 1s hint expires the frame is unidentified.
							res = e.AnalyzeAt(gap.img, at.Add(1200*time.Millisecond))
							checkSide(t, res, side, pattern, ys, unidentified)
							checkSide(t, res, 1-side, other, otherYs, readAll)
						})
					}
				}
			}
		}
	}
}

// Ordinary rows are unaffected: a white flash or a broad gold wash may blank
// all four default cores while dark scenery below would read as a complete
// legal 0-lit row. Without a THIN gold band no re-read may replace them.
func TestEnergyGaugeFallbackIgnoresOrdinaryOcclusion(t *testing.T) {
	cfg := config.Default()
	for _, size := range []image.Point{{1280, 720}, {1920, 1080}, {2400, 1080}} {
		for _, cover := range []struct {
			name string
			c    color.RGBA
			dy   float64
		}{{"white-flash", color.RGBA{255, 255, 255, 255}, 3}, {"gold-wash", color.RGBA{255, 200, 0, 255}, 9}} {
			t.Run(fmt.Sprintf("%dx%d/%s", size.X, size.Y, cover.name), func(t *testing.T) {
				h := newEnergyHUD(t, size, "duel")
				for side := range 2 {
					h.band(side, 8, 22, sceneDark)
					h.beans(side, 0, [4]color.RGBA{beanBlue, beanBlue, beanDark, beanDark})
				}
				h.band(0, -cover.dy, cover.dy, cover.c)
				// The control is meaningful only if the lower row WOULD be accepted.
				dy := int(math.Round(ninja.EnergyGaugeRowOffset * h.s540))
				_, lower := sampleCalibratedRow(h.img, h.rows[0], h.area, cfg.Vision, "", false, ninja.Readout{}, image.Pt(0, dy))
				if dark := detect.StateDark; !slices.Equal(lower, []detect.BeadState{dark, dark, dark, dark}) {
					t.Fatalf("lower scenery should read as a complete dark row: %v", lower)
				}
				e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
				e.Prefer("duel")
				res := e.AnalyzeAt(h.img, time.Unix(1700000000, 0))
				for _, b := range res.Beads {
					if b.Label[0] == 'L' && !b.Unknown {
						t.Fatalf("covered ordinary row was replaced: %+v", res.Beads)
					}
				}
				if _, right := countReady(res.Beads); right != 2 || knownCount(res.Beads) != 4 {
					t.Fatalf("uncovered ordinary row changed: %+v", res.Beads)
				}
			})
		}
	}
}

func writeEnergyPNG(t *testing.T, path string, img *image.RGBA) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
