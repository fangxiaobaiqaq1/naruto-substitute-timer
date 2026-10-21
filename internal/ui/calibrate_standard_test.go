package ui

import (
	"fmt"
	"image"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/engine/rgb"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

// 校准值是标准忍者值 (docs/区域校准.md): bead boxes are always the ordinary
// ninja's row. On a frame whose left row is lowered (a special ninja) the
// left bead boxes show on the visible beads, and what is drawn there is
// stored raised back to the standard row.

// Row drops of the special ninja in 960×540 units.
const (
	xiayinRowOffset = 9                          // 侠隐佐助
	minatoRowOffset = ninja.EnergyGaugeRowOffset // 九喇嘛水门, 百战鼬
)

// specialRowGeometry is the geometry the engine reports on a w×h duel frame
// whose left ninja's bead row is lowered by offset (960×540 units) and whose
// right ninja is ordinary: HUD scale s = min(w/1600, h/900), the standard rows
// at the duel layout's nominal bead 0 and pitch (left 77/3, right 25 at
// 1600×900), the sampled centres rounded to pixels. The boxes stay standard.
func specialRowGeometry(w, h int, offset float64) [2]frame.SideGeometry {
	W, H := float64(w), float64(h)
	s := math.Min(W/1600, H/900)
	units := s * 1600 / 960 // 960×540 units
	var g [2]frame.SideGeometry
	for side := range g {
		left := side == 0
		x0, pitch := 172*s, 77.0/3*s
		boxX := x0 - pitch/2
		if !left {
			x0, pitch = W-(1600-1423)*s, 25*s
			boxX = x0 + pitch/2 - 4*pitch
		}
		y := 103 * s
		box := config.NormalizedRect{X: boxX / W, Y: (y - pitch/2) / H, Width: 4 * pitch / W, Height: pitch / H}
		drop := 0.0
		if left {
			drop = offset * units
		}
		for _, c := range calibrationBeadCenters(box, left) {
			g[side].Beads = append(g[side].Beads, image.Pt(int(math.Round(c.X*W)), int(math.Round(c.Y*H+drop))))
		}
		// The name is searched from the standard bead 0, before the drop.
		g[side].NameROI = ninja.NameRegion(image.Pt(int(math.Round(x0)), int(math.Round(y))), units, left)
		b := 68.5 * units
		ax := boxX - 6*units - b
		if !left {
			ax = boxX + 4*pitch + 6*units
		}
		g[side].AvatarROI, _ = ninja.AvatarRegionFromBox(ax, 14*units, b, b, left)
		g[side].BeadBox = box
		g[side].AvatarBox = config.NormalizedRect{X: ax / W, Y: 14 * units / H, Width: b / W, Height: b / H}
	}
	return g
}

// specialRowFrame is a duel frame with specialRowGeometry: leftNinja lowered
// by offset, an ordinary 五代目水影 on the right.
func specialRowFrame(w, h int, leftNinja string, offset float64) frame.Frame {
	f := calibrationTestFrame(w, h, "duel")
	f.Geometry = specialRowGeometry(w, h, offset)
	f.LeftNinja, f.RightNinja = leftNinja, ninja.FifthMizukage
	return f
}

// visibleRow is where a side's sampled beads are: the mean y of the first
// four centres and the pixel offset from the standard row of its bead box.
func visibleRow(g frame.SideGeometry, h int) (meanY, drop float64) {
	for _, p := range g.Beads[:4] {
		meanY += float64(p.Y) / 4
	}
	return meanY, meanY - (g.BeadBox.Y+g.BeadBox.Height/2)*float64(h)
}

// pixelRect is r in frame pixels.
func pixelRect(r config.NormalizedRect, w, h int) [4]float64 {
	W, H := float64(w), float64(h)
	return [4]float64{r.X * W, r.Y * H, r.Width * W, r.Height * H}
}

// withinPixels: every edge of got is within tol frame pixels of want.
func withinPixels(got *config.NormalizedRect, want config.NormalizedRect, w, h int, tol float64) bool {
	if got == nil {
		return false
	}
	a, b := pixelRect(*got, w, h), pixelRect(want, w, h)
	for i := range a {
		if math.Abs(a[i]-b[i]) > tol {
			return false
		}
	}
	return true
}

type specialRowCase struct {
	name   string
	size   image.Point
	ninja  string
	offset float64
}

var specialRowCases = []specialRowCase{
	{"2400x1080-xiayin", image.Pt(2400, 1080), ninja.SasukeXiayin, xiayinRowOffset},
	{"2400x1080-minato", image.Pt(2400, 1080), ninja.MinatoKyubi, minatoRowOffset},
	{"2388x1668-minato", image.Pt(2388, 1668), ninja.MinatoKyubi, minatoRowOffset},
	{"2388x1668-xiayin", image.Pt(2388, 1668), ninja.SasukeXiayin, xiayinRowOffset},
}

// The drop is measured on the frame in bead pitches; small ones are pixel
// rounding. Pixels and boxes of an offset (cropped) frame are mapped like the
// overlay maps them.
func TestCalibrationRowDrop(t *testing.T) {
	box := config.NormalizedRect{X: 0.1, Y: 0.1, Width: 0.05, Height: 0.03} // 2000×1000: pitch 25 px, row y 115
	row := func(ys ...int) frame.SideGeometry {
		g := frame.SideGeometry{BeadBox: box}
		for i, y := range ys {
			g.Beads = append(g.Beads, image.Pt(200+25*i, y))
		}
		return g
	}
	for _, tc := range []struct {
		name string
		g    frame.SideGeometry
		want float64
	}{
		{"xiayin", row(130, 130, 130, 130), 0.6},
		{"mean of the first four", row(130, 131, 129, 130, 40, 40), 0.6},
		{"just above the threshold", row(118, 118, 117, 118), 0.11},
		{"just below the threshold", row(117, 118, 117, 117), 0},
		{"rounding", row(117, 117, 117, 117), 0},
		{"raised", row(100, 100, 100, 100), 0},
		{"three beads", row(130, 130, 130), 0},
		{"no box", frame.SideGeometry{Beads: row(130, 130, 130, 130).Beads}, 0},
		{"nothing", frame.SideGeometry{}, 0},
	} {
		if got := calibrationRowDrop(tc.g, 2000, 1000); math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("%s: drop %v, want %v", tc.name, got, tc.want)
		}
	}

	// A cropped capture: pixels in image coordinates, boxes in fractions of
	// its bounds.
	g := specialRowGeometry(2400, 1080, xiayinRowOffset)
	_, drop := visibleRow(g[0], 1080)
	origin := image.Pt(13, 9)
	parent := calibrationTestFrame(2400+40, 1080+30, "duel").Img
	sub := parent.SubImage(image.Rectangle{Min: origin, Max: origin.Add(image.Pt(2400, 1080))}).(*image.RGBA)
	moved := g
	for side := range moved {
		moved[side].Beads = nil
		for _, p := range g[side].Beads {
			moved[side].Beads = append(moved[side].Beads, p.Add(origin))
		}
	}
	snap := newCalibrationSnapshot(frame.Frame{Img: sub, Fighting: true, LayoutProfile: "duel", Geometry: moved, LeftNinja: ninja.SasukeXiayin})
	pitch := g[0].BeadBox.Width * 2400 / 4
	if math.Abs(snap.drops[0]-drop/pitch) > 1e-12 || math.Abs(snap.drops[0]-45.0/77) > 0.5/pitch || snap.drops[1] != 0 || snap.ninjas != [2]string{ninja.SasukeXiayin, ""} {
		t.Fatalf("cropped frame: drops %v ninjas %q (want %.4f, 0)", snap.drops, snap.ninjas, drop/pitch)
	}
}

