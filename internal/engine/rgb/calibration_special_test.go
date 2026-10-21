package rgb

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
	"narutotimer/internal/ninja"
)

// docs/区域校准.md 「校准值是标准忍者值」: a saved bead box is always the
// ORDINARY ninja's standard row. Every special row (MinatoKyubi/ItachiHyakusen
// +13@540, SasukeXiayin +9@540, Hashirama's slots 5/6, the warm/purple/red
// palettes) is derived by the engine from that side's standard values and
// calibrated scale. These are synthetic devices (NOT native captures) whose
// HUD the automatic layout misses.

// calibrationDevice is a synthetic device and the profile of the ordinary
// frame its boxes were drawn on.
type calibrationDevice struct {
	name    string
	size    image.Point
	sides   [2]deviceSide
	profile string
}

var calibrationDevices = []calibrationDevice{
	{"tablet-2388x1668", image.Pt(2388, 1668), tabletSides, "duel"},
	{"ultrawide-3440x1440", image.Pt(3440, 1440), ultrawideSides, "camp"},
	// 16:10 tablet whose sides have their own HUD scale (1.78 / 1.70), both
	// off the automatic width fit (1.6): every derived row must use its side's.
	{"tablet-2560x1600", image.Pt(2560, 1600), [2]deviceSide{{318, 190, 1.78}, {2200, 180, 1.70}}, "duel"},
}

// ordinary is the ordinary-ninja frame the player calibrates on: the
// standard rows, ordinary portraits, health bars above the rows and the dark
// scenery measured below real rows (it reads as a complete row of dark beans
// 13@540 lower, so a false energy-gauge re-read would be accepted there).
func (d calibrationDevice) ordinary(t *testing.T, profile string, rows [2][]slotBean) (*deviceHUD, [2][]image.Point) {
	t.Helper()
	h := newDeviceHUD(d.size, d.sides)
	var centres [2][]image.Point
	for side := range 2 {
		h.healthBar(side, profile)
		h.scenery(side, profile)
		centres[side] = h.beans(side, profile, 0, beanColors(rows[side]))
		h.backedPortrait(t, side, profile, ordinaryAvatars[side].id)
	}
	return h, centres
}

