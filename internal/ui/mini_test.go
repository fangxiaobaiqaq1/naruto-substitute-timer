package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"narutotimer/internal/config"
	"narutotimer/internal/ninja"
)

func newMiniTestSession(t *testing.T, cfg config.Config) (*session, *[]float64) {
	t.Helper()
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := newSession(cfg, nil, nil)
	s.cfgPath = filepath.Join(t.TempDir(), "config.json")
	var applied []float64
	s.overlayOpacity = func(_ fyne.Window, v float64) error { applied = append(applied, v); return nil }
	s.floatingHook = func(bool) error { return nil }
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.win.Resize(fyne.NewSize(320, 160))
	return s, &applied
}

func observeBothSides(s *session, at time.Time) {
	s.left.Observe(4, true, at.Add(-16*time.Millisecond), s.cooldown(), 2)
	s.left.Observe(3, true, at, s.cooldown(), 2)
	s.left.Observe(3, true, at.Add(16*time.Millisecond), s.cooldown(), 2)
	s.right.Observe(4, true, at.Add(-16*time.Millisecond), s.cooldown(), 2)
	s.right.Observe(3, true, at, s.cooldown(), 2)
	s.right.Observe(3, true, at.Add(16*time.Millisecond), s.cooldown(), 2)
}

func applyDetachedClockAt(s *session, at time.Time) {
	s.refreshClockAt(at)
	s.applyPendingClock()
}

func newDetachedMiniTestSession(t *testing.T, cfg config.Config) *session {
	t.Helper()
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := newSession(cfg, nil, nil)
	s.cfgPath = filepath.Join(t.TempDir(), "config.json")
	s.overlayOpacity = func(fyne.Window, float64) error { return nil }
	s.floatingHook = func(bool) error { return nil }
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.setupDetachedMiniWindow(a)
	t.Cleanup(func() { s.miniWin.Close() })
	return s
}

func TestBothSideClocksShowAndUpdateInFullAndMini(t *testing.T) {
	for _, mode := range []string{"full", "mini"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.Default()
			cfg.UI.ShowBothSides, cfg.UI.OverlayMode = true, mode
			s, _ := newMiniTestSession(t, cfg)
			// The player side is unknown on purpose: both clocks still show.
			if !s.bothSideBox.Visible() || s.singleClockRow.Visible() {
				t.Fatalf("%s: both-side box visible=%v single=%v", mode, s.bothSideBox.Visible(), s.singleClockRow.Visible())
			}
			// nextTenthWait reads the real clock and adds a 3 ms margin; start the
			// events 50 ms ago so the next tenth is never a full 100 ms away.
			at := time.Now().Add(-50 * time.Millisecond)
			observeBothSides(s, at)
			s.refreshClockAt(at.Add(5 * time.Second))
			if s.leftBothCD.Text != "10.0" || s.rightBothCD.Text != "10.0" {
				t.Fatalf("%s: left %q right %q", mode, s.leftBothCD.Text, s.rightBothCD.Text)
			}
			if wait := s.nextTenthWait(); wait <= 0 || wait > 100*time.Millisecond {
				t.Fatalf("%s: both-side wait %v", mode, wait)
			}
		})
	}
}

func TestMiniHidesChromeAndHoverShowsControls(t *testing.T) {
	cfg := config.Default()
	cfg.UI.OverlayMode = "mini"
	s, applied := newMiniTestSession(t, cfg)
	s.applyInitialWindowAppearance()
	if len(*applied) == 0 || (*applied)[len(*applied)-1] != cfg.UI.MiniOpacity {
		t.Fatalf("startup mini opacity = %v", *applied)
	}
	for name, obj := range map[string]fyne.CanvasObject{"tag": s.tag, "info": s.info, "eventTag": s.eventTag, "footer": s.footer} {
		if obj.Visible() {
			t.Fatalf("%s visible in mini", name)
		}
	}
	if s.miniControls.Visible() {
		t.Fatal("controls visible before hover")
	}
	// Run the delayed hide check on this goroutine once the hand-over is done.
	var pending []func()
	s.hoverSettleHook = func(check func()) { pending = append(pending, check) }
	settle := func() {
		for _, check := range pending {
			check()
		}
		pending = nil
	}
	s.miniSurface.MouseIn(nil)
	if !s.miniControls.Visible() {
		t.Fatal("hover did not show controls")
	}
	// Surface hands over to a button: controls must stay up past the settle.
	s.miniSurface.MouseOut()
	s.miniBothButton.MouseIn(nil)
	settle()
	if !s.miniControls.Visible() {
		t.Fatal("controls hid while pointer rests on a button")
	}
	s.miniBothButton.OnTapped()
	if !s.bothSideBox.Visible() || s.miniBothButton.Text != "单边计时" {
		t.Fatalf("toggle both: visible=%v label=%q", s.bothSideBox.Visible(), s.miniBothButton.Text)
	}
	s.miniBothButton.MouseOut()
	settle()
	if s.miniControls.Visible() {
		t.Fatal("MouseOut did not hide controls")
	}
}

