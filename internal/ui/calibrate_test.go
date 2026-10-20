package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	fynetest "fyne.io/fyne/v2/test"

	"narutotimer/internal/config"
	"narutotimer/internal/draw"
	"narutotimer/internal/frame"
)

func newCalibrationTestSession(t *testing.T) *session {
	t.Helper()
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), done: make(chan struct{}), remember: true}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	t.Cleanup(func() {
		if s.calibrationWin != nil {
			s.calibrationWin.win.Close()
		}
		if s.settings != nil {
			s.settings.Close()
		}
	})
	return s
}

// calibrationTestFrame is a captured frame whose pixels encode their position.
// profile "" is a non-match page.
func calibrationTestFrame(w, h int, profile string) frame.Frame {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = uint8(x), uint8(y), uint8(x^y), 255
		}
	}
	f := frame.Frame{Img: img, Scene: "lobby", CaptureMethod: "test", CapturedAt: time.Now()}
	if profile != "" {
		f.Scene, f.Fighting, f.LayoutProfile = "fight", true, profile
	}
	return f
}

// openCalibrationOn opens the window and lets the capture loop deliver f.
func openCalibrationOn(t *testing.T, s *session, f frame.Frame) *calibrationWindow {
	t.Helper()
	s.openCalibration()
	c := s.calibrationWin
	if c == nil {
		t.Fatal("calibration window did not open")
	}
	s.provider = func() frame.Frame { return f }
	s.captureOnce()
	if c.snap == nil || c.snap.img.Bounds().Size() != f.Img.Bounds().Size() {
		t.Fatal("the first captured frame was not shown")
	}
	return c
}

// dragSurface replays a mouse drag the way Fyne's desktop driver reports it:
// Position is the pointer relative to the widget, Dragged the movement since
// the previous event (the first event already includes the start threshold).
func dragSurface(v *calibrationSurface, from, to fyne.Position) {
	v.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: from}})
	previous := from
	for i := 1; i <= 4; i++ {
		f := float32(i) / 4
		p := fyne.NewPos(from.X+(to.X-from.X)*f, from.Y+(to.Y-from.Y)*f)
		if i == 4 {
			p = to
		}
		v.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: p}, Dragged: fyne.NewDelta(p.X-previous.X, p.Y-previous.Y)})
		previous = p
	}
	v.DragEnd()
	v.MouseUp(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: to}})
}

// surfaceMapper maps frame pixels to surface positions, computed here from the
// surface size rather than with the code under test.
func surfaceMapper(t *testing.T, c *calibrationWindow) (func(px, py float64) fyne.Position, float64) {
	t.Helper()
	size := c.surface.Size()
	b := c.snap.img.Bounds()
	if size.Width < 100 || size.Height < 100 {
		t.Fatalf("surface not laid out: %v", size)
	}
	scale := math.Min(float64(size.Width)/float64(b.Dx()), float64(size.Height)/float64(b.Dy()))
	ox := (float64(size.Width) - scale*float64(b.Dx())) / 2
	oy := (float64(size.Height) - scale*float64(b.Dy())) / 2
	return func(px, py float64) fyne.Position {
		return fyne.NewPos(float32(ox+px*scale), float32(oy+py*scale))
	}, scale
}

func nearRect(got *config.NormalizedRect, want config.NormalizedRect, tolerance float64) bool {
	return got != nil && math.Abs(got.X-want.X) <= tolerance && math.Abs(got.Y-want.Y) <= tolerance &&
		math.Abs(got.Width-want.Width) <= tolerance && math.Abs(got.Height-want.Height) <= tolerance
}

func near(a, b fyne.Position, tolerance float32) bool {
	return math.Abs(float64(a.X-b.X)) <= float64(tolerance) && math.Abs(float64(a.Y-b.Y)) <= float64(tolerance)
}

