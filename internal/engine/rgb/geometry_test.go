package rgb

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

// autoDevice is a synthetic HUD (NOT a native capture) drawn where the
// automatic layout looks. Wide frames get health bars that move each row
// inward by the HP-bar x-correction.
func autoDevice(size image.Point) *deviceHUD {
	probe := newDeviceHUD(size, [2]deviceSide{})
	layout := engine.NewConfiguredLayout(detect.ModeAuto, config.Default().Layout)
	area, _ := layout.ContentArea(probe.img)
	s := area.HUDScale(detect.HUDReferenceWidth, detect.HUDReferenceHeight)
	shift := 0
	if float64(area.W)/float64(area.H) >= 1.9 {
		shift = int(math.Round(14 * s))
	}
	duel := config.Default().Layout.Profile("duel")
	l0, r0 := duel.Left.NominalCenters[0], duel.Right.NominalCenters[0]
	h := newDeviceHUD(size, [2]deviceSide{
		{float64(area.X) + l0.X*detect.HUDReferenceWidth*s + float64(shift), float64(area.Y) + l0.Y*detect.HUDReferenceHeight*s, s},
		{float64(area.X+area.W) - (detect.HUDReferenceWidth-r0.X*detect.HUDReferenceWidth)*s - float64(shift), float64(area.Y) + r0.Y*detect.HUDReferenceHeight*s, s},
	})
	if shift == 0 {
		return h
	}
	// Bars (3 sloping scanlines) whose outer end is the corrected bead 0.
	step := max(1, int(math.Round(s)))
	for _, profile := range []string{"camp", "duel"} {
		y := 74.0
		if profile == "camp" {
			y = 70
		}
		cy := area.Y + int(math.Round(y*s))
		for _, first := range layout.PositionsIn(profile, area) {
			if first.Idx != 0 {
				continue
			}
			direction := -1
			if first.Side == "right" {
				direction = 1
			}
			for row := -1; row <= 1; row++ {
				x := first.X - direction*shift + direction*row*step
				x0, x1 := x, x+int(200*s)
				if direction == 1 {
					x0, x1 = x-int(200*s)+1, x+1
				}
				draw.Draw(h.img, image.Rect(x0, cy+row*step, x1, cy+row*step+1), image.NewUniform(color.RGBA{240, 70, 10, 255}), image.Point{}, draw.Src)
			}
		}
	}
	return h
}

// defaultBoxes is the calibration saving every default box of res unchanged.
func defaultBoxes(res engine.Result, beads, avatars bool) *config.RegionCalibration {
	g := res.Geometry
	c := &config.RegionCalibration{Profile: res.LayoutProfile}
	box := func(r config.NormalizedRect) *config.NormalizedRect { return &r }
	if beads {
		c.LeftBeads, c.RightBeads = box(g[0].BeadBox), box(g[1].BeadBox)
	}
	if avatars {
		c.LeftAvatar, c.RightAvatar = box(g[0].AvatarBox), box(g[1].AvatarBox)
	}
	return c
}

// float32Boxes is c as Android stores it.
func float32Boxes(c *config.RegionCalibration) *config.RegionCalibration {
	f := func(r *config.NormalizedRect) *config.NormalizedRect {
		if r == nil {
			return nil
		}
		return &config.NormalizedRect{X: float64(float32(r.X)), Y: float64(float32(r.Y)), Width: float64(float32(r.Width)), Height: float64(float32(r.Height))}
	}
	return &config.RegionCalibration{Profile: c.Profile, LeftBeads: f(c.LeftBeads), RightBeads: f(c.RightBeads), LeftAvatar: f(c.LeftAvatar), RightAvatar: f(c.RightAvatar)}
}

