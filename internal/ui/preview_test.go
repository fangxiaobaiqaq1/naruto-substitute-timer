package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"

	timerapp "narutotimer/internal/app"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

// seedEvent confirms one real substitution drop that started `ago` before now.
func seedEvent(c *timerapp.SideClock, ago time.Duration) {
	t0 := time.Now().Add(-ago - 100*time.Millisecond)
	c.Observe(4, true, t0, ninja.DefaultCooldown, 2)
	c.Observe(3, true, t0.Add(100*time.Millisecond), ninja.DefaultCooldown, 2)
	c.Observe(3, true, t0.Add(200*time.Millisecond), ninja.DefaultCooldown, 2)
}

// TIMER_UI_PREVIEW_DIR=/tmp/ui go test ./internal/ui -run TestWindowPreviews
// saves software-rendered screenshots of every window for visual review.
// It opens no native window and needs no game capture.
func TestWindowPreviews(t *testing.T) {
	out := os.Getenv("TIMER_UI_PREVIEW_DIR")
	if out == "" {
		t.Skip("set TIMER_UI_PREVIEW_DIR to save window previews")
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		t.Fatal(err)
	}
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	save := func(name string, img image.Image) {
		f, err := os.Create(filepath.Join(out, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	newPreviewSession := func() *session {
		s := &session{cfg: config.Default(), side: "left", scene: "fight", fighting: true, beads: testBeads(2, 3), done: make(chan struct{})}
		s.cfgPath = filepath.Join(t.TempDir(), "config.json")
		s.rightNinja = "宇智波斑[秽土转生]"
		return s
	}

	// Full overlay in its main states; the window is fitted exactly like Run.
	for _, state := range []struct {
		name  string
		setup func(*session)
	}{
		{"overlay-idle", func(s *session) { s.fighting, s.scene, s.beads = false, "lobby", nil }},
		{"overlay-counting", func(s *session) { seedEvent(&s.right, 2600*time.Millisecond) }},
		{"overlay-warning", func(s *session) { seedEvent(&s.right, 12500*time.Millisecond) }},
		{"overlay-unknown-side", func(s *session) { s.side = ""; s.cfg.UI.PlayerSide = "auto" }},
		{"overlay-both", func(s *session) {
			s.cfg.UI.ShowBothSides = true
			seedEvent(&s.right, 4000*time.Millisecond)
		}},
		{"overlay-mizukage", func(s *session) {
			s.rightNinja = "照美冥[五代目水影]"
			seedEvent(&s.right, 3000*time.Millisecond)
		}},
	} {
		s := newPreviewSession()
		state.setup(s)
		w := fynetest.NewWindow(nil)
		s.win = w
		w.SetPadded(false)
		w.SetContent(s.overlayContent())
		s.refreshOverlay()
		s.refreshClockAt(time.Now())
		s.applyPendingClock()
		s.fitOverlayWindow()
		save(state.name, w.Canvas().Capture())
		w.Close()
	}

	// Detached mini (color-key transparency cannot be reproduced here).
	{
		s := newPreviewSession()
		seedEvent(&s.right, 2600*time.Millisecond)
		w := fynetest.NewWindow(nil)
		s.win = w
		w.SetContent(s.overlayContent())
		s.setupDetachedMiniWindow(a)
		s.miniMode = true
		s.refreshClockAt(time.Now())
		s.applyPendingClock()
		s.fitDetachedMiniWindow()
		save("mini", s.miniWin.Canvas().Capture())
		s.toggleDetachedMiniControls()
		save("mini-controls", s.miniWin.Canvas().Capture())
	}

	// Settings tabs and the diagnostics window.
	{
		s := newPreviewSession()
		w := fynetest.NewWindow(nil)
		s.win = w
		w.SetContent(s.overlayContent())
		s.openSettings()
		for i, item := range s.settingsTabs.Items {
			s.settingsTabs.SelectIndex(i)
			save("settings-"+string(rune('0'+i))+"-"+item.Text, s.settings.Canvas().Capture())
		}
		s.openDiagnostics()
		save("diagnostics", s.diagnosticWindow.Canvas().Capture())
		if scroll, ok := s.settingsTabs.Items[0].Content.(*container.Scroll); ok {
			s.settingsTabs.SelectIndex(0)
			scroll.ScrollToBottom()
			save("settings-0-常规-bottom", s.settings.Canvas().Capture())
		}
	}

	// Region calibration on synthetic HUDs (not captures) with the geometry
	// the real engine reports on them: the automatic regions and default
	// boxes; with a saved left avatar and the right beads moved but not saved;
	// a non-16:9 tablet frame; a frame without an analysis.
	// The GLFW driver re-lays out a label whose text grew; this canvas does not.
	relayout := func(w fyne.Window) { w.Content().Refresh() }
	for _, view := range []struct {
		name  string
		size  image.Point
		edits bool
		bare  bool // no analysis: a lobby-like frame
	}{
		{"calibration-auto", image.Pt(2400, 1080), false, false},
		{"calibration-2400x1080", image.Pt(2400, 1080), true, false},
		{"calibration-2388x1668", image.Pt(2388, 1668), false, false},
		{"calibration-1280x720-no-geometry", image.Pt(1280, 720), false, true},
	} {
		s := newPreviewSession()
		w := fynetest.NewWindow(nil)
		s.win = w
		w.SetContent(s.overlayContent())
		if view.edits {
			_, auto := calibrationPreviewHUD(view.size, nil)
			leftAvatar := auto[0].AvatarBox
			leftAvatar.X += 6.0 / float64(view.size.X)
			s.cfg.Layout.Calibration = &config.RegionCalibration{Profile: "duel", LeftAvatar: &leftAvatar}
		}
		s.openCalibration()
		c := s.calibrationWin
		if view.edits {
			save("calibration-waiting", c.win.Canvas().Capture())
		}
		img, geometry := calibrationPreviewHUD(view.size, s.cfg.Layout.Calibration)
		f := frame.Frame{Img: img, Fighting: true, LayoutProfile: "duel", Geometry: geometry}
		if view.bare {
			f.Geometry = [2]frame.SideGeometry{}
		}
		s.calibrationFeed.offer(f, time.Now())
		relayout(c.win)
		if view.edits {
			c.targets.SetSelected("右侧豆")
			fit := c.surface.fit()
			box := geometry[1].BeadBox
			from := fit.point(box.X+box.Width/2, box.Y+box.Height/2)
			dragSurface(c.surface, from, from.AddXY(float32(9*fit.scale), float32(5*fit.scale)))
			relayout(c.win)
		}
		save(view.name, c.win.Canvas().Capture())
		c.win.Close()
	}

	// A special left ninja (侠隐佐助 title on the synthetic HUD) whose bead
	// row the engine lowers: the left bead box shows on the visible beads and
	// is stored as the ordinary row; the window says so. Also at a larger
	// window size, where the bead row is easier to see.
	for _, view := range []struct {
		name string
		win  fyne.Size
	}{
		{"calibration-lowered-2400x1080", fyne.Size{}},
		{"calibration-lowered-2400x1080-large", fyne.NewSize(1600, 900)},
	} {
		s := newPreviewSession()
		w := fynetest.NewWindow(nil)
		s.win = w
		w.SetContent(s.overlayContent())
		s.openCalibration()
		c := s.calibrationWin
		if view.win != (fyne.Size{}) {
			c.win.Resize(view.win)
		}
		img, res := calibrationPreviewFrame(image.Pt(2400, 1080), nil, previewXiayin)
		s.calibrationFeed.offer(frame.Frame{Img: img, Fighting: true, LayoutProfile: res.LayoutProfile, Geometry: res.Geometry,
			LeftNinja: res.LeftNinja, RightNinja: res.RightNinja}, time.Now())
		relayout(c.win)
		save(view.name, c.win.Canvas().Capture())
		c.win.Close()
	}
}
