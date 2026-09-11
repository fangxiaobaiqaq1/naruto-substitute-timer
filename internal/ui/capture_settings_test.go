package ui

import (
	fynetest "fyne.io/fyne/v2/test"
	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"os"
	"path/filepath"
	"strings"
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

func TestCaptureStateTextSeparatesSavedAndAppliedTargets(t *testing.T) {
	saved := config.MuMuCaptureConfig{Selection: "manual", InstallDir: `D:\MuMu`, Instance: 0}
	state := frame.CaptureState{
		RequestedRevision: 4,
		AppliedRevision:   3,
		Requested:         config.MuMuCaptureConfig{Selection: "manual", InstallDir: `E:\MuMu`, Instance: 2},
		Applied:           saved,
		LastError:         "连接 MuMu 实例失败",
	}
	text := captureStateText(`C:\cfg\config.json`, saved, state)
	for _, want := range []string{"已保存：D:\\MuMu · 实例 0", "已请求应用：E:\\MuMu · 实例 2", "采集器已应用：D:\\MuMu · 实例 0", "正在切换", "最近连接/采集异常"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
}

func TestCaptureProbeSelectionUsesSoleAutomaticCandidate(t *testing.T) {
	items := []mumu.ProcessChoice{{Instance: mumu.Instance{Root: `E:\MuMu`, Index: 2, Name: "训练"}}}
	got, err := captureProbeSelection(0, items, "", "0", config.MuMuCaptureConfig{Selection: "auto", Package: "com.tencent.KiHan"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Selection != "manual" || got.InstallDir != `E:\MuMu` || got.Instance != 2 || got.Package != "com.tencent.KiHan" {
		t.Fatalf("unexpected probe target: %+v", got)
	}
}

func TestCaptureProbeSelectionRejectsAmbiguousAutomaticCandidates(t *testing.T) {
	items := []mumu.ProcessChoice{
		{Instance: mumu.Instance{Root: `D:\MuMu`, Index: 0}},
		{Instance: mumu.Instance{Root: `E:\MuMu`, Index: 0}},
	}
	if _, err := captureProbeSelection(0, items, "", "0", config.MuMuCaptureConfig{Selection: "auto"}); err == nil || !strings.Contains(err.Error(), "2 个 MuMu 实例") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}
