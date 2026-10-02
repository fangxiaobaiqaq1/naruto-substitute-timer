package ui

import (
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"narutotimer/internal/config"
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
			at := time.Now()
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
