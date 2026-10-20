package ui

import (
	"bytes"
	"errors"
	"image"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	fynetest "fyne.io/fyne/v2/test"

	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

// testDefaultBoxes are default boxes like the engine reports on a 1920×1080
// frame (fractions of any frame size), with sub-pixel parts.
var testDefaultBoxes = [calibTargetCount]config.NormalizedRect{
	calibLeftBeads:   {X: 171.3 / 1920, Y: 106.2 / 1080, Width: 120.0 / 1920, Height: 30.0 / 1080},
	calibRightBeads:  {X: 1627.5 / 1920, Y: 106.4 / 1080, Width: 121.6 / 1920, Height: 30.4 / 1080},
	calibLeftAvatar:  {X: 41.5 / 1920, Y: 27.5 / 1080, Width: 137.0 / 1920, Height: 137.0 / 1080},
	calibRightAvatar: {X: 1741.5 / 1920, Y: 27.5 / 1080, Width: 137.0 / 1920, Height: 137.0 / 1080},
}

// testGeometry is the per-frame geometry of testDefaultBoxes on a w×h frame:
// the bead centres the bead boxes imply, the name region of bead 0 and the
// avatar window of the avatar box (docs/区域校准.md).
func testGeometry(w, h int) [2]frame.SideGeometry {
	var g [2]frame.SideGeometry
	W, H := float64(w), float64(h)
	for side := range g {
		left := side == 0
		beads, avatar := testDefaultBoxes[calibLeftBeads], testDefaultBoxes[calibLeftAvatar]
		if !left {
			beads, avatar = testDefaultBoxes[calibRightBeads], testDefaultBoxes[calibRightAvatar]
		}
		for _, c := range calibrationBeadCenters(beads, left) {
			g[side].Beads = append(g[side].Beads, image.Pt(int(math.Round(c.X*W)), int(math.Round(c.Y*H))))
		}
		scale := beads.Width * W / 4 / 15.4 // 960-wide units: a bead pitch is about 15.4
		g[side].NameROI = ninja.NameRegion(g[side].Beads[0], scale, left)
		g[side].AvatarROI, _ = ninja.AvatarRegionFromBox(avatar.X*W, avatar.Y*H, avatar.Width*W, avatar.Height*H, left)
		g[side].BeadBox, g[side].AvatarBox = beads, avatar
	}
	return g
}

// geometryFrame is a captured frame the engine analysed (see testGeometry).
func geometryFrame(w, h int, profile string) frame.Frame {
	f := calibrationTestFrame(w, h, profile)
	f.Geometry = testGeometry(w, h)
	return f
}

func calibRenderer(c *calibrationWindow) *calibrationRenderer {
	return fynetest.WidgetRenderer(c.surface).(*calibrationRenderer)
}

// boxCentre is the centre of a box in frame pixels.
func boxCentre(r config.NormalizedRect, w, h int) (float64, float64) {
	return (r.X + r.Width/2) * float64(w), (r.Y + r.Height/2) * float64(h)
}

func shifted(r config.NormalizedRect, dx, dy float64, w, h int) config.NormalizedRect {
	r.X += dx / float64(w)
	r.Y += dy / float64(h)
	return r
}

// noFileDialogs records import/export dialogs instead of showing them.
func noFileDialogs(t *testing.T) *[]*dialog.FileDialog {
	t.Helper()
	var shown []*dialog.FileDialog
	previous := showCalibrationFileDialog
	showCalibrationFileDialog = func(d *dialog.FileDialog) { shown = append(shown, d) }
	t.Cleanup(func() { showCalibrationFileDialog = previous })
	return &shown
}

func copyEveryFrame(t *testing.T) {
	t.Helper()
	interval := calibrationCopyInterval
	calibrationCopyInterval = 0
	t.Cleanup(func() { calibrationCopyInterval = interval })
}

// The window draws what the engine used on the frame: each side's name
// region and avatar window (clipped to the frame) and a cross on every
// sampled bead centre, named, off each other, and only while 显示自动识别区域
// is on. A frame without an analysis says so.
func TestCalibrationShowsAutomaticRegions(t *testing.T) {
	copyEveryFrame(t)
	s := newCalibrationTestSession(t)
	f := geometryFrame(1920, 1080, "duel")
	c := openCalibrationOn(t, s, f)
	at, _ := surfaceMapper(t, c)
	r := calibRenderer(c)
	if !c.showAuto.Checked || !c.autoShown() || !strings.Contains(c.status.Text, "白色虚线是这一帧自动识别实际用的区域") {
		t.Fatalf("automatic regions not announced: %q", c.status.Text)
	}
	frameRect := image.Rect(0, 0, 1920, 1080)
	for side, g := range f.Geometry {
		for k, roi := range [2]image.Rectangle{g.NameROI, g.AvatarROI} {
			clip := roi.Intersect(frameRect)
			if clip == roi {
				t.Fatalf("side %d region %d is inside the frame: nothing tests the clipping", side, k)
			}
			shade := r.autoShade[side][k]
			from, to := at(float64(clip.Min.X), float64(clip.Min.Y)), at(float64(clip.Max.X), float64(clip.Max.Y))
			if !shade.Visible() || !near(shade.Position(), from, 0.01) || !near(shade.Position().Add(shade.Size()), to, 0.01) {
				t.Fatalf("side %d region %d drawn at %v+%v, want %v-%v", side, k, shade.Position(), shade.Size(), from, to)
			}
			visible := 0
			for _, l := range r.autoDash[side][k] {
				if !l.Visible() {
					continue
				}
				visible++
				for _, p := range []fyne.Position{l.Position1, l.Position2} {
					if p.X < from.X-0.01 || p.X > to.X+0.01 || p.Y < from.Y-0.01 || p.Y > to.Y+0.01 || (p.X > from.X+0.01 && p.X < to.X-0.01 && p.Y > from.Y+0.01 && p.Y < to.Y-0.01) {
						t.Fatalf("side %d region %d: dash at %v is off the outline %v-%v", side, k, p, from, to)
					}
				}
			}
			if visible < 8 {
				t.Fatalf("side %d region %d has %d dashes", side, k, visible)
			}
			if tag := r.autoTags[side][k]; !tag.text.Visible() || tag.text.Text != calibAutoNames[k] {
				t.Fatalf("side %d region %d tag %q visible=%v", side, k, tag.text.Text, tag.text.Visible())
			}
		}
		if len(r.crosses[side]) != 4*len(g.Beads) {
			t.Fatalf("side %d: %d cross lines for %d beads", side, len(r.crosses[side]), len(g.Beads))
		}
		for i, p := range g.Beads {
			for _, l := range r.crosses[side][4*i : 4*i+4] {
				mid := fyne.NewPos((l.Position1.X+l.Position2.X)/2, (l.Position1.Y+l.Position2.Y)/2)
				if !l.Visible() || !near(mid, at(float64(p.X), float64(p.Y)), 0.01) {
					t.Fatalf("side %d bead %d: cross at %v, bead at %v", side, i, mid, at(float64(p.X), float64(p.Y)))
				}
			}
		}
	}
	// Every name is readable: no tag covers another.
	var tags []*calibTag
	for _, tag := range r.tags {
		tags = append(tags, tag)
	}
	for side := range r.autoTags {
		tags = append(tags, r.autoTags[side][:]...)
	}
	for i, a := range tags {
		if !a.bg.Visible() {
			t.Fatalf("tag %q hidden", a.text.Text)
		}
		for _, b := range tags[i+1:] {
			if calibAreaOf(a.bg.Position(), a.bg.Size()).overlap(calibAreaOf(b.bg.Position(), b.bg.Size())) > 0 {
				t.Fatalf("tags %q at %v and %q at %v overlap", a.text.Text, a.bg.Position(), b.text.Text, b.bg.Position())
			}
		}
	}

	autoVisible := func() bool {
		for side := range r.autoShade {
			for k := range r.autoShade[side] {
				if r.autoShade[side][k].Visible() || r.autoTags[side][k].text.Visible() {
					return true
				}
				for _, l := range r.autoDash[side][k] {
					if l.Visible() {
						return true
					}
				}
			}
			for _, l := range r.crosses[side] {
				if l.Visible() {
					return true
				}
			}
		}
		return false
	}
	c.showAuto.SetChecked(false)
	if autoVisible() || !r.boxes[calibLeftBeads].Visible() {
		t.Fatal("unchecking 显示自动识别区域 must hide the regions, not the editable boxes")
	}
	c.showAuto.SetChecked(true)
	if !autoVisible() {
		t.Fatal("regions did not come back")
	}

	s.provider = func() frame.Frame { return calibrationTestFrame(1920, 1080, "duel") }
	s.captureOnce()
	c.refreshFrame()
	if autoVisible() || c.autoShown() || !strings.Contains(c.status.Text, "这一帧没有自动识别区域") {
		t.Fatalf("frame without geometry: status %q", c.status.Text)
	}
	for tg, box := range c.shown() {
		if box.state != calibBoxNone || r.boxes[tg].Visible() {
			t.Fatalf("%s shows %+v without an analysis", calibTargetNames[tg], box)
		}
	}
}

// Without a calibration the editable boxes start as the frame's default
// boxes (automatic); saved boxes show as saved. None of this is an edit.
func TestCalibrationDefaultBoxesComeFromGeometry(t *testing.T) {
	s := newCalibrationTestSession(t)
	saved := config.NormalizedRect{X: 0.88, Y: 0.03, Width: 0.07, Height: 0.12}
	s.cfg.Layout.Calibration = &config.RegionCalibration{Profile: "duel", RightAvatar: &saved}
	c := openCalibrationOn(t, s, geometryFrame(1920, 1080, "duel"))
	at, _ := surfaceMapper(t, c)
	r := calibRenderer(c)
	want := testDefaultBoxes
	want[calibRightAvatar] = saved
	states := [calibTargetCount]calibBoxState{calibBoxAuto, calibBoxAuto, calibBoxAuto, calibBoxSaved}
	for tg, box := range c.shown() {
		if box.state != states[tg] || box.rect != want[tg] {
			t.Fatalf("%s: %+v, want %+v (%v)", calibTargetNames[tg], box, want[tg], states[tg])
		}
		outline := r.boxes[tg]
		if !outline.Visible() || !near(outline.Position(), at(want[tg].X*1920, want[tg].Y*1080), 0.01) {
			t.Fatalf("%s outline at %v", calibTargetNames[tg], outline.Position())
		}
		if text := r.tags[tg].text.Text; text != calibTargetNames[tg]+calibBoxSuffix[states[tg]] {
			t.Fatalf("%s tag %q", calibTargetNames[tg], text)
		}
	}
	if r.tags[calibLeftBeads].text.Text != "左侧豆（自动）" {
		t.Fatal(r.tags[calibLeftBeads].text.Text)
	}
	for i, dot := range r.dots[calibLeftBeads] {
		centre := dot.Position().AddXY(dot.Size().Width/2, dot.Size().Height/2)
		want := calibrationBeadCenters(testDefaultBoxes[calibLeftBeads], true)[i]
		if !dot.Visible() || !near(centre, at(want.X*1920, want.Y*1080), 0.01) {
			t.Fatalf("default bead box dot %d at %v", i, centre)
		}
	}
	if c.dirty() || !reflect.DeepEqual(c.boxes(), calibrationRects(s.cfg.Layout.Calibration)) {
		t.Fatalf("default boxes count as edits: %+v", c.boxes())
	}
}

// Saving without a change writes nothing and does not call the engine hook
// (decision: an unchanged save is no save at all), with or without a saved
// calibration. Moving a box away and back again is no change either.
func TestCalibrationSaveWithoutChangesWritesNothing(t *testing.T) {
	leftBeads := config.NormalizedRect{X: 0.081, Y: 0.104, Width: 0.052, Height: 0.022}
	for _, saved := range []*config.RegionCalibration{nil, {Profile: "duel", LeftBeads: &leftBeads}} {
		s := newCalibrationTestSession(t)
		var before []byte
		if saved != nil {
			s.cfg.Layout.Calibration = cloneCalibration(saved)
			if err := s.saveSettingsLocked(); err != nil {
				t.Fatal(err)
			}
			before, _ = os.ReadFile(s.cfgPath)
		}
		calls := 0
		s.calibrationApply = func(*config.RegionCalibration) { calls++ }
		c := openCalibrationOn(t, s, geometryFrame(1920, 1080, "duel"))
		at, _ := surfaceMapper(t, c)
		for _, tg := range []calibTarget{calibRightBeads, calibLeftBeads} {
			c.targets.SetSelected(calibTargetNames[tg])
			x, y := boxCentre(c.shown()[tg].rect, 1920, 1080)
			dragSurface(c.surface, at(x, y), at(x+12, y+3))
			if !c.dirty() || c.shown()[tg].state != calibBoxPending {
				t.Fatalf("%s did not move", calibTargetNames[tg])
			}
			dragSurface(c.surface, at(x+12, y+3), at(x, y))
			if c.dirty() || c.shown()[tg].state == calibBoxPending || !strings.Contains(c.status.Text, "不算改动") {
				t.Fatalf("%s moved back is still an edit: %q", calibTargetNames[tg], c.status.Text)
			}
		}
		c.confirmSave()
		after, err := os.ReadFile(s.cfgPath)
		if saved == nil && !os.IsNotExist(err) || saved != nil && (err != nil || !bytes.Equal(after, before)) {
			t.Fatalf("config written (saved=%v): %v", saved != nil, err)
		}
		if calls != 0 || !reflect.DeepEqual(s.cfg.Layout.Calibration, saved) || !strings.Contains(c.status.Text, "没有改动") {
			t.Fatalf("hook %d, calibration %+v, status %q", calls, s.cfg.Layout.Calibration, c.status.Text)
		}
	}
}

// Moving one default box saves that box only, by exactly the whole pixels
// it was dragged (its sub-pixel placement kept); the others stay automatic.
func TestCalibrationMovingDefaultBoxSavesOnlyThatBox(t *testing.T) {
	s := newCalibrationTestSession(t)
	var applied []*config.RegionCalibration
	s.calibrationApply = func(c *config.RegionCalibration) { applied = append(applied, c) }
	c := openCalibrationOn(t, s, geometryFrame(1920, 1080, "duel"))
	at, _ := surfaceMapper(t, c)
	box := testDefaultBoxes[calibLeftBeads]
	x, y := boxCentre(box, 1920, 1080)
	dragSurface(c.surface, at(x, y), at(x+10, y-4))
	moved := c.drawn[calibLeftBeads]
	if moved == nil || moved.profile != "duel" || c.touched != [calibTargetCount]bool{true, false, false, false} ||
		math.Abs((moved.rect.X-box.X)*1920-10) > 1e-9 || math.Abs((moved.rect.Y-box.Y)*1080+4) > 1e-9 || moved.rect.Width != box.Width || moved.rect.Height != box.Height {
		t.Fatalf("moved %+v from %+v (touched %v)", moved, box, c.touched)
	}
	if text := calibRenderer(c).tags[calibLeftBeads].text.Text; text != "左侧豆（未保存）" || !strings.Contains(c.status.Text, "左侧豆已移动") {
		t.Fatalf("tag %q status %q", text, c.status.Text)
	}
	c.confirmSave()
	got := s.cfg.Layout.Calibration
	if got == nil || got.Profile != "duel" || got.LeftBeads == nil || *got.LeftBeads != moved.rect || got.RightBeads != nil || got.LeftAvatar != nil || got.RightAvatar != nil {
		t.Fatalf("saved %+v", got)
	}
	disk, err := config.Load(s.cfgPath)
	if err != nil || !reflect.DeepEqual(disk.Layout.Calibration, got) {
		t.Fatalf("disk %+v, %v", disk.Layout.Calibration, err)
	}
	if len(applied) != 1 || !reflect.DeepEqual(applied[0], got) {
		t.Fatalf("hook got %+v", applied)
	}
}

// The selected box: edges and corners resize, the inside moves, a thin bead
// box moves from near its edges too, arrow keys nudge (Shift: resize). A
// press on another box selects it; a drag off every box draws a new one.
func TestCalibrationAdjustsBoxes(t *testing.T) {
	s := newCalibrationTestSession(t)
	c := openCalibrationOn(t, s, geometryFrame(1920, 1080, "duel"))
	at, _ := surfaceMapper(t, c)
	const W, H = 1920, 1080
	avatar := testDefaultBoxes[calibLeftAvatar]
	x0, y0 := avatar.X*W, avatar.Y*H
	x1, y1 := x0+avatar.Width*W, y0+avatar.Height*H

	// Pressing inside another box selects it (and a drag moves it).
	dragSurface(c.surface, at((x0+x1)/2, (y0+y1)/2), at((x0+x1)/2+2, (y0+y1)/2))
	if c.target != calibLeftAvatar || c.targets.Selected != "左侧头像" || !nearRect(&c.drawn[calibLeftAvatar].rect, shifted(avatar, 2, 0, W, H), 1e-12) {
		t.Fatalf("press on the avatar: target %v, box %+v", c.target, c.drawn[calibLeftAvatar])
	}
	avatar = c.drawn[calibLeftAvatar].rect
	x0, x1 = x0+2, x1+2
	// Right edge: only the width changes.
	dragSurface(c.surface, at(x1, (y0+y1)/2), at(x1+8, (y0+y1)/2+5))
	got := c.drawn[calibLeftAvatar].rect
	if got.X != avatar.X || got.Y != avatar.Y || got.Height != avatar.Height || math.Abs((got.Width-avatar.Width)*W-8) > 1e-9 || !strings.Contains(c.status.Text, "已调整大小") {
		t.Fatalf("right edge: %+v from %+v", got, avatar)
	}
	// Bottom-left corner: left and bottom edges, top and right stay.
	avatar, x1 = got, x1+8
	dragSurface(c.surface, at(x0, y1), at(x0-5, y1+7))
	got = c.drawn[calibLeftAvatar].rect
	if math.Abs((got.X-avatar.X)*W+5) > 1e-9 || math.Abs((got.X+got.Width)-(avatar.X+avatar.Width)) > 1e-12 || got.Y != avatar.Y || math.Abs((got.Height-avatar.Height)*H-7) > 1e-9 {
		t.Fatalf("corner: %+v from %+v", got, avatar)
	}

	// The left bead box is 14 units tall here: pressing just above it moves it.
	c.targets.SetSelected("左侧豆")
	beads := testDefaultBoxes[calibLeftBeads]
	bx, _ := boxCentre(beads, W, H)
	press := at(bx, beads.Y*H).SubtractXY(0, 3)
	dragSurface(c.surface, press, press.AddXY(0, float32(2*c.surface.fit().scale)))
	if box := c.drawn[calibLeftBeads]; box == nil || !nearRect(&box.rect, shifted(beads, 0, 2, W, H), 1e-12) {
		t.Fatalf("thin bead box: %+v", box)
	}

	// Arrow keys after a click: one frame pixel; with Shift the size.
	if c.win.Canvas().Focused() != c.surface {
		t.Fatal("a press on the frame does not focus it for the arrow keys")
	}
	beads = c.drawn[calibLeftBeads].rect
	c.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	c.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyUp})
	c.surface.KeyDown(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	c.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyLeft})
	c.surface.KeyUp(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	got = c.drawn[calibLeftBeads].rect
	if math.Abs((got.X-beads.X)*W-1) > 1e-9 || math.Abs((got.Y-beads.Y)*H+1) > 1e-9 || math.Abs((got.Width-beads.Width)*W+1) > 1e-9 || got.Height != beads.Height {
		t.Fatalf("nudged %+v from %+v", got, beads)
	}

	// Off every box: a new box for the selected target, in whole pixels.
	dragSurface(c.surface, at(600, 600), at(700, 640))
	if box := c.drawn[calibLeftBeads]; box == nil || !nearRect(&box.rect, config.NormalizedRect{X: 600.0 / W, Y: 600.0 / H, Width: 100.0 / W, Height: 40.0 / H}, 1e-6) {
		t.Fatalf("new box %+v", box)
	}
	// Boxes stay inside the frame.
	c.targets.SetSelected("右侧头像")
	right := testDefaultBoxes[calibRightAvatar]
	rx, ry := boxCentre(right, W, H)
	dragSurface(c.surface, at(rx, ry), at(rx+400, ry-400))
	if box := c.drawn[calibRightAvatar]; box == nil || !calibrationRectValid(box.rect) || box.rect.Y != 0 || box.rect.X+box.rect.Width > 1 || math.Abs(box.rect.Width-right.Width) > 1e-12 {
		t.Fatalf("box dragged off the frame: %+v", box)
	}
}