// On a lowered row the automatic box (and its bead dots) sits on the visible
// beads; the avatar boxes and the ordinary side do not move. Saving it as is
// writes nothing, nor does moving it away and back; 2 px down saves the
// standard box 2 px down.
func TestCalibrationLoweredRowShowsOnVisibleBeads(t *testing.T) {
	for _, tc := range specialRowCases {
		t.Run(tc.name, func(t *testing.T) {
			w, h := tc.size.X, tc.size.Y
			s := newCalibrationTestSession(t)
			calls := 0
			s.calibrationApply = func(*config.RegionCalibration) { calls++ }
			f := specialRowFrame(w, h, tc.ninja, tc.offset)
			c := openCalibrationOn(t, s, f)
			at, scale := surfaceMapper(t, c)
			r := calibRenderer(c)
			left, right := f.Geometry[0], f.Geometry[1]
			_, drop := visibleRow(left, h)
			pitch := left.BeadBox.Width * float64(w) / 4
			// The engine drops the row by offset·s·1600/960 px: offset·(5/3)/(77/3) pitches.
			if want := tc.offset * 5 / 77; math.Abs(c.snap.drops[0]-want) > 0.5/pitch || c.snap.drops[1] != 0 {
				t.Fatalf("drops %v, want %.4f and 0", c.snap.drops, want)
			}

			// Which ninja, and that the left row is converted.
			wantSides := "左侧：" + ninja.ShortLabel(tc.ninja) + "，豆行比普通忍者低，已按标准换算。右侧：五代目水影。"
			if !c.ninjas.Visible() || c.ninjas.Text != wantSides || !strings.Contains(c.status.Text, "认出九喇嘛水门、侠隐佐助、百战鼬时会自动换算") {
				t.Fatalf("sides %q (visible %v), status %q", c.ninjas.Text, c.ninjas.Visible(), c.status.Text)
			}

			shown := c.shown()
			lowered := left.BeadBox
			lowered.Y += drop / float64(h)
			if box := shown[calibLeftBeads]; box.state != calibBoxAuto || !withinPixels(&box.rect, lowered, w, h, 1e-6) {
				t.Fatalf("left beads shown at %v, want %v (drop %.2f px)", pixelRect(box.rect, w, h), pixelRect(lowered, w, h), drop)
			}
			outline := r.boxes[calibLeftBeads]
			if !outline.Visible() || !near(outline.Position(), at(lowered.X*float64(w), lowered.Y*float64(h)), 0.01) {
				t.Fatalf("left bead outline at %v, want %v", outline.Position(), at(lowered.X*float64(w), lowered.Y*float64(h)))
			}
			for i, dot := range r.dots[calibLeftBeads] {
				centre := dot.Position().AddXY(dot.Size().Width/2, dot.Size().Height/2)
				bead := left.Beads[i]
				if !dot.Visible() || !near(centre, at(float64(bead.X), float64(bead.Y)), float32(0.5*scale)+0.01) {
					t.Fatalf("left bead dot %d at %v, visible bead at %v", i, centre, at(float64(bead.X), float64(bead.Y)))
				}
			}
			// Not shifted: the ordinary side and both avatars.
			if shown[calibRightBeads].rect != right.BeadBox || shown[calibLeftAvatar].rect != left.AvatarBox || shown[calibRightAvatar].rect != right.AvatarBox {
				t.Fatalf("unshifted boxes moved: %+v", shown)
			}

			// Untouched: nothing to save.
			c.confirmSave()
			if _, err := os.Stat(s.cfgPath); !os.IsNotExist(err) || calls != 0 || s.cfg.Layout.Calibration != nil || !strings.Contains(c.status.Text, "没有改动") {
				t.Fatalf("saved the default: %v, hook %d, status %q", err, calls, c.status.Text)
			}
			// Away and back again is no change either.
			x, y := boxCentre(lowered, w, h)
			dragSurface(c.surface, at(x, y), at(x+12, y+3))
			if !c.dirty() {
				t.Fatal("the left bead box did not move")
			}
			dragSurface(c.surface, at(x+12, y+3), at(x, y))
			if c.dirty() || c.shown()[calibLeftBeads].state != calibBoxAuto {
				t.Fatalf("moved back is still an edit: %q", c.status.Text)
			}

			// Two pixels down with the arrow keys: the standard box 2 px down.
			c.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
			c.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
			if nudged := c.shown()[calibLeftBeads].rect; !withinPixels(&nudged, shifted(lowered, 0, 2, w, h), w, h, 1e-6) {
				t.Fatalf("nudged box shows at %v", pixelRect(nudged, w, h))
			}
			c.confirmSave()
			got := s.cfg.Layout.Calibration
			want := shifted(left.BeadBox, 0, 2, w, h)
			if got == nil || got.Profile != "duel" || !withinPixels(got.LeftBeads, want, w, h, 0.5) || got.RightBeads != nil || got.LeftAvatar != nil || got.RightAvatar != nil || calls != 1 {
				t.Fatalf("saved %+v, want left beads %v px (hook %d)", got, pixelRect(want, w, h), calls)
			}
			disk, err := config.Load(s.cfgPath)
			if err != nil || !reflect.DeepEqual(disk.Layout.Calibration, got) {
				t.Fatalf("disk %+v, %v", disk.Layout.Calibration, err)
			}
			// The saved box still shows on the visible beads, 2 px down.
			if box := c.shown()[calibLeftBeads]; box.state != calibBoxSaved || !withinPixels(&box.rect, shifted(lowered, 0, 2, w, h), w, h, 0.5) {
				t.Fatalf("saved box shows at %v", pixelRect(box.rect, w, h))
			}

			// On an ordinary frame the same saved box shows at its value.
			ordinary := calibrationTestFrame(w, h, "duel")
			ordinary.Geometry = specialRowGeometry(w, h, 0)
			s.provider = func() frame.Frame { return ordinary }
			s.captureOnce()
			c.refreshFrame()
			if c.ninjas.Visible() || c.snap.drops != [2]float64{} || c.shown()[calibLeftBeads].rect != *got.LeftBeads {
				t.Fatalf("ordinary frame: sides %q (visible %v), left beads %+v", c.ninjas.Text, c.ninjas.Visible(), c.shown()[calibLeftBeads])
			}
		})
	}
}

