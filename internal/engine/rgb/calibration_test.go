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
	"strings"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
	"narutotimer/internal/ninja"
)

// deviceSide is one HUD side of a synthetic device (NOT a native capture)
// whose HUD the automatic layout does not predict: its own scale s (pixels
// per 1600x900 reference unit) and duel bead-0 centre (x0, y0).
type deviceSide struct{ x0, y0, s float64 }

type deviceHUD struct {
	img   *image.RGBA
	sides [2]deviceSide
}

func newDeviceHUD(size image.Point, sides [2]deviceSide) *deviceHUD {
	img := image.NewRGBA(image.Rectangle{Max: size})
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{32, 32, 32, 255}), image.Point{}, draw.Src)
	return &deviceHUD{img: img, sides: sides}
}

func nominalCentres(profile string, side int) []config.NormalizedPoint {
	p := config.Default().Layout.Profile(profile)
	if side == 1 {
		return p.Right.NominalCenters
	}
	return p.Left.NominalCenters
}

// point maps a 1600x900 reference point through this device's side transform.
func (h *deviceHUD) point(side int, x, y float64) (float64, float64) {
	d, c0 := h.sides[side], nominalCentres("duel", side)[0]
	return d.x0 + d.s*(x-c0.X*detect.HUDReferenceWidth), d.y0 + d.s*(y-c0.Y*detect.HUDReferenceHeight)
}

func (h *deviceHUD) s960(side int) float64 { return h.sides[side].s * detect.HUDReferenceWidth / 960 }

// beans draws one diamond per colour at the profile's slots (slots 5/6
// extrapolated), dy reference units (960x540) lower; returns their centres.
func (h *deviceHUD) beans(side int, profile string, dy float64, colors []color.RGBA) []image.Point {
	c := nominalCentres(profile, side)
	pitch := (c[3].X - c[0].X) * detect.HUDReferenceWidth / 3
	s := h.s960(side)
	rx, ry := max(2, int(math.Round(4.8*s))), max(3, int(math.Round(6.5*s)))
	var out []image.Point
	for i, col := range colors {
		x := c[0].X*detect.HUDReferenceWidth + float64(i)*pitch
		if i < 4 {
			x = c[i].X * detect.HUDReferenceWidth
		}
		fx, fy := h.point(side, x, c[0].Y*detect.HUDReferenceHeight+dy*detect.HUDReferenceHeight/540)
		p := image.Pt(int(math.Round(fx)), int(math.Round(fy)))
		for yy := -ry; yy <= ry; yy++ {
			for xx := -rx; xx <= rx; xx++ {
				if detect.InBeadDiamond(0, 0, 2*rx, 2*ry, xx, yy) {
					h.img.SetRGBA(p.X+xx, p.Y+yy, col)
				}
			}
		}
		out = append(out, p)
	}
	return out
}

// bead0 is the profile's bead-0 centre on this device.
func (h *deviceHUD) bead0(side int, profile string) (float64, float64) {
	c := nominalCentres(profile, side)[0]
	return h.point(side, c.X*detect.HUDReferenceWidth, c.Y*detect.HUDReferenceHeight)
}

// portraitDiamond is the centre and half diagonal of the diamond portrait
// frame where the real HUD puts it relative to bead 0.
func (h *deviceHUD) portraitDiamond(side int, profile string) (cx, cy, half float64) {
	bx, by := h.bead0(side, profile)
	s := h.s960(side)
	dx := -38.0
	if side == 1 {
		dx = 41
	}
	return bx + dx*s, by - 12.5*s, 68.5 * s / 2
}

// portraitBox is the diamond frame's bounding box: the box a player draws.
func (h *deviceHUD) portraitBox(side int, profile string) detect.PixelBox {
	cx, cy, half := h.portraitDiamond(side, profile)
	return detect.PixelBox{X: cx - half, Y: cy - half, W: 2 * half, H: 2 * half}
}