// sameAnalysis requires identical beads, identity and geometry; boxes only
// up to floating-point rounding.
func sameAnalysis(t *testing.T, name string, got, want engine.Result) {
	t.Helper()
	if !slices.Equal(got.Beads, want.Beads) || got.LayoutProfile != want.LayoutProfile || got.LeftNinja != want.LeftNinja || got.RightNinja != want.RightNinja ||
		got.LeftSlots != want.LeftSlots || got.RightSlots != want.RightSlots {
		t.Fatalf("%s: analysis changed\n got %s %q/%q %d/%d %+v\nwant %s %q/%q %d/%d %+v", name, got.LayoutProfile, got.LeftNinja, got.RightNinja, got.LeftSlots, got.RightSlots, got.Beads,
			want.LayoutProfile, want.LeftNinja, want.RightNinja, want.LeftSlots, want.RightSlots, want.Beads)
	}
	for i, g := range got.Geometry {
		w := want.Geometry[i]
		near := func(a, b config.NormalizedRect) bool {
			return math.Abs(a.X-b.X) < 1e-6 && math.Abs(a.Y-b.Y) < 1e-6 && math.Abs(a.Width-b.Width) < 1e-6 && math.Abs(a.Height-b.Height) < 1e-6
		}
		if !slices.Equal(g.Beads, w.Beads) || g.NameROI != w.NameROI || g.AvatarROI != w.AvatarROI || !near(g.BeadBox, w.BeadBox) || !near(g.AvatarBox, w.AvatarBox) {
			t.Fatalf("%s side %d: geometry %+v, want %+v", name, i, g, w)
		}
	}
}

// checkGeometry ties a result's geometry to what the engine used: the
// sampled centres, the regions of the layout row's bead 0 and boxes that
// convert back to the row and the portrait window.
func checkGeometry(t *testing.T, name string, res engine.Result, img *image.RGBA, layout *engine.ConfiguredLayout, calibrated [2]bool) {
	t.Helper()
	area, _ := layout.ContentArea(img)
	rows := [2][]detect.BeadPosition{}
	for _, p := range layout.PositionsInImage(res.LayoutProfile, img, area) {
		rows[sideIndexOf(p.Side)] = append(rows[sideIndexOf(p.Side)], p)
	}
	b := img.Bounds()
	for i, g := range res.Geometry {
		var centres []image.Point
		for _, bead := range res.Beads {
			if bead.Label[0] == "LR"[i] {
				centres = append(centres, image.Pt(bead.X, bead.Y))
			}
		}
		first := rows[i][0]
		scale := detect.RowHUDScale(rows[i], area, 960, 540)
		wantBox, ok := layout.DefaultBeadBox(res.LayoutProfile, rows[i], b, area)
		if !slices.Equal(g.Beads, centres) || g.NameROI != ninja.NameRegion(image.Pt(first.X, first.Y), scale, i == 0) || !ok || g.BeadBox != wantBox {
			t.Fatalf("%s side %d: geometry %+v, beads %v", name, i, g, centres)
		}
		if !calibrated[i] && g.AvatarROI != ninja.AvatarRegion(image.Pt(first.X, first.Y), scale, i == 0) {
			t.Fatalf("%s side %d: avatar window %v", name, i, g.AvatarROI)
		}
		a := g.AvatarBox
		w, h := float64(b.Dx()), float64(b.Dy())
		if roi, _ := ninja.AvatarRegionFromBox(float64(b.Min.X)+a.X*w, float64(b.Min.Y)+a.Y*h, a.Width*w, a.Height*h, i == 0); roi != g.AvatarROI {
			t.Fatalf("%s side %d: avatar box %+v gives %v, want %v", name, i, a, roi, g.AvatarROI)
		}
	}
}

func sideIndexOf(side string) int {
	if side == "right" {
		return 1
	}
	return 0
}