// A box drawn tightly around the visible lowered beads is stored as the
// standard box: its bead centres (what recognition uses) within 0.5 px of
// the standard box's, its pitch too. A moved and saved box keeps its value.
func TestCalibrationBoxDrawnOnLoweredRowIsStandard(t *testing.T) {
	for _, tc := range specialRowCases {
		t.Run(tc.name, func(t *testing.T) {
			w, h := tc.size.X, tc.size.Y
			W, H := float64(w), float64(h)
			s := newCalibrationTestSession(t)
			f := specialRowFrame(w, h, tc.ninja, tc.offset)
			c := openCalibrationOn(t, s, f)
			at, _ := surfaceMapper(t, c)
			left := f.Geometry[0]
			meanY, _ := visibleRow(left, h)
			pitch := left.BeadBox.Width * W / 4

			// Move the automatic box out of the way, then draw from where its
			// bottom-right corner was (a press at the top-left one can land on
			// the avatar box, which would select and move that).
			x, y := boxCentre(c.shown()[calibLeftBeads].rect, w, h)
			dragSurface(c.surface, at(x, y), at(x, y+200))
			x0, x1 := float64(left.Beads[0].X)-pitch/2, float64(left.Beads[3].X)+pitch/2
			dragSurface(c.surface, at(x1, meanY+pitch/2), at(x0, meanY-pitch/2))
			drawn := c.drawn[calibLeftBeads]
			if drawn == nil || drawn.profile != "duel" || !strings.Contains(c.status.Text, "左侧豆已框好") {
				t.Fatalf("drawn %+v, status %q", drawn, c.status.Text)
			}
			check := func(what string, got config.NormalizedRect) {
				t.Helper()
				gotCentres, wantCentres := calibrationBeadCenters(got, true), calibrationBeadCenters(left.BeadBox, true)
				for i := range gotCentres {
					dx, dy := (gotCentres[i].X-wantCentres[i].X)*W, (gotCentres[i].Y-wantCentres[i].Y)*H
					if math.Abs(dx) > 0.5 || math.Abs(dy) > 0.5 {
						t.Fatalf("%s: bead %d off the standard by (%.2f, %.2f) px: %v vs %v", what, i, dx, dy, pixelRect(got, w, h), pixelRect(left.BeadBox, w, h))
					}
				}
				if d := (got.Width - left.BeadBox.Width) * W / 4; math.Abs(d) > 0.5 {
					t.Fatalf("%s: pitch off by %.2f px", what, d)
				}
			}
			check("drawn", drawn.rect)
			// It shows where it was drawn, on the visible beads.
			r := calibRenderer(c)
			outline := r.boxes[calibLeftBeads]
			if !near(outline.Position(), at(math.Round(x0), math.Round(meanY-pitch/2)), 0.01) {
				t.Fatalf("drawn box shows at %v, drawn at %v", outline.Position(), at(math.Round(x0), math.Round(meanY-pitch/2)))
			}

			c.confirmSave()
			got := s.cfg.Layout.Calibration
			if got == nil || got.LeftBeads == nil || *got.LeftBeads != drawn.rect || got.RightBeads != nil {
				t.Fatalf("saved %+v, drawn %+v", got, drawn.rect)
			}
			check("saved", *got.LeftBeads)

			// A saved box moved and moved back is no change.
			x, y = boxCentre(c.shown()[calibLeftBeads].rect, w, h)
			dragSurface(c.surface, at(x, y), at(x-7, y+4))
			dragSurface(c.surface, at(x-7, y+4), at(x, y))
			if c.dirty() {
				t.Fatal("a saved box moved back is an edit")
			}
		})
	}
}