// portrait stamps a catalog avatar inside a diamond frame where the real HUD
// puts it relative to bead 0 and returns the frame's bounding box: the box a
// player draws.
func (h *deviceHUD) portrait(t *testing.T, side int, profile, id string) detect.PixelBox {
	t.Helper()
	cx, cy, half := h.portraitDiamond(side, profile)
	s := h.s960(side)
	frame := color.RGBA{150, 40, 40, 255}
	for y := int(cy - half); y <= int(cy+half)+1; y++ {
		for x := int(cx - half); x <= int(cx+half)+1; x++ {
			if d := math.Abs(float64(x)-cx) + math.Abs(float64(y)-cy); d <= half && d >= half-2*s {
				h.img.SetRGBA(x, y, frame)
			}
		}
	}
	avatar := decodeRGBA(t, "../../../assets/avatars/"+id+".png")
	size := int(math.Round(float64(avatar.Bounds().Dx()) * s * .6))
	at := image.Pt(int(math.Round(cx))-size/2, int(math.Round(cy))-size/2)
	xdraw.BiLinear.Scale(h.img, image.Rectangle{Min: at, Max: at.Add(image.Pt(size, size))}, avatar, avatar.Bounds(), draw.Over, nil)
	return h.portraitBox(side, profile)
}

// title stamps a shipped name strip above bead 0 like the real HUD.
func (h *deviceHUD) title(t *testing.T, side int, profile, file string) {
	t.Helper()
	g := match.ToGray(decodeRGBA(t, "../../ninja/templates/"+file+".png"))
	s := h.s960(side)
	g = match.ScaleGray(g, int(math.Round(float64(g.Bounds().Dx())*s)), int(math.Round(float64(g.Bounds().Dy())*s)))
	bx, by := h.bead0(side, profile)
	x := int(math.Round(bx + 8*s))
	if side == 1 {
		x = int(math.Round(bx-8*s)) - g.Bounds().Dx()
	}
	at := image.Pt(x, int(math.Round(by-53.8*s)))
	draw.Draw(h.img, image.Rectangle{Min: at, Max: at.Add(g.Bounds().Size())}, g, image.Point{}, draw.Src)
}

// beadBox is the player's bead box (frame fractions) around the profile's
// four ordinary slots.
func (h *deviceHUD) beadBox(side int, profile string) *config.NormalizedRect {
	c := nominalCentres(profile, side)
	x0, y0 := h.bead0(side, profile)
	pitch := math.Abs(c[3].X-c[0].X) * detect.HUDReferenceWidth / 3 * h.sides[side].s
	left := x0 - pitch/2
	if side == 1 {
		left = x0 + pitch/2 - 4*pitch
	}
	height := 13 * h.s960(side)
	b := h.img.Bounds()
	return &config.NormalizedRect{X: left / float64(b.Dx()), Y: (y0 - height/2) / float64(b.Dy()), Width: 4 * pitch / float64(b.Dx()), Height: height / float64(b.Dy())}
}

func (h *deviceHUD) normalized(box detect.PixelBox) *config.NormalizedRect {
	b := h.img.Bounds()
	return &config.NormalizedRect{X: box.X / float64(b.Dx()), Y: box.Y / float64(b.Dy()), Width: box.W / float64(b.Dx()), Height: box.H / float64(b.Dy())}
}

// calibrationArg is the gocheck/hostcheck --calibration form of c.
func calibrationArg(c *config.RegionCalibration) string {
	rect := func(r *config.NormalizedRect) string {
		if r == nil {
			return "-"
		}
		f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
		return strings.Join([]string{f(r.X), f(r.Y), f(r.Width), f(r.Height)}, ",")
	}
	return c.Profile + ":" + strings.Join([]string{rect(c.LeftBeads), rect(c.RightBeads), rect(c.LeftAvatar), rect(c.RightAvatar)}, "/")
}

var (
	calibBlue = color.RGBA{40, 200, 255, 255}
	calibDark = color.RGBA{28, 54, 98, 255}
	calibGold = color.RGBA{255, 200, 0, 255}
	calibWarm = color.RGBA{240, 65, 18, 255}
)