// backedPortrait is portrait over a black diamond backing. The catalog's
// gray is premultiplied (transparent = black), so blended avatar edges then
// match it; over the plain dark-gray background the margins of some synthetic
// portraits (920541, 90464) hinge on sub-pixel placement and on the window's
// clipping at the frame top, on the automatic layout as much as calibrated.
func (h *deviceHUD) backedPortrait(t *testing.T, side int, profile, id string) detect.PixelBox {
	t.Helper()
	cx, cy, half := h.portraitDiamond(side, profile)
	inner := half - 2*h.s960(side)
	for y := int(cy - half); y <= int(cy+half)+1; y++ {
		for x := int(cx - half); x <= int(cx+half)+1; x++ {
			if math.Abs(float64(x)-cx)+math.Abs(float64(y)-cy) < inner {
				h.img.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}
	return h.portrait(t, side, profile, id)
}

// standard is the calibration saved on an ordinary frame of the device:
// both bead boxes, plus both portrait boxes when portraits is set.
func (d calibrationDevice) standard(t *testing.T, portraits bool) *config.RegionCalibration {
	t.Helper()
	ref, _ := d.ordinary(t, d.profile, [2][]slotBean{{sBlue, sDark, sDark, sDark}, {sGold, sGold, sDark, sDark}})
	c := &config.RegionCalibration{Profile: d.profile, LeftBeads: ref.beadBox(0, d.profile), RightBeads: ref.beadBox(1, d.profile)}
	if portraits {
		c.LeftAvatar, c.RightAvatar = ref.normalized(ref.portraitBox(0, d.profile)), ref.normalized(ref.portraitBox(1, d.profile))
	}
	return c
}

// slotBean is one drawn slot and how it must read.
type slotBean struct {
	c         color.RGBA
	lit, gold bool
}

var (
	sBlue = slotBean{calibBlue, true, false}
	sGold = slotBean{calibGold, true, true}
	sDark = slotBean{calibDark, false, false}
	sWarm = slotBean{calibWarm, true, false}
	// The warm palette reads a saturated gold core as a warm light: lit, not Gold.
	sWarmGold   = slotBean{calibGold, true, false}
	sPurple     = slotBean{color.RGBA{220, 60, 250, 255}, true, false}
	sPurpleDark = slotBean{color.RGBA{40, 15, 80, 255}, false, false}
	sRed        = slotBean{color.RGBA{240, 95, 80, 255}, true, false}
	sRedDark    = slotBean{color.RGBA{40, 10, 15, 255}, false, false}
)

func beanColors(beans []slotBean) []color.RGBA {
	out := make([]color.RGBA, len(beans))
	for i, b := range beans {
		out[i] = b.c
	}
	return out
}

// ordinaryAvatars are the ordinary portraits (left, right) and their names.
var ordinaryAvatars = [2]struct{ id, name string }{{"90106", "宇智波斑[秽土转生]"}, {"90244", "旗木卡卡西[须佐能乎]"}}

// ordinaryRows are mixed blue/gold/dark rows of ordinary ninjas.
var ordinaryRows = [][]slotBean{
	{sBlue, sBlue, sDark, sDark},
	{sGold, sGold, sGold, sGold},
	{sGold, sBlue, sBlue, sDark},
	{sDark, sDark, sDark, sDark},
	{sBlue, sBlue, sBlue, sBlue},
}

// wantBeans checks one side's labels, states, gold flags and sampled
// centres (±1px) against the drawn slots.
func wantBeans(t *testing.T, res engine.Result, side int, beans []slotBean, centres []image.Point) {
	t.Helper()
	prefix := "LR"[side]
	n := 0
	for _, b := range res.Beads {
		if b.Label == "" || b.Label[0] != prefix {
			continue
		}
		i, _ := strconv.Atoi(b.Label[1:])
		i--
		if i < 0 || i >= len(beans) {
			t.Fatalf("unexpected slot %+v in %+v", b, res.Beads)
		}
		n++
		if b.Unknown || b.Lit != beans[i].lit || b.Gold != beans[i].gold {
			t.Fatalf("%s=%+v, want lit=%v gold=%v\n%+v", b.Label, b, beans[i].lit, beans[i].gold, res.Beads)
		}
		if d := image.Pt(b.X, b.Y).Sub(centres[i]); d.X < -1 || d.X > 1 || d.Y < -1 || d.Y > 1 {
			t.Fatalf("%s sampled at (%d,%d), drawn at %v\n%+v", b.Label, b.X, b.Y, centres[i], res.Beads)
		}
	}
	if n != len(beans) {
		t.Fatalf("side %c has %d slots, want %d: %+v", prefix, n, len(beans), res.Beads)
	}
}

// healthBar paints the ordinary HP bar (three sloping scanlines) above the
// standard row, from bead 0 inwards like the real HUD (special_resolution_test).
func (h *deviceHUD) healthBar(side int, profile string) {
	bx, _ := h.bead0(side, profile)
	c0 := nominalCentres(profile, side)[0]
	y := 74.0
	if profile == "camp" {
		y = 70
	}
	_, by := h.point(side, c0.X*detect.HUDReferenceWidth, y)
	s := h.sides[side].s
	cy, step, length := int(math.Round(by)), max(1, int(math.Round(s))), int(200*s)
	x := int(math.Round(bx))
	for row := -1; row <= 1; row++ {
		x0, x1 := x-row*step, x+length
		if side == 1 {
			x0, x1 = x-length, x+row*step+1
		}
		draw.Draw(h.img, image.Rect(x0, cy+row*step, x1, cy+row*step+1), image.NewUniform(color.RGBA{240, 70, 10, 255}), image.Point{}, draw.Src)
	}
}

// scenery paints energy_gauge_test.go's sceneDark 8..22@540 below the side's
// standard row, 10@540 wider than slots 1..4.
func (h *deviceHUD) scenery(side int, profile string) {
	c := nominalCentres(profile, side)
	s := h.s960(side)
	x0, y := h.point(side, c[0].X*detect.HUDReferenceWidth, c[0].Y*detect.HUDReferenceHeight)
	x3, _ := h.point(side, c[3].X*detect.HUDReferenceWidth, c[3].Y*detect.HUDReferenceHeight)
	r := image.Rect(int(math.Round(min(x0, x3)-10*s)), int(math.Round(y+8*s)), int(math.Round(max(x0, x3)+10*s))+1, int(math.Round(y+22*s))+1)
	draw.Draw(h.img, r, image.NewUniform(sceneDark), image.Point{}, draw.Src)
}

// gauge paints the thin orange energy gauge (±2@540) ON the side's standard
// row, from 8@540 outside slot 1 to 10@540 past slot 4 (energy_gauge_test.go
// "thin"). Minato's and Itachi's beans then sit 13@540 lower.
func (h *deviceHUD) gauge(side int, profile string) {
	c := nominalCentres(profile, side)
	s := h.s960(side)
	x0, y := h.point(side, c[0].X*detect.HUDReferenceWidth, c[0].Y*detect.HUDReferenceHeight)
	x3, _ := h.point(side, c[3].X*detect.HUDReferenceWidth, c[3].Y*detect.HUDReferenceHeight)
	outer, inner := x0-8*s, x3+10*s
	if side == 1 {
		outer, inner = x0+8*s, x3-10*s
	}
	cy, half := int(math.Round(y)), int(math.Round(2*s))
	r := image.Rect(int(math.Round(min(outer, inner))), cy-half, int(math.Round(max(outer, inner)))+1, cy+half+1)
	draw.Draw(h.img, r, image.NewUniform(gaugeOrange), image.Point{}, draw.Src)
}

// itachiPortrait stamps 百战鼬's right-HUD portrait where a calibrated right
// side checks it: (8, 9) units inside the portrait window of box (docs/区域校准.md).
func (h *deviceHUD) itachiPortrait(t *testing.T, box detect.PixelBox) {
	t.Helper()
	window, scale := ninja.AvatarRegionFromBox(box.X, box.Y, box.W, box.H, false)
	g := match.ToGray(decodeRGBA(t, "../../ninja/templates/itachi_hyakusen_portrait.png"))
	s := h.s960(1)
	g = match.ScaleGray(g, int(math.Round(float64(g.Bounds().Dx())*s)), int(math.Round(float64(g.Bounds().Dy())*s)))
	at := window.Min.Add(image.Pt(int(math.Round(8*scale)), int(math.Round(9*scale))))
	draw.Draw(h.img, image.Rectangle{Min: at, Max: at.Add(g.Bounds().Size())}, g, image.Point{}, draw.Src)
}

// standardRowDrop is the drop of a side's sampled row below its reported
// standard bead box, in pixels: the calibration window's d·pitch.
func standardRowDrop(res engine.Result, side int, frame image.Rectangle) float64 {
	g := res.Geometry[side]
	sum := 0.0
	for _, p := range g.Beads[:4] {
		sum += float64(p.Y)
	}
	return sum/4 - (float64(frame.Min.Y) + (g.BeadBox.Y+g.BeadBox.Height/2)*float64(frame.Dy()))
}

// wantStandardBox requires a side's reported default bead box to be the
// standard (ordinary-ninja) box of this frame's profile, whatever the row read.
func wantStandardBox(t *testing.T, res engine.Result, side int, h *deviceHUD, profile string) {
	t.Helper()
	got, want := res.Geometry[side].BeadBox, h.beadBox(side, profile)
	w, hh := float64(h.img.Bounds().Dx()), float64(h.img.Bounds().Dy())
	if math.Abs(got.X-want.X)*w > .01 || math.Abs(got.Width-want.Width)*w > .01 ||
		math.Abs((got.Y+got.Height/2)-(want.Y+want.Height/2))*hh > .01 {
		t.Fatalf("side %d: reported bead box %+v, want the standard box %+v", side, got, *want)
	}
}

// calibratedSpecial is one special variant: its evidence, its drawn row and
// the row drop (960x540 units) the engine must derive.
type calibratedSpecial struct {
	name   string
	ninja  string    // expected name; "" = unidentified (energy-gauge fallback)
	titles [2]string // name strip per side ("" = none)
	avatar string    // catalog portrait in the diamond frame
	// itachi stamps 百战鼬's right-HUD portrait at the calibrated window.
	itachi    bool
	gauge     bool
	offset    float64
	rightOnly bool
	// portraits: the variant is identified by its portrait, so the saved
	// calibration also holds the portrait boxes (drawn on the ordinary frame).
	portraits bool
	patterns  [][]slotBean
}

func calibratedSpecials() []calibratedSpecial {
	B, G, D, W, WG := sBlue, sGold, sDark, sWarm, sWarmGold
	P, PD, R, RD := sPurple, sPurpleDark, sRed, sRedDark
	return []calibratedSpecial{
		{name: "minato-title", ninja: ninja.MinatoKyubi, titles: [2]string{"minato_kyubi", "minato_kyubi"}, gauge: true, offset: ninja.EnergyGaugeRowOffset,
			patterns: [][]slotBean{{G, B, D, D}, {B, B, B, D}, {D, D, D, D}, {G, G, G, G}}},
		{name: "minato-portrait", ninja: ninja.MinatoKyubi, avatar: "90316", gauge: true, offset: ninja.EnergyGaugeRowOffset, portraits: true,
			patterns: [][]slotBean{{B, B, B, D}, {G, B, D, D}, {B, D, D, D}}},
		// Neither title nor portrait: only the thin gauge on the standard row
		// permits the guarded re-read, at the side's calibrated scale.
		{name: "minato-gauge-only", gauge: true, offset: ninja.EnergyGaugeRowOffset,
			patterns: [][]slotBean{{G, G, D, D}, {B, B, B, B}, {B, D, D, D}, {D, D, D, D}}},
		{name: "itachi-portrait", ninja: ninja.ItachiHyakusen, avatar: "920541", gauge: true, offset: ninja.EnergyGaugeRowOffset, portraits: true,
			patterns: [][]slotBean{{G, B, D, D}, {B, B, B, B}, {B, D, D, D}}},
		// The right-HUD portrait check follows the calibrated portrait window:
		// from the saved portrait box, or the window of the calibrated bead 0.
		{name: "itachi-title-portrait-box", ninja: ninja.ItachiHyakusen, titles: [2]string{"", "itachi_hyakusen"}, itachi: true, gauge: true, offset: ninja.EnergyGaugeRowOffset, rightOnly: true, portraits: true,
			patterns: [][]slotBean{{B, B, G, D}, {D, D, D, D}}},
		{name: "itachi-title-beads-only", ninja: ninja.ItachiHyakusen, titles: [2]string{"", "itachi_hyakusen"}, itachi: true, gauge: true, offset: ninja.EnergyGaugeRowOffset, rightOnly: true,
			patterns: [][]slotBean{{G, G, G, D}, {B, D, D, D}}},
		{name: "xiayin-purple", ninja: ninja.SasukeXiayin, titles: [2]string{"sasuke_xiayin", "sasuke_xiayin"}, offset: 9,
			patterns: [][]slotBean{{P, P, PD, PD}, {P, P, P, P}, {P, PD, PD, PD}, {P, P, P, D}}},
		{name: "xiayin-red", ninja: ninja.SasukeXiayin, titles: [2]string{"sasuke_xiayin_duel", "sasuke_xiayin_duel"}, offset: 9,
			patterns: [][]slotBean{{R, RD, RD, RD}, {R, R, R, RD}, {RD, RD, RD, RD}, {R, R, RD, RD}}},
		{name: "xiayin-portrait", ninja: ninja.SasukeXiayin, avatar: "90464", offset: 9, portraits: true,
			patterns: [][]slotBean{{P, PD, PD, PD}, {R, R, RD, RD}}},
		{name: "obito", ninja: ninja.Obito, titles: [2]string{"obito", "obito_current"},
			patterns: [][]slotBean{{P, P, PD, D}, {P, P, P, PD}, {PD, PD, PD, PD}}},
		{name: "naruto", ninja: ninja.Naruto, titles: [2]string{"naruto", "naruto_right"},
			patterns: [][]slotBean{{R, R, RD, D}, {R, R, R, R}, {R, RD, RD, RD}}},
		{name: "hashirama", ninja: ninja.Hashirama, titles: [2]string{"hashirama", "hashirama_alt"},
			patterns: [][]slotBean{{W, W, B, W, D, D}, {W, W, W, W, W, D}, {B, B, D, D, D, D}, {W, WG, W, W, W, W}}},
		{name: "madara", ninja: ninja.Madara, titles: [2]string{"madara", "madara"},
			patterns: [][]slotBean{{W, B, D, D}, {W, W, W, W}, {WG, W, W, D}}},
	}
}

// analyzeCalibrated runs a fresh engine with c (scene-free: no profile hint,
// so camp vs duel is picked from the rows like gocheck/hostcheck).
func analyzeCalibrated(c *config.RegionCalibration, img *image.RGBA) engine.Result {
	cfg := config.Default()
	layout := cfg.Layout
	layout.Calibration = c
	return NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, layout), cfg.Vision).AnalyzeAt(img, time.Unix(1700000000, 0))
}