func TestMiniExitReturnsToFullAndPersists(t *testing.T) {
	cfg := config.Default()
	cfg.UI.WindowOpacity, cfg.UI.MiniFloating = 0.9, true
	s, _ := newMiniTestSession(t, cfg)
	var applied []float64
	var floating []bool
	s.overlayOpacity = func(_ fyne.Window, v float64) error { applied = append(applied, v); return nil }
	s.floatingHook = func(on bool) error { floating = append(floating, on); return nil }
	if s.miniLayer.Visible() {
		t.Fatal("mini layer visible in full mode")
	}
	s.toggleMini()
	if !s.miniLayer.Visible() || s.tag.Visible() {
		t.Fatal("Ctrl+M path did not enter mini")
	}
	var moved [2]int32
	s.moveHook = func(dx, dy int32) { moved = [2]int32{dx, dy} }
	s.miniSurface.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(12, -4)})
	if moved == ([2]int32{}) {
		t.Fatal("floating drag did not move the window")
	}
	s.miniSurface.MouseIn(nil)
	exit := s.miniControls.Objects[1].(*fyne.Container).Objects[3].(*miniButton)
	exit.OnTapped()
	if s.isMini() || s.miniLayer.Visible() || !s.tag.Visible() || !s.footer.Visible() {
		t.Fatal("exit did not return to full")
	}
	if len(applied) < 2 || applied[0] != cfg.UI.MiniOpacity || applied[len(applied)-1] != 0.9 {
		t.Fatalf("opacity sequence = %v", applied)
	}
	if len(floating) != 2 || !floating[0] || floating[1] {
		t.Fatalf("floating sequence = %v", floating)
	}
	stored, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.UI.OverlayMode != "full" {
		t.Fatalf("persisted overlay mode = %q", stored.UI.OverlayMode)
	}
}

func TestMiniOpacitySettingPersistsAndValidates(t *testing.T) {
	cfg := config.Default()
	cfg.UI.OverlayMode = "mini"
	s, _ := newMiniTestSession(t, cfg)
	if err := s.setMiniOpacity(0.1, true); err == nil {
		t.Fatal("out-of-range mini opacity accepted")
	}
	if err := s.setMiniOpacity(0.5, true); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.UI.MiniOpacity != 0.5 || stored.UI.OverlayMode != "mini" {
		t.Fatalf("stored = %+v", stored.UI)
	}
}