// A scheme holds standard values: 导出方案 on a lowered frame writes the
// standard boxes; 导入方案 shows them lowered on a lowered frame (on the
// visible beads) and at their value on an ordinary one; 保存 stores them
// unchanged. The ordinary side is never converted.
func TestCalibrationSchemeOnLoweredRowIsStandard(t *testing.T) {
	dialogs := noFileDialogs(t)
	const w, h = 2400, 1080
	f := specialRowFrame(w, h, ninja.SasukeXiayin, xiayinRowOffset)
	left, right := f.Geometry[0], f.Geometry[1]
	a := newCalibrationTestSession(t)
	ca := openCalibrationOn(t, a, f)
	at, _ := surfaceMapper(t, ca)
	ca.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown}) // left beads
	ca.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	ca.targets.SetSelected("右侧豆")
	x, y := boxCentre(right.BeadBox, w, h)
	dragSurface(ca.surface, at(x, y), at(x+2, y))
	wantLeft, wantRight := shifted(left.BeadBox, 0, 2, w, h), shifted(right.BeadBox, 2, 0, w, h)

	data, summary, err := ca.exportData()
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := decodeCalibrationScheme(data)
	if err != nil || scheme.Calibration.Profile != "duel" || !withinPixels(scheme.Calibration.LeftBeads, wantLeft, w, h, 0.5) ||
		!withinPixels(scheme.Calibration.RightBeads, wantRight, w, h, 1e-6) || scheme.Calibration.LeftAvatar != nil || scheme.Calibration.RightAvatar != nil {
		t.Fatalf("exported (%s)\n%s\n→ %+v %v", summary, data, scheme.Calibration, err)
	}
	for _, view := range []struct {
		name  string
		frame frame.Frame
	}{
		{"lowered", specialRowFrame(w, h, ninja.SasukeXiayin, xiayinRowOffset)},
		{"ordinary", func() frame.Frame {
			o := calibrationTestFrame(w, h, "duel")
			o.Geometry = specialRowGeometry(w, h, 0)
			return o
		}()},
	} {
		t.Run(view.name, func(t *testing.T) {
			b := newCalibrationTestSession(t)
			cb := openCalibrationOn(t, b, view.frame)
			if err := cb.loadScheme(data); err != nil {
				t.Fatal(err)
			}
			wantShown := *scheme.Calibration.LeftBeads
			if view.name == "lowered" {
				_, drop := visibleRow(view.frame.Geometry[0], h)
				wantShown.Y += drop / h
			} else if cb.snap.drops != [2]float64{} {
				t.Fatalf("ordinary frame drops %v", cb.snap.drops)
			}
			shown := cb.shown()
			if shown[calibLeftBeads].state != calibBoxPending || !withinPixels(&shown[calibLeftBeads].rect, wantShown, w, h, 1e-6) ||
				shown[calibRightBeads].rect != *scheme.Calibration.RightBeads {
				t.Fatalf("imported boxes show at %v and %v, want %v", pixelRect(shown[calibLeftBeads].rect, w, h),
					pixelRect(shown[calibRightBeads].rect, w, h), pixelRect(wantShown, w, h))
			}
			if view.name == "lowered" {
				// On the visible beads, 2 px down like the exported edit.
				atB, scale := surfaceMapper(t, cb)
				for i, dot := range calibRenderer(cb).dots[calibLeftBeads] {
					centre := dot.Position().AddXY(dot.Size().Width/2, dot.Size().Height/2)
					bead := view.frame.Geometry[0].Beads[i]
					if !near(centre, atB(float64(bead.X), float64(bead.Y)+2), float32(0.5*scale)+0.01) {
						t.Fatalf("imported dot %d at %v, bead at %v", i, centre, atB(float64(bead.X), float64(bead.Y)))
					}
				}
			}
			cb.confirmSave()
			if !reflect.DeepEqual(b.cfg.Layout.Calibration, scheme.Calibration) {
				t.Fatalf("saved %+v, want the scheme %+v", b.cfg.Layout.Calibration, scheme.Calibration)
			}
		})
	}
	if len(*dialogs) != 0 {
		t.Fatalf("%d file dialogs", len(*dialogs))
	}
}

