package ui

import (
	fynetest "fyne.io/fyne/v2/test"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureSelectionAppliesAndPersistsWithoutRestart(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), done: make(chan struct{})}
	var selected config.MuMuCaptureConfig
	calls := 0
	s.captureControl = func(c config.MuMuCaptureConfig) uint64 { selected = c; calls++; return uint64(calls) }
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.openSettings()
	defer s.settings.Close()
	s.settingsTabs.SelectIndex(1)
	apply := overlayButton(s.settings.Content(), "使用选中的进程")
	if apply == nil {
		t.Fatal("missing apply")
	}
	apply.OnTapped()
	cfg, e := config.Load(s.cfgPath)
	if e != nil || cfg.Capture.MuMu.Selection != "auto" || calls != 1 || selected.Selection != "auto" || s.sourceRevision != 1 {
		t.Fatal(cfg.Capture.MuMu, calls, e)
	}
}
func TestStaleSelectedInstanceFrameCannotReachUIOrEventCounter(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), done: make(chan struct{}), sourceRevision: 2, side: "left"}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	old := tracingFrame(time.Now())
	old.SourceRevision = 1
	s.provider = func() frame.Frame { return old }
	s.captureOnce()
	if len(s.beads) != 0 || s.left.EventCount() != 0 || s.right.EventCount() != 0 {
		t.Fatal("old instance observation applied")
	}
	old.SourceRevision = 2
	s.captureOnce()
	if len(s.beads) == 0 {
		t.Fatal("current instance frame discarded")
	}
	_ = os.Remove(s.cfgPath)
}