func TestDetachedMiniWindowUsesAndroidPillAndMainSettings(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	cfg := config.Default()
	cfg.UI.MiniAutoScale = true
	s := newSession(cfg, nil, nil)
	s.cfgPath = filepath.Join(t.TempDir(), "config.json")
	s.overlayOpacity = func(fyne.Window, float64) error { return nil }
	s.floatingHook = func(bool) error { return nil }
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.setupDetachedMiniWindow(a)
	t.Cleanup(func() { s.miniWin.Close() })
	if s.miniWin.FixedSize() {
		t.Fatal("detached mini window is still fixed-size")
	}

	s.side = "left"
	s.applyDetachedMiniClock("12.4", tagMine)
	if got := s.miniStatus.Text; got != "12.4" {
		t.Fatalf("mini timer text = %q", got)
	}
	if got, pill := s.miniWin.Canvas().Size(), s.miniPillBox.MinSize(); got != pill.Add(fyne.NewSize(miniGrabMargin, miniGrabMargin)) {
		t.Fatalf("window %v is not the pill %v plus the grab margin", got, pill)
	}
	if s.miniWindowControls.Visible() {
		t.Fatal("mini controls visible before tapping the pill")
	}
	if output := os.Getenv("TIMER_UI_PREVIEW_DIR"); output != "" {
		if err := os.MkdirAll(output, 0755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(output, "detached-mini-collapsed.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, s.miniWin.Canvas().Capture()); err != nil {
			file.Close()
			t.Fatal(err)
		}
		file.Close()
	}
	s.miniWindowSurface.Tapped(nil)
	if !s.miniWindowControls.Visible() {
		t.Fatal("tapping the pill did not open controls")
	}
	if output := os.Getenv("TIMER_UI_PREVIEW_DIR"); output != "" {
		file, err := os.Create(filepath.Join(output, "detached-mini-expanded.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, s.miniWin.Canvas().Capture()); err != nil {
			file.Close()
			t.Fatal(err)
		}
		file.Close()
	}
	buttons := s.miniWindowControls.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container)
	buttons.Objects[0].(*scaledMiniButton).OnTapped()
	if s.settings == nil || s.settings.Title() != "替身设置" {
		t.Fatal("mini settings action did not open the main settings window")
	}
	s.settings.Close()
}

func TestDetachedMiniWindowIsThePillAndResizesFromEdges(t *testing.T) {
	s := newDetachedMiniTestSession(t, config.Default())
	s.applyDetachedMiniClock("12.4", tagMine)
	margin := fyne.NewSize(miniGrabMargin, miniGrabMargin)
	pill := func() fyne.Size { return s.miniPill.Size() }
	fit := pill()
	if fit != s.miniPillBox.MinSize() || s.miniWin.Canvas().Size() != fit.Add(margin) {
		t.Fatalf("window %v, pill %v: the window must be the pill plus the grab margin", s.miniWin.Canvas().Size(), fit)
	}
	base := s.miniStatus.TextSize
	right := s.miniEdges.Objects[0].(*fyne.Container).Objects[1].(*miniEdge)
	bottom := s.miniEdges.Objects[1].(*fyne.Container).Objects[1].(*miniEdge)
	drag := func(e *miniEdge, x, y float32) {
		e.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: fyne.NewPos(x, y)}})
		e.DragEnd()
	}

	// Fyne picks the drag target at the pointer's previous position, which an
	// outward drag from the pill's last pixel can move up to its 2-unit drag
	// threshold past the pill: the strips must still be there.
	moved := false
	s.moveHook = func(int32, int32) { moved = true }
	for _, at := range []fyne.Position{{X: fit.Width + 2, Y: fit.Height / 2}, {X: fit.Width / 2, Y: fit.Height + 2}} {
		s.cfg.UI.MiniSize = nil
		fynetest.Drag(s.miniWin.Canvas(), at, 1, 1)
		if s.cfg.UI.MiniSize == nil || moved {
			t.Fatalf("drag in the grab margin at %v missed the edge strip (moved window: %v)", at, moved)
		}
	}
	s.cfg.UI.MiniSize = nil
	s.fitDetachedMiniWindow()
	if pill() != fit || s.miniStatus.TextSize != base {
		t.Fatalf("reset: pill %v text %v", pill(), s.miniStatus.TextSize)
	}

	// Longer: only the width follows the pointer.
	drag(right, fit.Width+100, 5)
	if got := pill(); got.Width != fit.Width+100+miniEdgeSize/2 || got.Height != fit.Height || s.miniStatus.TextSize != base {
		t.Fatalf("right edge: pill %v text %v (fit %v text %v)", got, s.miniStatus.TextSize, fit, base)
	}
	// Taller: the clock grows with the height.
	drag(bottom, 5, fit.Height*2)
	tall := pill()
	if tall.Height != fit.Height*2+miniEdgeSize/2 || s.miniStatus.TextSize < base*1.8 {
		t.Fatalf("bottom edge: pill %v text %v", tall, s.miniStatus.TextSize)
	}
	if s.miniWin.Canvas().Size() != tall.Add(margin) {
		t.Fatalf("window %v does not hug the pill %v", s.miniWin.Canvas().Size(), tall)
	}
	stored, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.UI.MiniSize; got == nil || got.Width != tall.Width || got.Height != tall.Height {
		t.Fatalf("stored mini size = %+v, shown %v", got, tall)
	}

	// Clock text changes and the action row keep the dragged pill.
	s.applyDetachedMiniClock("待机", tagIdle)
	if got := pill(); got != tall {
		t.Fatalf("text change resized the pill: %v -> %v", tall, got)
	}
	s.miniWindowSurface.Tapped(nil)
	if got := pill(); got.Height != tall.Height || s.miniWin.Canvas().Size().Height <= tall.Height+miniGrabMargin {
		t.Fatalf("open row: pill %v window %v", got, s.miniWin.Canvas().Size())
	}
	s.miniWindowSurface.Tapped(nil)
	if got := s.miniWin.Canvas().Size(); got != tall.Add(margin) {
		t.Fatalf("closing the row did not restore the pill: %v", got)
	}

	// Shrinking past the text stops at the text, and is stored as shown.
	drag(right, 4, 0)
	if got := pill(); got.Width <= 4 || got.Width != s.miniPillBox.MinSize().Width {
		t.Fatalf("narrowest pill %v", got)
	}
	if got := s.cfg.UI.MiniSize; got.Width != pill().Width {
		t.Fatalf("stored width %v below the shown pill", got.Width)
	}

	// Fitting the text again (settings checkbox) hides the edges.
	s.cfg.UI.MiniAutoScale = true
	s.applyMiniContentScale()
	if s.miniEdges.Visible() || s.miniStatus.TextSize != base || pill() != s.miniPillBox.MinSize() {
		t.Fatalf("auto fit: edges %v text %v pill %v", s.miniEdges.Visible(), s.miniStatus.TextSize, pill())
	}
}