// Through the real engine (synthetic HUD with the 侠隐佐助 title, NOT a
// capture): the frame's lowered row is measured from what the engine sampled,
// a box drawn on the visible beads is saved as the ordinary row, and the
// engine analysing the same frame with that calibration lowers it again,
// onto the visible beads.
func TestCalibrationLoweredRowRoundTripsThroughEngine(t *testing.T) {
	size := image.Pt(2400, 1080)
	w, h := size.X, size.Y
	img, res := calibrationPreviewFrame(size, nil, previewXiayin)
	left := res.Geometry[0]
	if res.LeftNinja != ninja.SasukeXiayin || len(left.Beads) != 4 || res.RightNinja != "" {
		t.Fatalf("engine read %q / %q, left beads %v", res.LeftNinja, res.RightNinja, left.Beads)
	}
	s := newCalibrationTestSession(t)
	c := openCalibrationOn(t, s, frame.Frame{Img: img, Fighting: true, LayoutProfile: res.LayoutProfile, Scene: "fight",
		Geometry: res.Geometry, LeftNinja: res.LeftNinja, RightNinja: res.RightNinja})
	pitch := left.BeadBox.Width * float64(w) / 4
	if math.Abs(c.snap.drops[0]-45.0/77) > 0.5/pitch || c.snap.drops[1] != 0 || c.ninjas.Text != "左侧：佐助·侠隐江湖，豆行比普通忍者低，已按标准换算。" {
		t.Fatalf("drops %v, sides %q", c.snap.drops, c.ninjas.Text)
	}
	at, scale := surfaceMapper(t, c)
	for i, dot := range calibRenderer(c).dots[calibLeftBeads] {
		// On the sampled row; across, the layout's nominal centres are not
		// evenly spaced (26, 25, 26 units) like the dots: within 1 px.
		centre := dot.Position().AddXY(dot.Size().Width/2, dot.Size().Height/2)
		bead := at(float64(left.Beads[i].X), float64(left.Beads[i].Y))
		if math.Abs(float64(centre.Y-bead.Y)) > 0.01 || math.Abs(float64(centre.X-bead.X)) > scale+0.01 {
			t.Fatalf("automatic dot %d at %v, sampled bead at %v", i, centre, bead)
		}
	}

	meanY, _ := visibleRow(left, h)
	x, y := boxCentre(c.shown()[calibLeftBeads].rect, w, h)
	dragSurface(c.surface, at(x, y), at(x, y+200))
	x0, x1 := float64(left.Beads[0].X)-pitch/2, float64(left.Beads[3].X)+pitch/2
	dragSurface(c.surface, at(x1, meanY+pitch/2), at(x0, meanY-pitch/2))
	drawn := c.shown()[calibLeftBeads]
	if c.target != calibLeftBeads || drawn.state != calibBoxPending || c.touched[calibLeftAvatar] ||
		!withinPixels(&drawn.rect, config.NormalizedRect{X: math.Round(x0) / float64(w), Y: math.Round(meanY-pitch/2) / float64(h),
			Width: (math.Round(x1) - math.Round(x0)) / float64(w), Height: (math.Round(meanY+pitch/2) - math.Round(meanY-pitch/2)) / float64(h)}, w, h, 1e-3) {
		t.Fatalf("drawn %v (target %v, touched %v)", pixelRect(drawn.rect, w, h), c.target, c.touched)
	}
	c.confirmSave()
	saved := s.cfg.Layout.Calibration
	if saved == nil || saved.LeftBeads == nil || saved.RightBeads != nil {
		t.Fatalf("saved %+v", saved)
	}

	layout := config.Default().Layout
	layout.Calibration = saved
	eng := rgb.NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, layout), config.Default().Vision)
	eng.Prefer("duel")
	again := eng.AnalyzeAt(img, time.Unix(1700000000, 0))
	if again.LeftNinja != ninja.SasukeXiayin || len(again.Geometry[0].Beads) != 4 {
		t.Fatalf("calibrated analysis read %q, beads %v", again.LeftNinja, again.Geometry[0].Beads)
	}
	for i, p := range again.Geometry[0].Beads {
		if d := p.Sub(left.Beads[i]); d.X < -1 || d.X > 1 || d.Y < -1 || d.Y > 1 {
			t.Fatalf("calibrated bead %d sampled at %v, visible at %v", i, p, left.Beads[i])
		}
	}
	if again.Geometry[1].Beads[0] != res.Geometry[1].Beads[0] {
		t.Fatalf("the automatic right side moved: %v vs %v", again.Geometry[1].Beads, res.Geometry[1].Beads)
	}
}