// After 保存 the shown frame's regions are history: its automatic regions
// and default boxes go away until the first frame analysed with the saved
// boxes arrives, which then replaces it (unless the player is editing again
// or it is not a match frame).
func TestCalibrationShowsNewRegionsAfterSave(t *testing.T) {
	copyEveryFrame(t)
	s := newCalibrationTestSession(t)
	var applied []*config.RegionCalibration
	s.calibrationApply = func(c *config.RegionCalibration) { applied = append(applied, c) }
	c := openCalibrationOn(t, s, geometryFrame(1920, 1080, "duel"))
	at, _ := surfaceMapper(t, c)
	x, y := boxCentre(testDefaultBoxes[calibLeftBeads], 1920, 1080)
	dragSurface(c.surface, at(x, y), at(x+6, y))
	c.confirmSave()
	shown := c.shown()
	if len(applied) != 1 || c.fresh() || c.autoShown() || shown[calibLeftBeads].state != calibBoxSaved || shown[calibRightBeads].state != calibBoxNone ||
		calibRenderer(c).autoShade[0][0].Visible() || !strings.Contains(c.status.Text, "正在等新画面") {
		t.Fatalf("after save: fresh=%v shown=%+v status %q", c.fresh(), shown, c.status.Text)
	}

	next := geometryFrame(1920, 1080, "duel")
	next.Geometry[1].BeadBox.X += 0.001
	s.provider = func() frame.Frame { return next }
	s.captureOnce()
	if !c.fresh() || c.snap.geometry[1].BeadBox != next.Geometry[1].BeadBox || c.shown()[calibRightBeads].state != calibBoxAuto ||
		!c.autoShown() || !strings.Contains(c.status.Text, "已保存") || !strings.Contains(c.status.Text, "画面已刷新") {
		t.Fatalf("next frame not shown: fresh=%v status %q", c.fresh(), c.status.Text)
	}

	// A lobby frame does not replace a frame with regions.
	shownSnap := c.snap
	c.confirmRestoreAuto()
	c.confirm.Confirm()
	s.provider = func() frame.Frame { return calibrationTestFrame(1920, 1080, "") }
	s.captureOnce()
	if c.snap != shownSnap || len(applied) != 2 || applied[1] != nil || !strings.Contains(c.status.Text, "新画面没有自动识别区域") {
		t.Fatalf("lobby frame: replaced=%v status %q", c.snap != shownSnap, c.status.Text)
	}
	// Nor does any frame while the player is drawing again.
	c.refreshFrame() // the lobby frame
	s.provider = func() frame.Frame { return geometryFrame(1920, 1080, "camp") }
	s.captureOnce()
	c.refreshFrame()
	x, y = boxCentre(testDefaultBoxes[calibRightBeads], 1920, 1080)
	c.targets.SetSelected("右侧豆")
	dragSurface(c.surface, at(x, y), at(x, y+5))
	c.confirmSave()
	dragSurface(c.surface, at(600, 600), at(700, 640))
	editing := c.snap
	s.provider = func() frame.Frame { return geometryFrame(1920, 1080, "camp") }
	s.captureOnce()
	if c.snap != editing || !c.dirty() {
		t.Fatal("a new frame replaced the one being edited")
	}
}