// docs/区域校准.md 每帧几何 end to end: every frame reports the geometry it used,
// and its default boxes saved unchanged (all, beads only, portraits only,
// float32 like Android) analyse the frame exactly like the automatic layout,
// including the HP-bar x-correction, Minato's row drop and Hashirama's slots
// 5/6; with an existing calibration likewise. NARUTO_GEOMETRY_OUT exports
// the frames and their default calibration for gocheck/hostcheck.
func TestFrameGeometryDefaultBoxesReproduceAnalysis(t *testing.T) {
	cfg := config.Default()
	B, D, G, W := calibBlue, calibDark, calibGold, calibWarm
	at := time.Unix(1700000000, 0)
	analyze := func(c *config.RegionCalibration, img *image.RGBA) (engine.Result, *engine.ConfiguredLayout) {
		layout := cfg.Layout
		layout.Calibration = c
		l := engine.NewConfiguredLayout(detect.ModeAuto, layout)
		return NewConfigured(l, cfg.Vision).AnalyzeAt(img, at), l
	}
	out := os.Getenv("NARUTO_GEOMETRY_OUT")
	type paint func(h *deviceHUD, profile string) [2][]image.Point
	variants := []struct {
		name   string
		paint  paint
		names  [2]string
		slots  [2]int
		colors [2][]color.RGBA
	}{
		{"ordinary", func(h *deviceHUD, profile string) [2][]image.Point {
			c := [2][]image.Point{h.beans(0, profile, 0, []color.RGBA{B, B, D, D}), h.beans(1, profile, 0, []color.RGBA{G, G, G, G})}
			h.portrait(t, 0, profile, "90106")
			h.portrait(t, 1, profile, "90244")
			return c
		}, [2]string{"宇智波斑[秽土转生]", "旗木卡卡西[须佐能乎]"}, [2]int{4, 4}, [2][]color.RGBA{{B, B, D, D}, {G, G, G, G}}},
		{"special", func(h *deviceHUD, profile string) [2][]image.Point {
			c := [2][]image.Point{h.beans(0, profile, ninja.EnergyGaugeRowOffset, []color.RGBA{B, B, B, D}), h.beans(1, profile, 0, []color.RGBA{W, W, W, W, W, D})}
			h.portrait(t, 0, profile, "90316")
			h.title(t, 1, profile, "hashirama")
			return c
		}, [2]string{ninja.MinatoKyubi, ninja.Hashirama}, [2]int{4, 6}, [2][]color.RGBA{{B, B, B, D}, {W, W, W, W, W, D}}},
	}
	for _, size := range []image.Point{{1280, 720}, {1920, 1080}, {2400, 1080}, {2388, 1668}} {
		for _, profile := range []string{"camp", "duel"} {
			for _, v := range variants {
				name := fmt.Sprintf("%dx%d-%s-%s", size.X, size.Y, profile, v.name)
				t.Run(name, func(t *testing.T) {
					h := autoDevice(size)
					centres := v.paint(h, profile)
					auto, layout := analyze(nil, h.img)
					if auto.LayoutProfile != profile || [2]string{auto.LeftNinja, auto.RightNinja} != v.names || auto.LeftSlots != v.slots[0] || auto.RightSlots != v.slots[1] {
						t.Fatalf("automatic: profile=%s names=%q/%q slots=%d/%d %+v", auto.LayoutProfile, auto.LeftNinja, auto.RightNinja, auto.LeftSlots, auto.RightSlots, auto.Beads)
					}
					wantRow(t, auto, 0, v.colors[0], centres[0])
					wantRow(t, auto, 1, v.colors[1], centres[1])
					checkGeometry(t, "automatic", auto, h.img, layout, [2]bool{})
					full := defaultBoxes(auto, true, true)
					for _, tc := range []struct {
						name string
						c    *config.RegionCalibration
					}{{"all", full}, {"beads", defaultBoxes(auto, true, false)}, {"portraits", defaultBoxes(auto, false, true)}, {"float32", float32Boxes(full)}} {
						got, _ := analyze(tc.c, h.img)
						sameAnalysis(t, tc.name, got, auto)
					}
					if out != "" {
						dir := filepath.Join(out, name)
						writeEnergyPNG(t, filepath.Join(dir, "0.png"), h.img)
						if err := os.WriteFile(filepath.Join(dir, "calibration.txt"), []byte(calibrationArg(full)+"\n"), 0o644); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
	// An existing calibration (a tablet HUD the automatic layout misses):
	// its frames' default boxes reproduce the calibrated analysis.
	for _, profile := range []string{"duel", "camp"} {
		ref := newDeviceHUD(image.Pt(2388, 1668), tabletSides)
		existing := &config.RegionCalibration{Profile: "duel", LeftBeads: ref.beadBox(0, "duel"), RightBeads: ref.beadBox(1, "duel"),
			LeftAvatar: ref.normalized(ref.portrait(t, 0, "duel", "90106")), RightAvatar: ref.normalized(ref.portrait(t, 1, "duel", "90244"))}
		h := newDeviceHUD(image.Pt(2388, 1668), tabletSides)
		variants[0].paint(h, profile)
		calibrated, layout := analyze(existing, h.img)
		if calibrated.LayoutProfile != profile || calibrated.LeftNinja != variants[0].names[0] || calibrated.RightNinja != variants[0].names[1] {
			t.Fatalf("existing %s: %+v", profile, calibrated)
		}
		checkGeometry(t, "existing "+profile, calibrated, h.img, layout, [2]bool{true, true})
		for _, c := range []*config.RegionCalibration{defaultBoxes(calibrated, true, true), float32Boxes(defaultBoxes(calibrated, true, true))} {
			got, _ := analyze(c, h.img)
			sameAnalysis(t, "existing "+profile, got, calibrated)
		}
	}
}