// After 保存 the next frame replaces the shown one, but not in the middle of
// a drag: the dragged box is in the shown frame's pixels and row drop. The
// frame after the drag does.
func TestCalibrationFreshFrameWaitsForDrag(t *testing.T) {
	copyEveryFrame(t)
	const w, h = 2400, 1080
	s := newCalibrationTestSession(t)
	s.calibrationApply = func(*config.RegionCalibration) {}
	c := openCalibrationOn(t, s, specialRowFrame(w, h, ninja.SasukeXiayin, xiayinRowOffset))
	at, _ := surfaceMapper(t, c)
	c.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	c.confirmSave()
	lowered := c.snap
	if c.fresh() || lowered.drops[0] == 0 {
		t.Fatalf("after save: fresh %v drops %v", c.fresh(), lowered.drops)
	}

	x, y := boxCentre(c.shown()[calibLeftBeads].rect, w, h)
	press := at(x, y)
	c.surface.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: press}})
	c.surface.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: press.AddXY(0, 6)}, Dragged: fyne.NewDelta(0, 6)})
	ordinary := calibrationTestFrame(w, h, "duel")
	ordinary.Geometry = specialRowGeometry(w, h, 0)
	s.provider = func() frame.Frame { return ordinary }
	s.captureOnce()
	if c.snap != lowered {
		t.Fatal("a new frame replaced the one a box is being dragged on")
	}
	// Back where it was: no edit, and the next frame is shown.
	c.surface.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: press}, Dragged: fyne.NewDelta(0, -6)})
	c.surface.DragEnd()
	if c.dirty() {
		t.Fatal("moved back is an edit")
	}
	s.captureOnce()
	if c.snap == lowered || !c.fresh() || c.snap.drops != [2]float64{} || !strings.Contains(c.status.Text, "画面已刷新") ||
		c.shown()[calibLeftBeads].rect != *s.cfg.Layout.Calibration.LeftBeads {
		t.Fatalf("after the drag: replaced %v fresh %v, status %q", c.snap != lowered, c.fresh(), c.status.Text)
	}
}

