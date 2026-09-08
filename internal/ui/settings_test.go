package ui

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/identity"
)

func settingsRadio(root fyne.CanvasObject) *widget.RadioGroup {
	if radio, ok := root.(*widget.RadioGroup); ok {
		return radio
	}
	if scroll, ok := root.(*container.Scroll); ok {
		return settingsRadio(scroll.Content)
	}
	if c, ok := root.(*fyne.Container); ok {
		for _, child := range c.Objects {
			if found := settingsRadio(child); found != nil {
				return found
			}
		}
	}
	return nil
}

func TestSettingsSideSelectionAppliesWithoutSave(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), remember: true}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.openSettings()
	t.Cleanup(func() {
		if s.settings != nil {
			s.settings.Close()
		}
	})
	radio := settingsRadio(s.settings.Content())
	if radio == nil {
		t.Fatal("missing side radio")
	}
	at := time.Now()
	s.left.Observe(4, true, at.Add(-2*time.Millisecond), 15*time.Second, 1)
	s.left.Observe(3, true, at.Add(-time.Millisecond), 15*time.Second, 1)
	s.left.Observe(2, true, at, 15*time.Second, 1)
	s.right.Observe(4, true, at.Add(-time.Millisecond), 10*time.Second, 1)
	s.right.Observe(3, true, at, 10*time.Second, 1)
	for _, tc := range []struct{ label, side, opponent, event string }{
		{"左边", "left", "对面·右 · 忍者未确认", "第 1 次"},
		{"右边", "right", "对面·左 · 忍者未确认", "第 2 次"},
		{"自动认边", "", "对面·待认边", "左2 / 右1"},
	} {
		radio.SetSelected(tc.label)
		if s.side != tc.side || s.tag.Text != tc.opponent || s.eventTag.Text != tc.event {
			t.Fatalf("select %s without Save: side=%q overlay=%q event=%q", tc.label, s.side, s.tag.Text, s.eventTag.Text)
		}
		if tc.side == "" && s.cd.Text != "—" {
			t.Fatalf("unknown-side selection kept a stale clock: %q", s.cd.Text)
		}
		cfg, err := config.Load(s.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		want := tc.side
		if want == "" {
			want = "auto"
		}
		if cfg.UI.PlayerSide != want {
			t.Fatalf("persisted %q, want %q", cfg.UI.PlayerSide, want)
		}
	}
	if !radio.Required {
		t.Fatal("clicking selected side must not clear the mode")
	}
}

func TestSelectingAutoDoesNotKeepManualSide(t *testing.T) {
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), side: "right", remember: true}
	s.cfg.UI.PlayerSide = "right"
	s.applySettings("白方小", "", "自动认边", true, false)
	if s.side != "" {
		t.Fatalf("auto still labels stale manual side %q as known", s.side)
	}
}

func TestUnrememberedManualSideIsNotSaved(t *testing.T) {
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json")}
	s.applySettings("白方小", "", "左边", false, false)
	if s.side != "left" {
		t.Fatalf("manual selection must work this session: %q", s.side)
	}
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.PlayerSide != "auto" || cfg.UI.RememberSide {
		t.Fatalf("unremembered side leaked into next launch: %+v", cfg.UI)
	}
}

func TestReturningToAutoUsesFreshRecognitionUnderManualLock(t *testing.T) {
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), side: "right", remember: true}
	s.cfg.UI.PlayerNames = []string{"白方小"}
	s.cfg.UI.PlayerSide = "right"
	s.maybeAutoSide(frame.Frame{Scene: "fight", PlayerSide: "left", PlayerName: "白方小"})
	if s.side != "right" {
		t.Fatal("automatic evidence overrode manual selection")
	}
	if err := s.selectSideMode("auto"); err != nil {
		t.Fatal(err)
	}
	if s.side != "left" {
		t.Fatalf("auto did not immediately use latest screenshot: %q", s.side)
	}
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.PlayerSide != "auto" {
		t.Fatalf("recognition persisted a manual lock: %q", cfg.UI.PlayerSide)
	}
}

func TestAutoDoesNotReuseStaleOrDifferentMatchIdentity(t *testing.T) {
	for _, expire := range []string{"time", "lobby", "result", "queue", "pick"} {
		t.Run(expire, func(t *testing.T) {
			s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), side: "right", remember: true}
			s.cfg.UI.PlayerNames = []string{"白方小"}
			s.cfg.UI.PlayerSide = "right"
			s.maybeAutoSide(frame.Frame{Scene: "fight", PlayerSide: "left", PlayerName: "白方小"})
			if expire == "time" {
				s.autoIdentityAt = time.Now().Add(-3 * time.Second)
			} else {
				s.maybeAutoSide(frame.Frame{Scene: expire})
			}
			if err := s.selectSideMode("auto"); err != nil {
				t.Fatal(err)
			}
			if s.side != "" {
				t.Fatalf("%s retained stale side: %q", expire, s.side)
			}
		})
	}
}

func TestAccountChangeInvalidatesAutoIdentity(t *testing.T) {
	t.Cleanup(func() { identity.SetMineNames(config.Default().UI.PlayerNames) })
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), remember: true}
	s.maybeAutoSide(frame.Frame{Scene: "fight", PlayerSide: "left", PlayerName: "白方小"})
	if err := s.applySettings("新账号", "", "自动认边", true, false); err != nil {
		t.Fatal(err)
	}
	if s.side != "" {
		t.Fatal("account change kept old identity")
	}
	s.maybeAutoSide(frame.Frame{Scene: "fight", PlayerSide: "left", PlayerName: "白方小"})
	if s.side != "" {
		t.Fatal("in-flight old-account frame restored stale identity")
	}
}