// exportCalibrated writes a frame and its --calibration for gocheck/hostcheck.
func exportCalibrated(t *testing.T, dir string, img *image.RGBA, c *config.RegionCalibration) {
	t.Helper()
	writeEnergyPNG(t, filepath.Join(dir, "0.png"), img)
	if err := os.WriteFile(filepath.Join(dir, "calibration.txt"), []byte(calibrationArg(c)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// On every device, profile and side, with ONLY standard boxes saved earlier on
// an ordinary frame (bead boxes; portrait boxes where the variant is identified
// by its portrait), each special variant reads its name, slots and states at
// the derived row. The other side stays an ordinary row in the same frame,
// both sides still report the standard bead box, and saving the reported
// boxes again reproduces the analysis (the standard values do not drift).
func TestRegionCalibrationDerivesEverySpecialVariant(t *testing.T) {
	out := os.Getenv("NARUTO_CALIB_OUT")
	for di, d := range calibrationDevices {
		calibrations := map[bool]*config.RegionCalibration{false: d.standard(t, false), true: d.standard(t, true)}
		for pi, profile := range []string{"camp", "duel"} {
			for side := range 2 {
				for _, v := range calibratedSpecials() {
					if v.rightOnly && side == 0 {
						continue
					}
					name := fmt.Sprintf("%s-%s-%s-%s", d.name, profile, []string{"left", "right"}[side], v.name)
					t.Run(name, func(t *testing.T) {
						beans := v.patterns[(di*4+pi*2+side)%len(v.patterns)]
						others := ordinaryRows[(di+pi+side)%len(ordinaryRows)]
						h := newDeviceHUD(d.size, d.sides)
						h.healthBar(0, profile)
						h.healthBar(1, profile)
						if v.gauge {
							h.gauge(side, profile)
						}
						centres := h.beans(side, profile, v.offset, beanColors(beans))
						otherCentres := h.beans(1-side, profile, 0, beanColors(others))
						h.backedPortrait(t, 1-side, profile, ordinaryAvatars[1-side].id)
						if v.titles[side] != "" {
							h.title(t, side, profile, v.titles[side])
						}
						if v.avatar != "" {
							h.backedPortrait(t, side, profile, v.avatar)
						}
						if v.itachi {
							h.itachiPortrait(t, h.portraitBox(side, profile))
						}
						c := calibrations[v.portraits]
						if out != "" {
							exportCalibrated(t, filepath.Join(out, "special-"+name), h.img, c)
						}
						res := analyzeCalibrated(c, h.img)
						names, slots := [2]string{res.LeftNinja, res.RightNinja}, [2]int{res.LeftSlots, res.RightSlots}
						if res.LayoutProfile != profile || names[side] != v.ninja || names[1-side] != ordinaryAvatars[1-side].name || slots[side] != len(beans) || slots[1-side] != 4 {
							t.Fatalf("profile=%s names=%q/%q slots=%d/%d %+v", res.LayoutProfile, res.LeftNinja, res.RightNinja, res.LeftSlots, res.RightSlots, res.Beads)
						}
						wantBeans(t, res, side, beans, centres)
						wantBeans(t, res, 1-side, others, otherCentres)
						// The engine still reports both standard rows; the window's
						// d·pitch is the variant's drop at this side's calibrated scale.
						for i := range 2 {
							wantStandardBox(t, res, i, h, profile)
						}
						if drop, want := standardRowDrop(res, side, h.img.Bounds()), v.offset*h.s960(side); math.Abs(drop-want) > 1 {
							t.Fatalf("special row drop %.2fpx, want %.2fpx", drop, want)
						}
						if drop := standardRowDrop(res, 1-side, h.img.Bounds()); math.Abs(drop) > 1 {
							t.Fatalf("ordinary row drop %.2fpx", drop)
						}
						if v.ninja == "" {
							// The automatic scale would re-read a different row here:
							// the fallback provably used the calibrated side scale.
							auto, _ := engine.NewConfiguredLayout(detect.ModeAuto, config.Default().Layout).ContentArea(h.img)
							if d.name != "ultrawide-3440x1440" && math.Abs(math.Round(v.offset*auto.HUDScale(960, 540))-math.Round(v.offset*h.s960(side))) < 2 {
								t.Fatalf("device scale %.3f does not discriminate the automatic %.3f", h.s960(side), auto.HUDScale(960, 540))
							}
						}
						again := analyzeCalibrated(defaultBoxes(res, true, v.portraits), h.img)
						sameAnalysis(t, "saved reported boxes", again, res)
					})
				}
			}
		}
	}
}

// Negative controls on the same devices: an ordinary frame under the standard
// calibration reads its standard rows unchanged (no drop, four slots, ordinary
// names, the saved boxes reported back), and boxes misdrawn by a few pixels
// (shifted, ±3% scale) or by a quarter pitch sideways (the box's dots still
// on the beads) never create a special row: no special name, no slots 5/6 and
// no 13/9@540 row drop. One side is always all gold over dark scenery: gold
// diamonds sampled near their tips must not pass for an energy gauge.
func TestRegionCalibrationOrdinaryControls(t *testing.T) {
	out := os.Getenv("NARUTO_CALIB_OUT")
	misdrawn := []struct {
		dx, dy, scale float64 // pixels; relative width
		pitches       float64 // bead boxes only: extra x shift in bead pitches
	}{{3, 3, 1, 0}, {-3, -2, 1.03, 0}, {2, -3, .97, 0}, {-2, 4, 1, 0}, {0, 0, 1, .25}, {0, 2, 1, -.25}}
	allGold := []slotBean{sGold, sGold, sGold, sGold}
	for di, d := range calibrationDevices {
		full := d.standard(t, true)
		for pi, profile := range []string{"camp", "duel"} {
			mixed := ordinaryRows[[]int{0, 2, 3, 4}[(di*2+pi)%4]]
			rows := [2][]slotBean{allGold, mixed}
			if (di+pi)%2 == 1 {
				rows = [2][]slotBean{mixed, allGold}
			}
			t.Run(fmt.Sprintf("%s-%s", d.name, profile), func(t *testing.T) {
				h, centres := d.ordinary(t, profile, rows)
				if out != "" {
					exportCalibrated(t, filepath.Join(out, fmt.Sprintf("control-%s-%s", d.name, profile)), h.img, full)
				}
				res := analyzeCalibrated(full, h.img)
				if res.LayoutProfile != profile || res.LeftNinja != ordinaryAvatars[0].name || res.RightNinja != ordinaryAvatars[1].name || res.LeftSlots != 4 || res.RightSlots != 4 {
					t.Fatalf("ordinary: profile=%s names=%q/%q slots=%d/%d %+v", res.LayoutProfile, res.LeftNinja, res.RightNinja, res.LeftSlots, res.RightSlots, res.Beads)
				}
				for side := range 2 {
					wantBeans(t, res, side, rows[side], centres[side])
					wantStandardBox(t, res, side, h, profile)
					if drop := standardRowDrop(res, side, h.img.Bounds()); math.Abs(drop) > 1 {
						t.Fatalf("ordinary side %d drop %.2fpx", side, drop)
					}
				}
				sameAnalysis(t, "saved reported boxes", analyzeCalibrated(defaultBoxes(res, true, true), h.img), res)
				W, H := float64(h.img.Bounds().Dx()), float64(h.img.Bounds().Dy())
				for mi, m := range misdrawn {
					// Every box of the player's calibration misdrawn alike.
					off := func(r *config.NormalizedRect, scale, pitches float64) *config.NormalizedRect {
						cx, cy := r.X+r.Width/2, r.Y+r.Height/2
						w, hh := r.Width*scale, r.Height*scale
						return &config.NormalizedRect{X: cx - w/2 + m.dx/W + pitches*r.Width/4, Y: cy - hh/2 + m.dy/H, Width: w, Height: hh}
					}
					c := &config.RegionCalibration{Profile: full.Profile, LeftBeads: off(full.LeftBeads, m.scale, m.pitches), RightBeads: off(full.RightBeads, m.scale, m.pitches),
						LeftAvatar: off(full.LeftAvatar, 1, 0), RightAvatar: off(full.RightAvatar, 1, 0)}
					if out != "" {
						exportCalibrated(t, filepath.Join(out, fmt.Sprintf("control-%s-%s-misdrawn%d", d.name, profile, mi)), h.img, c)
					}
					res := analyzeCalibrated(c, h.img)
					if res.LeftSlots != 4 || res.RightSlots != 4 {
						t.Fatalf("misdrawn %d: slots %d/%d %+v", mi, res.LeftSlots, res.RightSlots, res.Beads)
					}
					for side, got := range [2]string{res.LeftNinja, res.RightNinja} {
						if got != "" && got != ordinaryAvatars[side].name {
							t.Fatalf("misdrawn %d: side %d named %q", mi, side, got)
						}
					}
					// Losing the profile pick would be allowed only if it then read
					// nothing. On the frame's profile each centre stays a few pixels
					// from the standard row (never dropped) and no vote contradicts
					// the drawn bean.
					for _, b := range res.Beads {
						side, i := 0, int(b.Label[1]-'1')
						if b.Label[0] == 'R' {
							side = 1
						}
						if res.LayoutProfile != profile {
							if !b.Unknown {
								t.Fatalf("misdrawn %d: other profile %s read %+v", mi, res.LayoutProfile, res.Beads)
							}
							continue
						}
						if dy := b.Y - centres[side][i].Y; dy < -6 || dy > 6 {
							t.Fatalf("misdrawn %d: %s sampled %dpx off the standard row: %+v", mi, b.Label, dy, res.Beads)
						}
						if !b.Unknown && (b.Lit != rows[side][i].lit || b.Gold != rows[side][i].gold) {
							t.Fatalf("misdrawn %d: %s=%+v, drawn %+v", mi, b.Label, b, rows[side][i])
						}
					}
				}
			})
		}
	}
}