func TestSwapSideCyclesThroughAutoMode(t *testing.T) {
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), side: "left", remember: true}
	s.cfg.UI.PlayerSide = "left"
	s.swapSide()
	if s.side != "right" || s.cfg.UI.PlayerSide != "right" {
		t.Fatalf("left to right: side=%q mode=%q", s.side, s.cfg.UI.PlayerSide)
	}
	s.swapSide()
	if s.side != "" || s.cfg.UI.PlayerSide != "auto" {
		t.Fatalf("right to auto: side=%q mode=%q", s.side, s.cfg.UI.PlayerSide)
	}
	s.swapSide()
	if s.side != "left" || s.cfg.UI.PlayerSide != "left" {
		t.Fatalf("auto to left: side=%q mode=%q", s.side, s.cfg.UI.PlayerSide)
	}
}

func TestDetachedMiniShowsBothSideClockValues(t *testing.T) {
	cfg := config.Default()
	cfg.UI.ShowBothSides = true
	s := newDetachedMiniTestSession(t, cfg)

	at := time.Unix(1700000000, 0)
	observeBothSides(s, at)
	applyDetachedClockAt(s, at.Add(5*time.Second))
	text := s.miniStatus.Text
	if text != "左10.0 右10.0" {
		t.Fatalf("detached both-side text = %q", text)
	}
}

func TestDetachedMiniShowsFifthMizukageDualEstimates(t *testing.T) {
	cfg := config.Default()
	cfg.UI.NinjaQuery = ninja.FifthMizukage
	s := newDetachedMiniTestSession(t, cfg)

	s.side = "left"
	at := time.Unix(1700000000, 0)
	s.right.Observe(4, true, at.Add(-16*time.Millisecond), s.cooldown(), 2)
	s.right.Observe(3, true, at, s.cooldown(), 2)
	s.right.Observe(3, true, at.Add(16*time.Millisecond), s.cooldown(), 2)
	applyDetachedClockAt(s, at.Add(32*time.Millisecond))
	text := s.miniStatus.Text
	if text != "14.9 / 9.9" {
		t.Fatalf("detached dual text = %q", text)
	}
}

func TestDetachedMiniTextDoesNotExposeSide(t *testing.T) {
	s := newDetachedMiniTestSession(t, config.Default())

	at := time.Unix(1700000000, 0)
	observeBothSides(s, at)
	s.side = "left"
	applyDetachedClockAt(s, at.Add(5*time.Second))
	left := s.miniStatus.Text
	s.side = "right"
	applyDetachedClockAt(s, at.Add(5*time.Second))
	right := s.miniStatus.Text
	if left != right || left != "10.0" {
		t.Fatalf("side leaked into mini text: left=%q right=%q", left, right)
	}
}

func TestDetachedMiniUsesStoredFontScale(t *testing.T) {
	cfg := config.Default()
	cfg.UI.FontScale = 1.6
	cfg.UI.MiniAutoScale = true
	s := newDetachedMiniTestSession(t, cfg)
	if got, want := s.miniStatus.TextSize, overlayTextSize(miniClockText, cfg.UI.FontScale); got != want {
		t.Fatalf("detached mini font scale = %v, want %v", got, want)
	}
}

func TestDetachedMiniCtrlMShortcutTogglesFullWindow(t *testing.T) {
	s := newDetachedMiniTestSession(t, config.Default())
	s.toggleMini()
	if !s.isMini() {
		t.Fatal("Ctrl+M on detached mini did not enter mini mode")
	}
	s.toggleMini()
	if s.isMini() {
		t.Fatal("second Ctrl+M on detached mini did not return to full mode")
	}
}