// wantRow checks one side's labels, states and sampled centres.
func wantRow(t *testing.T, res engine.Result, side int, colors []color.RGBA, centres []image.Point) {
	t.Helper()
	prefix := "LR"[side]
	n := 0
	for _, b := range res.Beads {
		if b.Label == "" || b.Label[0] != prefix {
			continue
		}
		i, _ := strconv.Atoi(b.Label[1:])
		i--
		if i < 0 || i >= len(colors) {
			t.Fatalf("unexpected slot %+v in %+v", b, res.Beads)
		}
		n++
		lit, gold := colors[i] != calibDark, colors[i] == calibGold
		if b.Unknown || b.Lit != lit || b.Gold != gold {
			t.Fatalf("%s=%+v, want lit=%v gold=%v\n%+v", b.Label, b, lit, gold, res.Beads)
		}
		if d := image.Pt(b.X, b.Y).Sub(centres[i]); d.X < -1 || d.X > 1 || d.Y < -1 || d.Y > 1 {
			t.Fatalf("%s sampled at (%d,%d), drawn at %v", b.Label, b.X, b.Y, centres[i])
		}
	}
	if n != len(colors) {
		t.Fatalf("side %c has %d slots, want %d: %+v", prefix, n, len(colors), res.Beads)
	}
}

// rowRead reports whether every drawn slot of a side was read correctly.
func rowRead(res engine.Result, side int, colors []color.RGBA) bool {
	prefix := "LR"[side]
	n := 0
	for _, b := range res.Beads {
		if b.Label == "" || b.Label[0] != prefix {
			continue
		}
		i, _ := strconv.Atoi(b.Label[1:])
		i--
		if i < 0 || i >= len(colors) || b.Unknown || b.Lit != (colors[i] != calibDark) {
			return false
		}
		n++
	}
	return n == len(colors)
}

var (
	// 2388x1668 tablet drawing its HUD larger and lower than the width fit.
	tabletSides = [2]deviceSide{{330, 250, 1.7}, {2041, 250, 1.7}}
	// 3440x1440 ultrawide with uneven safe areas and no HP bar to anchor on.
	ultrawideSides = [2]deviceSide{{535.2, 164.8, 1.6}, {2956.8, 164.8, 1.6}}
)

