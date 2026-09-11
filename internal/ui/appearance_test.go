package ui

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	fynetest "fyne.io/fyne/v2/test"

	"narutotimer/internal/config"
	"narutotimer/internal/ninja"
)

func TestOverlayTextScaleAppliesToSingleAndDualClocksWithoutAccumulating(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	cfg := config.Default()
	cfg.UI.FontScale = 1.6
	s := &session{cfg: cfg, side: "left"}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	if got, want := s.cd.TextSize, overlayTextSize(44, 1.6); got != want {
		t.Fatalf("single clock size = %v, want %v", got, want)
	}
	at := time.Now()
	s.right.Observe(4, true, at.Add(-time.Millisecond), s.cooldown(), 1)
	s.right.Observe(3, true, at, s.cooldown(), 1)
	s.cfg.UI.NinjaQuery = ninja.FifthMizukage
	s.refreshClockAt(at)
	if got, want := s.cd.TextSize, overlayTextSize(32, 1.6); got != want {
		t.Fatalf("dual clock size = %v, want %v", got, want)
	}
	s.cfg.UI.NinjaQuery = ""
	s.refreshClockAt(at.Add(time.Second))
	if got, want := s.cd.TextSize, overlayTextSize(44, 1.6); got != want {
		t.Fatalf("single clock after dual = %v, want %v", got, want)
	}
	s.applyOverlayFontScale(1.6)
	if got, want := s.cd.TextSize, overlayTextSize(44, 1.6); got != want {
		t.Fatalf("reapplying scale accumulated: %v, want %v", got, want)
	}
}

func TestAppearancePreviewPersistsOnlyOnSaveAndReportsNativeErrors(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	cfg := config.Default()
	path := filepath.Join(t.TempDir(), "config.json")
	var applied []float64
	s := &session{cfg: cfg, cfgPath: path, overlayOpacity: func(_ fyne.Window, value float64) error {
		applied = append(applied, value)
		return nil
	}}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	if err := s.previewAppearance(0.65, 1.3); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0] != 0.65 {
		t.Fatalf("native opacity values = %v", applied)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("preview unexpectedly persisted configuration")
	}
	if err := s.saveAppearance(); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(path)
	if err != nil || stored.UI.WindowOpacity != 0.65 || stored.UI.FontScale != 1.3 {
		t.Fatalf("stored appearance = %+v, %v", stored.UI, err)
	}

	s.overlayOpacity = func(fyne.Window, float64) error { return errors.New("native unavailable") }
	if err := s.previewAppearance(0.70, 1.2); err == nil || s.appearanceStatus() == "" {
		t.Fatalf("native error was not exposed: err=%v status=%q", err, s.appearanceStatus())
	}
}

func TestAppearanceRejectsUnsupportedRanges(t *testing.T) {
	for _, tc := range [][2]float64{{0.39, 1}, {1.01, 1}, {1, 0.79}, {1, 1.61}} {
		if err := validOverlayAppearance(tc[0], tc[1]); err == nil {
			t.Fatal("invalid appearance accepted", tc)
		}
	}
}

func TestScaledOverlayFitsSingleAndDualDisplays(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	for _, scale := range []float64{0.8, 1, 1.6} {
		t.Run("scale", func(t *testing.T) {
			cfg := config.Default()
			cfg.UI.FontScale = scale
			s := &session{cfg: cfg, side: "left", scene: "fight", fighting: true, beads: testBeads(2, 2)}
			s.win = fynetest.NewTempWindow(t, s.overlayContent())
			s.win.SetPadded(false)
			at := time.Now()
			s.right.Observe(4, true, at.Add(-time.Millisecond), s.cooldown(), 1)
			s.right.Observe(3, true, at, s.cooldown(), 1)
			s.cfg.UI.NinjaQuery = ninja.FifthMizukage
			s.refreshClockAt(at)
			fitNativeOverlayPreview(s.win)
			bounds := s.win.Canvas().Capture().Bounds()
			for _, label := range []*canvas.Text{s.tag, s.primaryLabel, s.cd, s.alternateLabel, s.altCD, s.eventTag, s.info} {
				pos, found := overlayObjectPosition(s.win.Content(), label, fyne.NewPos(0, 0))
				if !found {
					t.Fatalf("scaled text %q missing", label.Text)
				}
				size := label.MinSize()
				if pos.X < 0 || pos.Y < 0 || pos.X+size.Width > float32(bounds.Dx()) || pos.Y+size.Height > float32(bounds.Dy()) {
					t.Fatalf("scale %.1f clips %q: pos=%v size=%v canvas=%v", scale, label.Text, pos, size, bounds)
				}
			}
		})
	}
}