// A lowered row the engine re-read without a name (energy gauge) is still
// converted; a frame without a ninja or a drop says nothing.
func TestCalibrationSidesText(t *testing.T) {
	copyEveryFrame(t)
	s := newCalibrationTestSession(t)
	f := specialRowFrame(2400, 1080, "", minatoRowOffset)
	f.RightNinja = ""
	c := openCalibrationOn(t, s, f)
	if !c.ninjas.Visible() || c.ninjas.Text != "左侧：豆行比普通忍者低，已按标准换算。" {
		t.Fatalf("unnamed drop: %q (visible %v)", c.ninjas.Text, c.ninjas.Visible())
	}
	for name, g := range map[string][2]frame.SideGeometry{"ordinary": specialRowGeometry(2400, 1080, 0), "no geometry": {}} {
		o := calibrationTestFrame(2400, 1080, "duel")
		o.Geometry = g
		s.provider = func() frame.Frame { return o }
		s.captureOnce()
		c.refreshFrame()
		if c.ninjas.Visible() || c.sidesText() != "" {
			t.Fatalf("%s: %q", name, c.sidesText())
		}
	}
	o := specialRowFrame(2400, 1080, ninja.Hashirama, 0)
	o.RightNinja = ninja.MinatoKyubi // named, but this frame shows its row at the standard
	s.provider = func() frame.Frame { return o }
	s.captureOnce()
	c.refreshFrame()
	if want := fmt.Sprintf("左侧：%s。右侧：%s。", ninja.ShortLabel(ninja.Hashirama), ninja.ShortLabel(ninja.MinatoKyubi)); c.ninjas.Text != want || !c.ninjas.Visible() {
		t.Fatalf("named sides: %q, want %q", c.ninjas.Text, want)
	}
}