// Synthetic devices whose HUD sits where the automatic layout does not look.
// Boxes drawn on the duel frame must read both duel and camp frames, beads
// and portraits.
func TestRegionCalibrationReadsAwkwardDevices(t *testing.T) {
	cfg := config.Default()
	B, D, G := calibBlue, calibDark, calibGold
	left, right := []color.RGBA{B, B, D, D}, []color.RGBA{G, G, G, G}
	out := os.Getenv("NARUTO_CALIB_OUT")
	for _, device := range []struct {
		name  string
		size  image.Point
		sides [2]deviceSide
	}{
		{"tablet-2388x1668", image.Pt(2388, 1668), tabletSides},
		{"ultrawide-3440x1440", image.Pt(3440, 1440), ultrawideSides},
	} {
		t.Run(device.name, func(t *testing.T) {
			paint := func(profile string) (*deviceHUD, [2][]image.Point, [2]detect.PixelBox) {
				h := newDeviceHUD(device.size, device.sides)
				centres := [2][]image.Point{h.beans(0, profile, 0, left), h.beans(1, profile, 0, right)}
				boxes := [2]detect.PixelBox{h.portrait(t, 0, profile, "90106"), h.portrait(t, 1, profile, "90244")}
				return h, centres, boxes
			}
			duel, duelCentres, duelBoxes := paint("duel")
			camp, campCentres, _ := paint("camp")
			full := &config.RegionCalibration{Profile: "duel",
				LeftBeads: duel.beadBox(0, "duel"), RightBeads: duel.beadBox(1, "duel"),
				LeftAvatar: duel.normalized(duelBoxes[0]), RightAvatar: duel.normalized(duelBoxes[1])}
			names := [2]string{"宇智波斑[秽土转生]", "旗木卡卡西[须佐能乎]"}
			at := time.Unix(1700000000, 0)
			// prefer binds the profile like a scene template does.
			analyzeAs := func(c *config.RegionCalibration, h *deviceHUD, prefer string) engine.Result {
				layout := cfg.Layout
				layout.Calibration = c
				e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, layout), cfg.Vision)
				e.Prefer(prefer)
				return e.AnalyzeAt(h.img, at)
			}
			analyze := func(c *config.RegionCalibration, h *deviceHUD) engine.Result { return analyzeAs(c, h, "") }
			// The automatic layout misses both rows and both portraits.
			for _, h := range []*deviceHUD{duel, camp} {
				res := analyze(nil, h)
				if rowRead(res, 0, left) || rowRead(res, 1, right) || res.LeftNinja != "" || res.RightNinja != "" {
					t.Fatalf("automatic layout unexpectedly read the device HUD: %+v", res)
				}
			}
			for _, tc := range []struct {
				name    string
				h       *deviceHUD
				profile string
				centres [2][]image.Point
			}{{"duel", duel, "duel", duelCentres}, {"camp", camp, "camp", campCentres}} {
				res := analyze(full, tc.h)
				if res.LayoutProfile != tc.profile || res.LeftSlots != 4 || res.RightSlots != 4 || res.LeftNinja != names[0] || res.RightNinja != names[1] {
					t.Fatalf("%s: profile=%s slots=%d/%d names=%q/%q", tc.name, res.LayoutProfile, res.LeftSlots, res.RightSlots, res.LeftNinja, res.RightNinja)
				}
				wantRow(t, res, 0, left, tc.centres[0])
				wantRow(t, res, 1, right, tc.centres[1])
				if out != "" {
					dir := filepath.Join(out, device.name+"-"+tc.name)
					writeEnergyPNG(t, filepath.Join(dir, "0.png"), tc.h.img)
					if err := os.WriteFile(filepath.Join(dir, "calibration.txt"), []byte(calibrationArg(full)+"\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Bead boxes alone also place the portrait windows (bead 0 anchor).
			beadsOnly := &config.RegionCalibration{Profile: "duel", LeftBeads: full.LeftBeads, RightBeads: full.RightBeads}
			res := analyze(beadsOnly, camp)
			if res.LeftNinja != names[0] || res.RightNinja != names[1] {
				t.Fatalf("bead-only calibration lost portraits: %q/%q", res.LeftNinja, res.RightNinja)
			}
			wantRow(t, res, 0, left, campCentres[0])
			wantRow(t, res, 1, right, campCentres[1])
			// Portrait boxes alone identify, but leave the beads automatic.
			avatarsOnly := &config.RegionCalibration{Profile: "duel", LeftAvatar: full.LeftAvatar, RightAvatar: full.RightAvatar}
			for profile, h := range map[string]*deviceHUD{"duel": duel, "camp": camp} {
				res := analyzeAs(avatarsOnly, h, profile)
				if res.LeftNinja != names[0] || res.RightNinja != names[1] || rowRead(res, 0, left) {
					t.Fatalf("avatar-only calibration (%s): names=%q/%q beads=%+v", profile, res.LeftNinja, res.RightNinja, res.Beads)
				}
			}
			// One calibrated side never moves the other one.
			oneSide := &config.RegionCalibration{Profile: "duel", RightBeads: full.RightBeads, RightAvatar: full.RightAvatar}
			res = analyze(oneSide, duel)
			if res.RightNinja != names[1] || res.LeftNinja != "" || rowRead(res, 0, left) {
				t.Fatalf("right-only calibration: %+v", res)
			}
			wantRow(t, res, 1, right, duelCentres[1])
		})
	}
}

// Special rows on a calibrated side keep their rules in pixel space: the
// energy-gauge drop (portrait-identified Minato) and the six-slot extension
// (title-identified Hashirama) use the calibrated scale and pitch.
func TestRegionCalibrationSpecialRows(t *testing.T) {
	cfg := config.Default()
	B, D, W := calibBlue, calibDark, calibWarm
	minato, hashirama := []color.RGBA{B, B, B, D}, []color.RGBA{W, W, W, W, W, D}
	out := os.Getenv("NARUTO_CALIB_OUT")
	for _, size := range []image.Point{{2388, 1668}, {3440, 1440}} {
		for _, profile := range []string{"camp", "duel"} {
			t.Run(fmt.Sprintf("%dx%d-%s", size.X, size.Y, profile), func(t *testing.T) {
				sides := tabletSides
				if size.X == 3440 {
					sides = ultrawideSides
				}
				h := newDeviceHUD(size, sides)
				// Left: Minato's beans 13px@540 below the ordinary row.
				lc := h.beans(0, profile, ninja.EnergyGaugeRowOffset, minato)
				h.portrait(t, 0, profile, "90316")
				rc := h.beans(1, profile, 0, hashirama)
				h.title(t, 1, profile, "hashirama")
				// Boxes drawn earlier on an ordinary duel frame of this device.
				ref := newDeviceHUD(size, sides)
				c := &config.RegionCalibration{Profile: "duel", LeftBeads: ref.beadBox(0, "duel"), RightBeads: ref.beadBox(1, "duel"),
					LeftAvatar: ref.normalized(ref.portrait(t, 0, "duel", "90316"))}
				layout := cfg.Layout
				layout.Calibration = c
				res := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, layout), cfg.Vision).AnalyzeAt(h.img, time.Unix(1700000000, 0))
				if res.LayoutProfile != profile || res.LeftNinja != ninja.MinatoKyubi || res.RightNinja != ninja.Hashirama || res.LeftSlots != 4 || res.RightSlots != 6 {
					t.Fatalf("profile=%s names=%q/%q slots=%d/%d %+v", res.LayoutProfile, res.LeftNinja, res.RightNinja, res.LeftSlots, res.RightSlots, res.Beads)
				}
				wantRow(t, res, 0, minato, lc)
				wantRow(t, res, 1, hashirama, rc)
				if out != "" {
					dir := filepath.Join(out, fmt.Sprintf("special-%dx%d-%s", size.X, size.Y, profile))
					writeEnergyPNG(t, filepath.Join(dir, "0.png"), h.img)
					if err := os.WriteFile(filepath.Join(dir, "calibration.txt"), []byte(calibrationArg(c)+"\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

// A live replacement takes effect on the next frame, even for the same
// pixels, and clearing it restores the automatic layout.
func TestRegionCalibrationLiveReplacement(t *testing.T) {
	cfg := config.Default()
	left, right := []color.RGBA{calibBlue, calibDark, calibDark, calibDark}, []color.RGBA{calibBlue, calibBlue, calibBlue, calibDark}
	h := newDeviceHUD(image.Pt(2388, 1668), tabletSides)
	lc, rc := h.beans(0, "duel", 0, left), h.beans(1, "duel", 0, right)
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	at := time.Unix(1700000000, 0)
	if res := e.AnalyzeAt(h.img, at); rowRead(res, 0, left) || rowRead(res, 1, right) || e.CalibrationRevision() != 0 {
		t.Fatalf("automatic read the device HUD: %+v", res)
	}
	e.SetCalibration(&config.RegionCalibration{Profile: "duel", LeftBeads: h.beadBox(0, "duel"), RightBeads: h.beadBox(1, "duel")})
	if e.CalibrationRevision() == 0 {
		t.Fatal("replacement did not change the revision")
	}
	res := e.AnalyzeAt(h.img, at.Add(16*time.Millisecond))
	wantRow(t, res, 0, left, lc)
	wantRow(t, res, 1, right, rc)
	revision := e.CalibrationRevision()
	e.SetCalibration(nil)
	if res := e.AnalyzeAt(h.img, at.Add(32*time.Millisecond)); rowRead(res, 0, left) || rowRead(res, 1, right) || e.CalibrationRevision() == revision {
		t.Fatalf("cleared calibration still applied: %+v", res)
	}
}
