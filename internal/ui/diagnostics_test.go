package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"

	"narutotimer/internal/config"
	"narutotimer/internal/diagnostics"
	"narutotimer/internal/frame"
)

func tracingFrame(at time.Time) frame.Frame {
	return frame.Frame{Img: image.NewRGBA(image.Rect(0, 0, 400, 250)),
		CaptureStarted: at, CapturedAt: at.Add(time.Millisecond),
		AnalysisStarted: at.Add(2 * time.Millisecond), AnalyzedAt: at.Add(3 * time.Millisecond),
		Sequence: 1, Scene: "fight", Fighting: true, LayoutProfile: "duel", Beads: testBeads(4, 4)}
}

func TestDiagnosticDrawRequiresBothUIComponentsAndRealCallback(t *testing.T) {
	r, err := diagnostics.New(diagnostics.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	s := &session{}
	at := time.Now().Add(-time.Second)
	a := traceRef{r, r.Observe(tracingFrame(at), at.Add(4*time.Millisecond))}
	b := traceRef{r, r.Observe(tracingFrame(at.Add(20*time.Millisecond)), at.Add(24*time.Millisecond))}
	a.mark("queued", at.Add(5*time.Millisecond))
	b.mark("queued", at.Add(25*time.Millisecond))
	s.traceApplied(a, true)
	s.traceApplied(b, false)
	s.traceDrawn(time.Now(), time.Now())
	if r.Snapshot().CompleteFrames != 0 {
		t.Fatal("mismatched clock/overlay revisions were counted as drawn")
	}
	s.traceApplied(b, true)
	if r.Snapshot().CompleteFrames != 0 {
		t.Fatal("Refresh or UI apply was counted as a real draw")
	}
	s.traceDrawn(time.Now(), time.Now())
	if r.Snapshot().CompleteFrames != 1 {
		t.Fatal("matching real draw did not complete exactly one trace")
	}
	s.traceDrawn(time.Now(), time.Now())
	if r.Snapshot().CompleteFrames != 1 {
		t.Fatal("timer redraw counted the same frame twice")
	}
}

func TestDiagnosticLifecycleAndSettingsControl(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := &session{cfg: config.Default(), done: make(chan struct{}), cfgPath: filepath.Join(t.TempDir(), "config.json")}
	s.cfg.Debug.Directory = t.TempDir()
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.win.SetPadded(false)
	s.win.Resize(fyne.NewSize(280, 140))
	s.installDrawTrace()
	if s.drawTraceAvailable {
		t.Fatal("software test driver claimed native swap timestamps")
	}
	entry := overlayButton(s.win.Content(), "诊断")
	if entry == nil {
		t.Fatal("diagnostics entry missing from overlay")
	}
	position, found := overlayObjectPosition(s.win.Content(), entry, fyne.NewPos(0, 0))
	if !found || position.X < 0 || position.Y < 0 || position.X+entry.Size().Width > 280 || position.Y+entry.Size().Height > 140 {
		t.Fatalf("diagnostics entry is outside the visible overlay: %v %v", position, entry.Size())
	}
	entry.OnTapped()
	preview := s.diagnosticWindow
	if preview == nil {
		t.Fatal("overlay action did not open diagnostics window")
	}
	t.Cleanup(func() {
		if s.diagnosticWindow != nil {
			s.diagnosticWindow.Close()
		}
		if s.settings != nil {
			s.settings.Close()
		}
	})
	s.openSettings()
	settingsEntry := overlayButton(s.settings.Content(), "采集与延迟诊断 / 原帧录制")
	if settingsEntry == nil {
		t.Fatal("fixed diagnostics entry missing from settings")
	}
	settingsEntry.OnTapped()
	if s.diagnosticWindow != preview {
		t.Fatal("settings created a second diagnostics control window")
	}
	s.settings.Close()
	if s.diagnosticLabel == nil {
		t.Fatal("closing settings detached diagnostics status")
	}
	if output := os.Getenv("TIMER_UI_PREVIEW_DIR"); output != "" {
		if err := os.MkdirAll(output, 0755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(output, "diagnostics-controls.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, preview.Canvas().Capture())
		closeErr := file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	start := overlayButton(preview.Content(), "开始诊断")
	if start == nil {
		t.Fatal("missing diagnostics start control")
	}
	start.OnTapped()
	first := s.currentDiagnostics()
	if first == nil {
		t.Fatal("start control did not enable collection")
	}
	s.provider = func() frame.Frame { return tracingFrame(time.Now().Add(-time.Second)) }
	s.captureOnce()
	if first.Snapshot().Attempts != 1 {
		t.Fatal("capture was not logged")
	}
	preview.Close()
	if s.currentDiagnostics() != first || first.Snapshot().Closed {
		t.Fatal("closing the controls stopped the diagnostic session")
	}
	entry.OnTapped()
	start = overlayButton(s.diagnosticWindow.Content(), "停止并生成报告")
	if start == nil {
		t.Fatal("reopening diagnostics lost the active session controls")
	}
	start.OnTapped()
	s.diagnosticWriters.Wait()
	if s.currentDiagnostics() != nil || !first.Snapshot().Closed {
		t.Fatal("stop did not drain recorder")
	}
	start.OnTapped()
	second := s.currentDiagnostics()
	if second == nil || first.Snapshot().Directory == second.Snapshot().Directory {
		t.Fatal("new recording reused old session")
	}
	s.stop()
	s.diagnosticWriters.Wait()
	if !second.Snapshot().Closed {
		t.Fatal("app shutdown did not close recorder")
	}
}