func TestRestoreSideHonorsRememberSetting(t *testing.T) {
	for _, remember := range []bool{false, true} {
		s := &session{cfg: config.Default()}
		s.cfg.UI.PlayerSide, s.cfg.UI.RememberSide = "right", remember
		s.restoreSide()
		if remember && s.side != "right" {
			t.Fatal("remembered manual side not restored")
		}
		if !remember && (s.side != "" || s.cfg.UI.PlayerSide != "auto") {
			t.Fatal("unremembered lock restored")
		}
	}
}

func TestSwapKeepsOpenAndReopenedSettingsInSync(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), side: "left", remember: true}
	s.cfg.UI.PlayerSide = "left"
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.openSettings()
	t.Cleanup(func() {
		if s.settings != nil {
			s.settings.Close()
		}
	})
	s.swapSide()
	if got := settingsRadio(s.settings.Content()).Selected; got != "右边" {
		t.Fatalf("open settings not synced: %q", got)
	}
	s.settings.Hide()
	s.swapSide()
	s.openSettings()
	if got := settingsRadio(s.settings.Content()).Selected; got != "左边" {
		t.Fatalf("reopened settings stale: %q", got)
	}
}

func TestUnknownSideDoesNotPresentOwnClockAsOpponent(t *testing.T) {
	s := &session{}
	at := time.Now()
	s.left.Observe(4, true, at, 15*time.Second, 1)
	s.left.Observe(3, true, at.Add(time.Second), 15*time.Second, 1)
	if got := s.oppRemaining(at.Add(time.Second)); len(got) != 0 {
		t.Fatalf("unknown side guessed opponent from a single clock: %v", got)
	}
}

func TestReportedScreenshotFlowsThroughEngineToOverlay(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Cleanup(func() { identity.SetMineNames(config.Default().UI.PlayerNames) })
	cfg, err := config.Load(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.PlayerSide = "auto"
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(root, "inbox/regressions/duel-player-left-20260906.png"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := png.Decode(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	now := time.Now()
	f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "user-screenshot")
	if !f.Fighting || f.Hold || f.PlayerSide != "left" {
		t.Fatalf("recognition pipeline: fight=%v hold=%v player=%q scene=%q", f.Fighting, f.Hold, f.PlayerSide, f.Scene)
	}
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := &session{cfg: cfg, cfgPath: filepath.Join(t.TempDir(), "config.json"), provider: func() frame.Frame { return f }, remember: true, topmost: cfg.UI.AlwaysOnTop}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.win.SetPadded(false)
	s.win.Resize(fyne.NewSize(280, 140))
	s.captureOnce()
	if s.side != "left" || s.tag.Text != "对面·右 · 忍者未确认" {
		t.Fatalf("reported reversal: side=%q label=%q", s.side, s.tag.Text)
	}
	// Separate left/right clocks must follow the recognized opponent, not merely
	// invert the label on the same old clock.
	s.left.Reset()
	s.right.Reset()
	s.left.Observe(4, true, now.Add(-50*time.Millisecond), 15*time.Second, 1)
	s.left.Observe(3, true, now, 15*time.Second, 1)
	s.right.Observe(4, true, now.Add(-50*time.Millisecond), 10*time.Second, 1)
	s.right.Observe(3, true, now, 10*time.Second, 1)
	s.refreshClockAt(now)
	fitNativeOverlayPreview(s.win)
	if s.cd.Text != "10.0" {
		t.Fatalf("label fixed but clock still used my side: %q", s.cd.Text)
	}
	clockPos, _ := overlayObjectPosition(s.win.Content(), s.cd, fyne.NewPos(0, 0))
	numberPos, _ := overlayObjectPosition(s.win.Content(), s.eventTag, fyne.NewPos(0, 0))
	if numberPos.X < clockPos.X+s.cd.MinSize().Width {
		t.Fatalf("live clock overlapped the event badge after growing from an unknown-side dash: clock=%v width=%v badge=%v", clockPos, s.cd.MinSize().Width, numberPos)
	}
	if output := os.Getenv("TIMER_UI_PREVIEW_DIR"); output != "" {
		if err := os.MkdirAll(output, 0755); err != nil {
			t.Fatal(err)
		}
		savePreview := func(name string, w fyne.Window) {
			file, err := os.Create(filepath.Join(output, name))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, w.Canvas().Capture())
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		savePreview("side-fix-overlay.png", s.win)
		s.openSettings()
		savePreview("side-fix-settings.png", s.settings)
		s.settings.Close()
	}
}

func TestRecognitionCannotOverwriteSavedManualMode(t *testing.T) {
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), remember: true}
	if err := s.selectSideMode("auto"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			s.mu.Lock()
			s.maybeAutoSide(frame.Frame{Scene: "fight", PlayerSide: "left", PlayerName: "白方小"})
			s.mu.Unlock()
		}
	})
	for range 20 {
		if err := s.selectSideMode("right"); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if s.side != "right" || cfg.UI.PlayerSide != "right" {
		t.Fatalf("background recognition overwrote selection: runtime=%q persisted=%q", s.side, cfg.UI.PlayerSide)
	}
}

func TestSettingsSaveFailureIsReturnedWithoutUndoingSelection(t *testing.T) {
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "missing", "config.json"), remember: true}
	if err := s.selectSideMode("left"); err == nil {
		t.Fatal("save failure was silently ignored")
	}
	if s.side != "left" {
		t.Fatal("a save failure prevented immediate session selection")
	}
}