// 导出方案 writes what 保存 would store (saved boxes and pending edits, not
// the untouched defaults) with the frame size; 导入方案 on another install
// loads it as pending edits of the file's profile, and 保存 stores it.
func TestCalibrationExportImportRoundTrip(t *testing.T) {
	dialogs := noFileDialogs(t)
	saved := config.NormalizedRect{X: 0.081, Y: 0.104, Width: 0.052, Height: 0.022}
	a := newCalibrationTestSession(t)
	a.cfg.Layout.Calibration = &config.RegionCalibration{Profile: "duel", LeftBeads: &saved}
	ca := openCalibrationOn(t, a, geometryFrame(2400, 1080, "duel"))
	at, _ := surfaceMapper(t, ca)
	ca.targets.SetSelected("右侧头像")
	x, y := boxCentre(testDefaultBoxes[calibRightAvatar], 2400, 1080)
	dragSurface(ca.surface, at(x, y), at(x-3, y+2))
	moved := ca.drawn[calibRightAvatar].rect
	want := &config.RegionCalibration{Profile: "duel", LeftBeads: &saved, RightAvatar: &moved}

	fynetest.Tap(overlayButton(ca.win.Content(), "导出方案"))
	if len(*dialogs) != 1 {
		t.Fatalf("导出方案 opened %d dialogs (status %q)", len(*dialogs), ca.status.Text)
	}
	data, summary, err := ca.exportData()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), calibrationSchemeFileName(2400, 1080))
	w, err := storage.Writer(storage.NewFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.writeScheme(w, nil, data, summary); err != nil || !strings.Contains(ca.status.Text, "已导出左侧豆、右侧头像的方案") ||
		!strings.Contains(ca.status.Text, "校准方案-2400x1080.json") {
		t.Fatalf("export: %v, status %q", err, ca.status.Text)
	}
	file, _ := os.ReadFile(path)
	scheme, err := decodeCalibrationScheme(file)
	if err != nil || !reflect.DeepEqual(scheme.Calibration, want) || scheme.FrameWidth != 2400 || scheme.FrameHeight != 1080 {
		t.Fatalf("exported\n%s\n→ %+v %v", file, scheme.Calibration, err)
	}
	if !ca.dirty() || a.cfg.Layout.Calibration.RightAvatar != nil {
		t.Fatal("export saved or dropped the pending edit")
	}

	b := newCalibrationTestSession(t)
	calls := 0
	b.calibrationApply = func(*config.RegionCalibration) { calls++ }
	cb := openCalibrationOn(t, b, geometryFrame(2340, 1053, "camp")) // the same aspect, another layout
	fynetest.Tap(overlayButton(cb.win.Content(), "导入方案"))
	if len(*dialogs) != 2 {
		t.Fatal("导入方案 opened no dialog")
	}
	r, err := storage.Reader(storage.NewFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := cb.readScheme(r, nil); err != nil {
		t.Fatal(err)
	}
	states := [calibTargetCount]calibBoxState{calibBoxPending, calibBoxAuto, calibBoxAuto, calibBoxPending}
	for tg, box := range cb.shown() {
		if box.state != states[tg] {
			t.Fatalf("%s after import: %+v", calibTargetNames[tg], box)
		}
	}
	if cb.drawn[calibLeftBeads].rect != saved || cb.drawn[calibRightAvatar].rect != moved || cb.drawn[calibLeftBeads].profile != "duel" ||
		strings.Contains(cb.status.Text, "比例") || !strings.Contains(cb.status.Text, "已导入决斗场画面上画的方案（2400×1080）：左侧豆、右侧头像") ||
		!strings.Contains(cb.status.Text, "这里是训练场画面") || calls != 0 || b.cfg.Layout.Calibration != nil {
		t.Fatalf("imported %+v / %+v, status %q", cb.drawn[calibLeftBeads], cb.drawn[calibRightAvatar], cb.status.Text)
	}
	cb.confirmSave()
	if !reflect.DeepEqual(b.cfg.Layout.Calibration, want) || calls != 1 {
		t.Fatalf("saved import %+v", b.cfg.Layout.Calibration)
	}
}

// A scheme drawn on another frame aspect loads, with a warning; the boxes it
// leaves out are cleared (保存 stores exactly the scheme).
func TestCalibrationImportWarnsAboutAspect(t *testing.T) {
	s := newCalibrationTestSession(t)
	rightBeads := config.NormalizedRect{X: 0.867, Y: 0.104, Width: 0.052, Height: 0.022}
	s.cfg.Layout.Calibration = &config.RegionCalibration{Profile: "duel", RightBeads: &rightBeads}
	c := openCalibrationOn(t, s, geometryFrame(1920, 1080, "duel"))
	data := []byte(docSchemeExample(t)) // 2400×1080
	if err := c.loadScheme(data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.status.Text, "方案画在 2400×1080 的画面上，和当前 1920×1080 的画面比例相差 25.0%") ||
		!strings.Contains(c.status.Text, "方案里没有右侧豆，保存后恢复自动") {
		t.Fatalf("status %q", c.status.Text)
	}
	if !c.touched[calibLeftBeads] || !c.touched[calibRightAvatar] || !c.touched[calibRightBeads] || c.drawn[calibRightBeads] != nil || c.touched[calibLeftAvatar] {
		t.Fatalf("import with a warning: touched %v", c.touched)
	}
	c.confirmSave()
	scheme, _ := decodeCalibrationScheme(data)
	if !reflect.DeepEqual(s.cfg.Layout.Calibration, scheme.Calibration) {
		t.Fatalf("saved %+v, want %+v", s.cfg.Layout.Calibration, scheme.Calibration)
	}
}

// An invalid file is rejected in words and changes nothing; importing over
// unsaved boxes asks first.
func TestCalibrationImportRejectsInvalidFiles(t *testing.T) {
	dialogs := noFileDialogs(t)
	s := newCalibrationTestSession(t)
	leftAvatar := config.NormalizedRect{X: 0.02, Y: 0.03, Width: 0.07, Height: 0.12}
	s.cfg.Layout.Calibration = &config.RegionCalibration{Profile: "duel", LeftAvatar: &leftAvatar}
	c := openCalibrationOn(t, s, geometryFrame(1920, 1080, "duel"))
	for name, file := range map[string]string{
		"not a scheme":    `{"schemaVersion": 3, "layout": {}}`,
		"newer version":   `{"kind": "naruto-timer-calibration", "version": 2, "profile": "duel"}`,
		"box out of view": `{"kind": "naruto-timer-calibration", "version": 1, "profile": "duel", "leftBeads": {"x": 0.98, "y": 0.1, "width": 0.05, "height": 0.02}}`,
		"text":            "左侧豆 0.081 0.104",
	} {
		err := c.loadScheme([]byte(file))
		if !errors.Is(err, errCalibrationScheme) || !strings.HasPrefix(c.status.Text, "未导入：") || c.dirty() ||
			!reflect.DeepEqual(c.saved, s.cfg.Layout.Calibration) || c.shown()[calibLeftAvatar].state != calibBoxSaved {
			t.Fatalf("%s: %v, status %q, dirty %v", name, err, c.status.Text, c.dirty())
		}
	}
	at, _ := surfaceMapper(t, c)
	dragSurface(c.surface, at(600, 600), at(700, 640))
	fynetest.Tap(overlayButton(c.win.Content(), "导入方案"))
	if c.confirm == nil || len(*dialogs) != 0 {
		t.Fatal("import over unsaved boxes did not ask first")
	}
	c.confirm.Confirm()
	if len(*dialogs) != 1 {
		t.Fatal("confirmed import opened no file dialog")
	}
	// Nothing to export without a calibration: the defaults are automatic.
	empty := newCalibrationTestSession(t)
	ce := openCalibrationOn(t, empty, geometryFrame(1920, 1080, "duel"))
	fynetest.Tap(overlayButton(ce.win.Content(), "导出方案"))
	if len(*dialogs) != 1 || !strings.Contains(ce.status.Text, "没有校准框") {
		t.Fatalf("export without boxes: status %q", ce.status.Text)
	}
}

// Snapshot geometry is a private copy in the snapshot's own (0,0) pixels.
func TestCalibrationSnapshotKeepsGeometry(t *testing.T) {
	parent := calibrationTestFrame(64, 40, "duel").Img
	sub := parent.SubImage(image.Rect(5, 7, 45, 30)).(*image.RGBA)
	box := config.NormalizedRect{X: 0.2, Y: 0.1, Width: 0.4, Height: 0.2}
	g := [2]frame.SideGeometry{{Beads: []image.Point{{10, 12}, {14, 12}}, NameROI: image.Rect(6, 8, 20, 15), AvatarROI: image.Rect(5, 0, 9, 9), BeadBox: box}}
	snap := newCalibrationSnapshot(frame.Frame{Img: sub, Geometry: g, Fighting: true, LayoutProfile: "duel"})
	got := snap.geometry[0]
	if !reflect.DeepEqual(got.Beads, []image.Point{{5, 5}, {9, 5}}) || got.NameROI != image.Rect(1, 1, 15, 8) || got.AvatarROI != image.Rect(0, -7, 4, 2) ||
		got.BeadBox != box || !snap.hasGeometry() || snap.geometry[1].NameROI != (image.Rectangle{}) {
		t.Fatalf("snapshot geometry %+v", snap.geometry)
	}
	g[0].Beads[0].X = 99
	if snap.geometry[0].Beads[0].X != 5 {
		t.Fatal("snapshot shares the frame's bead slice")
	}
	if r, ok := snap.defaultBox(calibLeftBeads); !ok || r != box {
		t.Fatalf("default box %+v %v", r, ok)
	}
	if _, ok := snap.defaultBox(calibLeftAvatar); ok {
		t.Fatal("a zero avatar box is no default")
	}
	if newCalibrationSnapshot(calibrationTestFrame(64, 40, "duel")).hasGeometry() {
		t.Fatal("a frame without an analysis has geometry")
	}
}

// Overlay lines are plain canvas lines: keep the count bounded however big
// the window gets.
func TestCalibrationDashesStayBounded(t *testing.T) {
	s := newCalibrationTestSession(t)
	c := openCalibrationOn(t, s, geometryFrame(1920, 1080, "duel"))
	c.win.Resize(fyne.NewSize(3800, 2200))
	c.surface.Refresh()
	r := calibRenderer(c)
	for side := range r.autoDash {
		for k, pool := range r.autoDash[side] {
			visible := 0
			for _, l := range pool {
				if l.Visible() {
					visible++
				}
			}
			if visible == 0 || visible > 4*(calibDashesPerSide+1) {
				t.Fatalf("side %d region %d: %d dashes", side, k, visible)
			}
		}
	}
}