func TestCalibrationDragRoundTripsThroughFrameFractions(t *testing.T) {
	for _, size := range []image.Point{{2388, 1668}, {2400, 1080}} {
		t.Run(fmt.Sprintf("%dx%d", size.X, size.Y), func(t *testing.T) {
			s := newCalibrationTestSession(t)
			c := openCalibrationOn(t, s, calibrationTestFrame(size.X, size.Y, "duel"))
			at, scale := surfaceMapper(t, c)
			W, H := float64(size.X), float64(size.Y)

			// Left beads, dragged from the bottom-right corner to the top-left.
			x0, y0 := math.Round(0.081*W), math.Round(0.104*H)
			x1, y1 := math.Round(0.133*W), math.Round(0.126*H)
			dragSurface(c.surface, at(x1, y1), at(x0, y0))
			want := config.NormalizedRect{X: x0 / W, Y: y0 / H, Width: (x1 - x0) / W, Height: (y1 - y0) / H}
			if box := c.drawn[calibLeftBeads]; box == nil || !nearRect(&box.rect, want, 1e-6) || box.profile != "duel" {
				t.Fatalf("left beads = %+v, want %+v", box, want)
			}
			// The outline is drawn where the drag was, at the window's scale.
			r := fynetest.WidgetRenderer(c.surface).(*calibrationRenderer)
			outline := r.boxes[calibLeftBeads]
			if !outline.Visible() || !near(outline.Position(), at(x0, y0), 0.01) ||
				!near(fyne.NewPos(outline.Size().Width, outline.Size().Height), fyne.NewPos(float32((x1-x0)*scale), float32((y1-y0)*scale)), 0.01) {
				t.Fatalf("outline at %v size %v, dragged %v → %v", outline.Position(), outline.Size(), at(x0, y0), at(x1, y1))
			}
			for i, dot := range r.dots[calibLeftBeads] {
				centre := dot.Position().AddXY(dot.Size().Width/2, dot.Size().Height/2)
				if wantCentre := at(x0+(float64(i)+0.5)*(x1-x0)/4, (y0+y1)/2); !dot.Visible() || !near(centre, wantCentre, 0.01) {
					t.Fatalf("bead %d centre %v, want %v", i, centre, wantCentre)
				}
			}

			// Right avatar, dragged past the frame's top-right corner: clamped.
			c.targets.SetSelected("右侧头像")
			ax, ay := math.Round(0.924*W), math.Round(0.134*H)
			dragSurface(c.surface, at(ax, ay), fyne.NewPos(c.surface.Size().Width+40, -30))
			avatar := c.drawn[calibRightAvatar]
			if avatar == nil || !nearRect(&avatar.rect, config.NormalizedRect{X: ax / W, Y: 0, Width: 1 - ax/W, Height: ay / H}, 1e-6) ||
				avatar.rect.X+avatar.rect.Width > 1 {
				t.Fatalf("clamped avatar = %+v", avatar)
			}

			// A slip of a few pixels is not a box and keeps the previous one.
			c.targets.SetSelected("左侧豆")
			dragSurface(c.surface, at(W/2, H/2), at(W/2, H/2).AddXY(3, 2))
			if box := c.drawn[calibLeftBeads]; box == nil || !nearRect(&box.rect, want, 1e-6) || !strings.Contains(c.status.Text, "框太小") {
				t.Fatalf("tiny drag replaced the box: %+v status %q", box, c.status.Text)
			}

			if err := c.save(); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(s.cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			cal := cfg.Layout.Calibration
			if cal == nil || cal.Profile != "duel" || !nearRect(cal.LeftBeads, want, 1e-6) || !nearRect(cal.RightAvatar, avatar.rect, 0) ||
				cal.RightBeads != nil || cal.LeftAvatar != nil {
				t.Fatalf("saved calibration = %+v", cal)
			}
			// Fractions → pixels gives back the dragged frame pixels.
			if px := cal.LeftBeads.X * W; math.Abs(px-x0) > 0.01 {
				t.Fatalf("saved x maps back to pixel %.3f, dragged %v", px, x0)
			}
		})
	}
}

func TestCalibrationSaveKeepsUntouchedBoxes(t *testing.T) {
	s := newCalibrationTestSession(t)
	leftBeads := config.NormalizedRect{X: 0.081, Y: 0.104, Width: 0.052, Height: 0.022}
	rightAvatar := config.NormalizedRect{X: 0.924, Y: 0.006, Width: 0.072, Height: 0.128}
	s.cfg.Layout.Calibration = &config.RegionCalibration{Profile: "duel", LeftBeads: &leftBeads, RightAvatar: &rightAvatar}
	var applied []*config.RegionCalibration
	s.calibrationApply = func(c *config.RegionCalibration) { applied = append(applied, c) }
	c := openCalibrationOn(t, s, calibrationTestFrame(1920, 1080, "duel"))
	at, _ := surfaceMapper(t, c)

	c.targets.SetSelected("右侧豆")
	dragSurface(c.surface, at(1665, 112), at(1765, 136))
	rightBeads := config.NormalizedRect{X: 1665.0 / 1920, Y: 112.0 / 1080, Width: 100.0 / 1920, Height: 24.0 / 1080}
	c.confirmSave()
	if c.confirm != nil {
		t.Fatal("same-layout save asked to drop boxes")
	}
	got := s.cfg.Layout.Calibration
	if got == nil || got.Profile != "duel" || *got.LeftBeads != leftBeads || *got.RightAvatar != rightAvatar ||
		!nearRect(got.RightBeads, rightBeads, 1e-6) || got.LeftAvatar != nil {
		t.Fatalf("saved %+v", got)
	}
	if c.dirty() || !strings.Contains(c.status.Text, "已保存") {
		t.Fatalf("after save: dirty=%v status %q", c.dirty(), c.status.Text)
	}
	disk, err := config.Load(s.cfgPath)
	if err != nil || !reflect.DeepEqual(disk.Layout.Calibration, got) {
		t.Fatalf("disk %+v, %v", disk.Layout.Calibration, err)
	}
	// The engine gets an equal, private copy.
	if len(applied) != 1 || !reflect.DeepEqual(applied[0], got) || applied[0] == got || applied[0].LeftBeads == got.LeftBeads {
		t.Fatalf("hook got %+v", applied)
	}

	// Clearing one box removes only that one.
	c.targets.SetSelected("左侧豆")
	c.clearSelected()
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	got = s.cfg.Layout.Calibration
	if got.LeftBeads != nil || *got.RightAvatar != rightAvatar || !nearRect(got.RightBeads, rightBeads, 1e-6) || len(applied) != 2 {
		t.Fatalf("after clearing left beads: %+v (hook calls %d)", got, len(applied))
	}

	// Reopening shows the saved boxes and no leftover edits.
	c.requestClose()
	reopened := openCalibrationOn(t, s, calibrationTestFrame(1920, 1080, "duel"))
	if reopened == c || reopened.dirty() || !reflect.DeepEqual(reopened.boxes(), calibrationRects(got)) || reopened.target != calibLeftBeads {
		t.Fatalf("reopened window state: %+v", reopened.boxes())
	}
}

func TestCalibrationOnOtherLayoutAsksBeforeDroppingSavedBoxes(t *testing.T) {
	s := newCalibrationTestSession(t)
	leftAvatar := config.NormalizedRect{X: 0.004, Y: 0.006, Width: 0.072, Height: 0.128}
	s.cfg.Layout.Calibration = &config.RegionCalibration{Profile: "duel", LeftAvatar: &leftAvatar}
	c := openCalibrationOn(t, s, calibrationTestFrame(1920, 1080, "camp"))
	if !strings.Contains(c.status.Text, "决斗场") {
		t.Fatalf("no layout note: %q", c.status.Text)
	}
	at, _ := surfaceMapper(t, c)
	dragSurface(c.surface, at(150, 100), at(250, 124))
	c.confirmSave()
	if c.confirm == nil || s.cfg.Layout.Calibration.Profile != "duel" {
		t.Fatal("saved over boxes from the other layout without asking")
	}
	c.confirm.Confirm()
	got := s.cfg.Layout.Calibration
	if got == nil || got.Profile != "camp" || got.LeftAvatar != nil || got.LeftBeads == nil {
		t.Fatalf("after confirming: %+v", got)
	}
}

func TestCalibrationSaveNeedsMatchOrTrainingFrame(t *testing.T) {
	lobby := calibrationTestFrame(1920, 1080, "")
	guessed := calibrationTestFrame(1920, 1080, "") // engine guess on a VS page
	guessed.Scene, guessed.LayoutProfile, guessed.Hold = "vs", "duel", true
	interval := calibrationCopyInterval
	calibrationCopyInterval = 0 // the test captures again at once
	t.Cleanup(func() { calibrationCopyInterval = interval })
	for name, f := range map[string]frame.Frame{"lobby": lobby, "unrecognised": guessed} {
		t.Run(name, func(t *testing.T) {
			s := newCalibrationTestSession(t)
			calls := 0
			s.calibrationApply = func(*config.RegionCalibration) { calls++ }
			c := openCalibrationOn(t, s, f)
			if c.snap.profile != "" || !c.layout.Visible() || !strings.Contains(c.info.Text, "未识别") || !strings.Contains(c.status.Text, "选择训练场/决斗场") {
				t.Fatalf("profile %q info %q status %q", c.snap.profile, c.info.Text, c.status.Text)
			}
			at, _ := surfaceMapper(t, c)
			dragSurface(c.surface, at(150, 100), at(250, 124))
			if !c.dirty() {
				t.Fatal("box not drawn")
			}
			if err := c.save(); !errors.Is(err, errCalibrationProfile) {
				t.Fatalf("save err = %v", err)
			}
			if s.cfg.Layout.Calibration != nil || calls != 0 || !c.dirty() || !strings.Contains(c.status.Text, "请在对局或训练场画面上校准") {
				t.Fatalf("rejected save changed state: cal=%+v hook=%d status %q", s.cfg.Layout.Calibration, calls, c.status.Text)
			}
			if _, err := os.Stat(s.cfgPath); !os.IsNotExist(err) {
				t.Fatalf("config written: %v", err)
			}
			// An unsupported resolution may never be recognised as a match:
			// the player names the layout and the boxes save.
			c.layout.SetSelected("决斗场")
			dragSurface(c.surface, at(150, 100), at(250, 124))
			if err := c.save(); err != nil || s.cfg.Layout.Calibration == nil || s.cfg.Layout.Calibration.Profile != "duel" || calls != 1 {
				t.Fatalf("save with a picked layout: %v %+v", err, s.cfg.Layout.Calibration)
			}
			// A recognised match frame overrides the pick.
			s.provider = func() frame.Frame { return calibrationTestFrame(1920, 1080, "camp") }
			s.captureOnce()
			c.refreshFrame()
			if c.layout.Visible() {
				t.Fatal("layout picker shown on a recognised frame")
			}
			dragSurface(c.surface, at(150, 100), at(250, 124))
			if err := c.save(); err != nil || s.cfg.Layout.Calibration == nil || s.cfg.Layout.Calibration.Profile != "camp" || calls != 2 {
				t.Fatalf("save on training frame: %v %+v", err, s.cfg.Layout.Calibration)
			}
		})
	}
}

func TestCalibrationRestoreAutoClearsAndCallsHook(t *testing.T) {
	s := newCalibrationTestSession(t)
	box := config.NormalizedRect{X: 0.081, Y: 0.104, Width: 0.052, Height: 0.022}
	s.cfg.Layout.Calibration = &config.RegionCalibration{Profile: "camp", LeftBeads: &box}
	if err := s.saveSettingsLocked(); err != nil {
		t.Fatal(err)
	}
	var calls []*config.RegionCalibration
	s.calibrationApply = func(c *config.RegionCalibration) { calls = append(calls, c) }
	c := openCalibrationOn(t, s, calibrationTestFrame(1920, 1080, "camp"))
	at, _ := surfaceMapper(t, c)
	c.targets.SetSelected("右侧头像")
	dragSurface(c.surface, at(1770, 6), at(1910, 140))

	c.confirmRestoreAuto()
	if c.confirm == nil || s.cfg.Layout.Calibration == nil {
		t.Fatal("全部恢复自动 did not ask first")
	}
	c.confirm.Confirm()
	if s.cfg.Layout.Calibration != nil || len(calls) != 1 || calls[0] != nil {
		t.Fatalf("calibration %+v hook %v", s.cfg.Layout.Calibration, calls)
	}
	if c.dirty() || c.saved != nil || c.boxes() != [calibTargetCount]*config.NormalizedRect{} {
		t.Fatalf("window kept boxes: %+v", c.boxes())
	}
	disk, err := config.Load(s.cfgPath)
	if err != nil || disk.Layout.Calibration != nil {
		t.Fatalf("disk calibration %+v, %v", disk.Layout.Calibration, err)
	}
	// Without a hook (not wired yet) it is a plain settings change.
	s.calibrationApply = nil
	if err := c.restoreAuto(); err != nil {
		t.Fatal(err)
	}
}

func TestCalibrationCopiesFramesOnlyWhileOpen(t *testing.T) {
	s := newCalibrationTestSession(t)
	ring := calibrationTestFrame(1280, 720, "camp") // one reused capture buffer
	s.provider = func() frame.Frame { return ring }
	s.captureOnce()
	if s.calibrationFeed.open.Load() || s.calibrationFeed.snapshot() != nil {
		t.Fatal("copied a frame with the window closed")
	}

	s.openCalibration()
	c := s.calibrationWin
	s.captureOnce()
	snap := s.calibrationFeed.snapshot()
	if snap == nil || c.snap != snap || snap.profile != "camp" {
		t.Fatalf("open window got %+v", snap)
	}
	if &snap.img.Pix[0] == &ring.Img.Pix[0] || !bytes.Equal(snap.img.Pix, ring.Img.Pix) {
		t.Fatal("snapshot is not a deep copy of the frame")
	}
	want := append([]byte(nil), snap.img.Pix...)
	clear(ring.Img.Pix) // the capture reuses the buffer for a later frame
	if !bytes.Equal(snap.img.Pix, want) {
		t.Fatal("snapshot changed with the capture buffer")
	}

	// A frame from a replaced emulator instance is never copied.
	s.calibrationFeed.stop()
	s.calibrationFeed.start(nil)
	stale := calibrationTestFrame(1280, 720, "camp")
	stale.SourceRevision = 7
	s.provider = func() frame.Frame { return stale }
	s.captureOnce()
	if s.calibrationFeed.snapshot() != nil {
		t.Fatal("copied a stale instance's frame")
	}

	c.requestClose()
	if s.calibrationWin != nil || s.calibrationFeed.open.Load() || s.calibrationFeed.snapshot() != nil {
		t.Fatal("closing kept the feed")
	}
	s.provider = func() frame.Frame { return calibrationTestFrame(1280, 720, "camp") }
	s.captureOnce()
	if s.calibrationFeed.snapshot() != nil {
		t.Fatal("copied after the window closed")
	}

	s.openCalibration()
	if s.calibrationWin == nil || s.calibrationWin == c || s.calibrationWin.snap != nil || s.calibrationWin.dirty() {
		t.Fatal("reopened window kept old state")
	}
}

func TestCalibrationWaitsForFrameAndAsksBeforeDroppingEdits(t *testing.T) {
	s := newCalibrationTestSession(t)
	s.openCalibration()
	c := s.calibrationWin
	c.refreshFrame()
	if c.snap != nil || !strings.Contains(c.status.Text, "还没有采集到画面") {
		t.Fatalf("no frame yet: status %q", c.status.Text)
	}
	// A drag before any frame arrives draws nothing.
	dragSurface(c.surface, fyne.NewPos(10, 10), fyne.NewPos(200, 100))
	if c.dirty() {
		t.Fatal("drew a box without a frame")
	}
	// The first captured frame appears without pressing 刷新画面.
	s.provider = func() frame.Frame { return calibrationTestFrame(1920, 1080, "duel") }
	s.captureOnce()
	if c.snap == nil || c.info.Text != "决斗场 · 1920×1080" {
		t.Fatalf("first frame not shown: %q", c.info.Text)
	}

	at, _ := surfaceMapper(t, c)
	dragSurface(c.surface, at(150, 100), at(250, 124))
	c.requestClose()
	if c.confirm == nil || s.calibrationWin != c || !s.calibrationFeed.open.Load() {
		t.Fatal("closed with unsaved boxes without asking")
	}
	c.confirm.Hide() // 取消
	if s.calibrationWin != c || !c.dirty() {
		t.Fatal("cancel closed the window or dropped the box")
	}
	c.requestClose()
	c.confirm.Confirm()
	if s.calibrationWin != nil || s.calibrationFeed.open.Load() || s.cfg.Layout.Calibration != nil {
		t.Fatal("confirmed close kept the window or saved the box")
	}
}

func TestCalibrationFeedThrottlesCopies(t *testing.T) {
	var feed calibrationFeed
	f := calibrationTestFrame(400, 250, "duel")
	notified := 0
	feed.start(func() { notified++ })
	at := time.Now()
	feed.offer(f, at)
	first := feed.snapshot()
	feed.offer(f, at.Add(calibrationCopyInterval/2))
	if feed.snapshot() != first {
		t.Fatal("copied again before the interval")
	}
	feed.offer(f, at.Add(calibrationCopyInterval))
	if feed.snapshot() == first || notified != 1 {
		t.Fatalf("newer copy missing or notified %d times", notified)
	}
	feed.offer(frame.Frame{Img: f.Img, Err: errors.New("capture failed")}, at.Add(time.Hour))
	if feed.snapshot().img == nil {
		t.Fatal("failed capture replaced the copy")
	}
}

// The capture goroutine offers frames while the UI opens, reads and closes.
func TestCalibrationFeedAcrossGoroutines(t *testing.T) {
	var feed calibrationFeed
	f := calibrationTestFrame(400, 250, "duel")
	done := make(chan struct{})
	go func() {
		defer close(done)
		at := time.Now()
		for i := range 2000 {
			feed.offer(f, at.Add(time.Duration(i)*time.Second))
		}
	}()
	for range 200 {
		feed.start(func() {})
		if snap := feed.snapshot(); snap != nil && snap.img.Bounds().Dx() != 400 {
			t.Fatal("torn snapshot")
		}
		feed.stop()
	}
	<-done
	if feed.open.Load() || feed.snapshot() != nil {
		t.Fatal("a stopped feed kept a frame")
	}
}

func TestCopyFrameImageHandlesPaddedFrames(t *testing.T) {
	parent := calibrationTestFrame(64, 40, "duel").Img
	sub := parent.SubImage(image.Rect(5, 7, 45, 30)).(*image.RGBA)
	got := copyFrameImage(sub)
	if got.Bounds() != image.Rect(0, 0, 40, 23) {
		t.Fatalf("bounds %v", got.Bounds())
	}
	for y := 0; y < 23; y++ {
		for x := 0; x < 40; x++ {
			if got.RGBAAt(x, y) != sub.RGBAAt(x+5, y+7) {
				t.Fatalf("pixel %d,%d differs", x, y)
			}
		}
	}
}

func TestMergeCalibration(t *testing.T) {
	r := func(x float64) config.NormalizedRect {
		return config.NormalizedRect{X: x, Y: 0.1, Width: 0.05, Height: 0.02}
	}
	saved := &config.RegionCalibration{Profile: "duel", LeftBeads: ptr(r(0.08)), RightBeads: ptr(r(0.87))}
	var drawn [calibTargetCount]*calibBox
	var touched [calibTargetCount]bool
	drawn[calibLeftAvatar], touched[calibLeftAvatar] = &calibBox{rect: r(0.01), profile: "duel"}, true
	next, dropped, err := mergeCalibration(saved, drawn, touched)
	if err != nil || len(dropped) != 0 || *next.LeftBeads != r(0.08) || *next.RightBeads != r(0.87) || *next.LeftAvatar != r(0.01) || next.Profile != "duel" {
		t.Fatalf("merge = %+v %v %v", next, dropped, err)
	}
	if next.LeftBeads == saved.LeftBeads {
		t.Fatal("merged calibration shares a rect with the saved one")
	}
	drawn[calibRightAvatar], touched[calibRightAvatar] = &calibBox{rect: r(0.9), profile: "camp"}, true
	if _, _, err := mergeCalibration(saved, drawn, touched); !errors.Is(err, errCalibrationMixed) {
		t.Fatalf("mixed layouts: %v", err)
	}
	drawn[calibLeftAvatar] = &calibBox{rect: r(0.01), profile: "camp"}
	next, dropped, err = mergeCalibration(saved, drawn, touched)
	if err != nil || next.Profile != "camp" || next.LeftBeads != nil || next.RightBeads != nil || !reflect.DeepEqual(dropped, []calibTarget{calibLeftBeads, calibRightBeads}) {
		t.Fatalf("other layout = %+v dropped %v err %v", next, dropped, err)
	}
	// Clearing every saved box (and drawing nothing) is fully automatic.
	var cleared [calibTargetCount]bool
	cleared[calibLeftBeads], cleared[calibRightBeads] = true, true
	if next, _, err := mergeCalibration(saved, [calibTargetCount]*calibBox{}, cleared); next != nil || err != nil {
		t.Fatalf("all cleared = %+v %v", next, err)
	}
}

func TestCalibrationBeadCentresStartOutermost(t *testing.T) {
	box := config.NormalizedRect{X: 0.1, Y: 0.2, Width: 0.04, Height: 0.02}
	left, right := calibrationBeadCenters(box, true), calibrationBeadCenters(box, false)
	for i := range 4 {
		wantLeft := 0.1 + (float64(i)+0.5)*0.01
		if math.Abs(left[i].X-wantLeft) > 1e-12 || math.Abs(right[i].X-(0.14-(float64(i)+0.5)*0.01)) > 1e-12 || math.Abs(left[i].Y-0.21) > 1e-12 {
			t.Fatalf("bead %d: left %+v right %+v", i, left[i], right[i])
		}
	}
}

func TestSettingsOpensOneCalibrationWindow(t *testing.T) {
	s := newCalibrationTestSession(t)
	s.openSettings()
	button := overlayButton(s.settings.Content(), "区域校准（豆/头像）")
	if button == nil {
		t.Fatal("settings has no calibration button")
	}
	fynetest.Tap(button)
	first := s.calibrationWin
	if first == nil || !s.calibrationFeed.open.Load() {
		t.Fatal("button did not open the calibration window")
	}
	fynetest.Tap(button)
	if s.calibrationWin != first {
		t.Fatal("a second calibration window was opened")
	}
	// Usable at its default size: most of the window is the frame.
	if size := first.surface.Size(); size.Width < 880 || size.Height < 380 {
		t.Fatalf("frame area %v in a 960×620 window", size)
	}
}

func ptr[T any](v T) *T { return &v }

// calibrationPreviewFrame is a synthetic HUD: avatars, bars and four beads per
// side at the positions of the example in docs/区域校准.md.
func calibrationPreviewFrame(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(30 + 40*y/h), G: uint8(60 + 60*x/w), B: uint8(90 + 50*y/h), A: 255})
		}
	}
	fill := func(r config.NormalizedRect, c color.RGBA) {
		for y := int(r.Y * float64(h)); y < int((r.Y+r.Height)*float64(h)); y++ {
			for x := int(r.X * float64(w)); x < int((r.X+r.Width)*float64(w)); x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	avatar := color.RGBA{R: 210, G: 170, B: 120, A: 255}
	fill(config.NormalizedRect{X: 0.004, Y: 0.006, Width: 0.072, Height: 0.128}, avatar)
	fill(config.NormalizedRect{X: 0.924, Y: 0.006, Width: 0.072, Height: 0.128}, avatar)
	fill(config.NormalizedRect{X: 0.081, Y: 0.05, Width: 0.3, Height: 0.03}, color.RGBA{R: 240, G: 200, B: 40, A: 255})
	fill(config.NormalizedRect{X: 0.619, Y: 0.05, Width: 0.3, Height: 0.03}, color.RGBA{R: 240, G: 200, B: 40, A: 255})
	beadW, beadH := int(0.009*float64(w)), int(0.02*float64(h))
	for _, side := range []struct {
		box  config.NormalizedRect
		left bool
	}{{config.NormalizedRect{X: 0.081, Y: 0.104, Width: 0.052, Height: 0.022}, true}, {config.NormalizedRect{X: 0.867, Y: 0.104, Width: 0.052, Height: 0.022}, false}} {
		for i, c := range calibrationBeadCenters(side.box, side.left) {
			col := color.RGBA{R: 90, G: 230, B: 255, A: 255}
			if i == 3 {
				col = color.RGBA{R: 30, G: 70, B: 90, A: 255}
			}
			draw.FillDiamond(img, int(c.X*float64(w)), int(c.Y*float64(h)), beadW, beadH, col)
		}
	}
	return img
}
