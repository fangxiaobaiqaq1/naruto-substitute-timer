package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	fynetest "fyne.io/fyne/v2/test"

	timerapp "narutotimer/internal/app"
	"narutotimer/internal/config"
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
	}
}
