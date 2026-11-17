package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"

	"narutotimer/internal/config"
	"narutotimer/internal/diagnostics"
	"narutotimer/internal/frame"
)

type fakePerformanceRecorder struct {
	mu            sync.Mutex
	options       diagnostics.PerformanceOptions
	status        diagnostics.PerformanceStatus
	done          chan struct{}
	stopOnce      sync.Once
	stopGate      <-chan struct{}
	stopError     error
	exportGate    <-chan struct{}
	exportStarted chan struct{}
	exportActive  []bool
	frames        int
	lastFrame     frame.Frame // Img removed in ObserveFrame, like the real contract
	loops         int
	loopTotal     time.Duration
	loopPost      time.Duration
	size          image.Point
	poll          time.Duration
	full          bool
	queueWaits    []time.Duration
	marks         []string
	creates       atomic.Int32
}

func newFakePerformance() *fakePerformanceRecorder {
	return &fakePerformanceRecorder{done: make(chan struct{}), exportStarted: make(chan struct{}, 8)}
}

func (r *fakePerformanceRecorder) create(o diagnostics.PerformanceOptions) (performanceRecorder, error) {
	r.creates.Add(1)
	r.mu.Lock()
	r.options = o
	r.status = diagnostics.PerformanceStatus{Active: true, StartedAt: time.Now()}
	r.mu.Unlock()
	o.OnSample() // intentionally before the constructor returns
	return r, nil
}

func (r *fakePerformanceRecorder) Snapshot() diagnostics.PerformanceStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.status
	if s.Latest != nil {
		copy := *s.Latest
		copy.Values, copy.Unavailable = maps.Clone(copy.Values), maps.Clone(copy.Unavailable)
		s.Latest = &copy
	}
	s.ProfileStatus = maps.Clone(s.ProfileStatus)
	return s
}

func (r *fakePerformanceRecorder) Metadata() diagnostics.PerformanceMetadata {
	r.mu.Lock()
	defer r.mu.Unlock()
	return diagnostics.PerformanceMetadata{Version: "test", RootPID: 101, Settings: r.options.Settings,
		System:     diagnostics.PerformanceSystem{OS: "test", OSVersion: "test OS", LogicalCPUs: 8, TotalRAMBytes: 16 << 30},
		IntervalMS: 1000, MaxSamples: diagnostics.PerformanceMaxSamples, MaxBytes: diagnostics.PerformanceMaxBytes}
}

func (r *fakePerformanceRecorder) ObserveFrame(f frame.Frame, _ time.Time, poll time.Duration, full bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.status.Active {
		return
	}
	r.frames++
	if f.Img != nil {
		r.size = f.Img.Bounds().Size()
	}
	f.Img = nil
	r.lastFrame, r.poll, r.full = f, poll, full
}

func (r *fakePerformanceRecorder) ObserveLoop(total, post time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Active {
		r.loops, r.loopTotal, r.loopPost = r.loops+1, total, post
	}
}

func (r *fakePerformanceRecorder) ObserveUIQueue(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Active {
		r.queueWaits = append(r.queueWaits, d)
	}
}

func (r *fakePerformanceRecorder) Mark(label string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.status.Active {
		return errors.New("stopped")
	}
	r.marks = append(r.marks, label)
	return nil
}

func (r *fakePerformanceRecorder) Stop() error {
	r.stopOnce.Do(func() {
		if r.stopGate != nil {
			<-r.stopGate
		}
		r.mu.Lock()
		r.status.Active = false
		ended := time.Now()
		r.status.EndedAt = &ended
		if r.status.StopReason == "" {
			r.status.StopReason = "user_stopped"
		}
		if r.stopError != nil {
			r.status.LastError = r.stopError.Error()
		}
		notify := r.options.OnSample
		r.mu.Unlock()
		close(r.done)
		if notify != nil {
			notify()
		}
	})
	return r.stopError
}

func (r *fakePerformanceRecorder) Done() <-chan struct{} { return r.done }

func (r *fakePerformanceRecorder) Export(ctx context.Context) (string, error) {
	r.mu.Lock()
	r.exportActive = append(r.exportActive, r.status.Active)
	n := len(r.exportActive)
	r.mu.Unlock()
	r.exportStarted <- struct{}{}
	if r.exportGate != nil {
		select {
		case <-r.exportGate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return fmt.Sprintf("performance-%d.zip", n), nil
}

func (r *fakePerformanceRecorder) sample(sample diagnostics.PerformanceSample) {
	r.mu.Lock()
	r.status.Samples++
	r.status.Latest = &sample
	notify := r.options.OnSample
	r.mu.Unlock()
	if notify != nil {
		notify()
	}
}

func performanceTestSession(t *testing.T) (*session, *fakePerformanceRecorder, chan func()) {
	t.Helper()
	a := fynetest.NewApp()
	a.Settings().SetTheme(newChromaTheme())
	t.Cleanup(a.Quit)
	t.Setenv("NARUTO_DEBUG", "")
	cfg := config.Default()
	cfg.Debug.Directory, cfg.UI.AutoCheckUpdates = t.TempDir(), false
	// These tests open the settings window. The MuMu/雷电 tab starts an emulator
	// scan goroutine at build time whose completion races with the headless
	// test driver; the window-capture provider does not scan.
	cfg.Capture.Provider = "printwindow-fullcontent"
	s := newSession(cfg, nil, nil)
	s.cfgPath = filepath.Join(t.TempDir(), "config.json")
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.win.SetPadded(false)
	s.fitOverlayWindow()
	r := newFakePerformance()
	s.performanceNew = r.create
	posts := make(chan func(), 16)
	s.performancePost = func(fn func()) {
		select {
		case posts <- fn:
		default:
			panic("performance UI posts were not coalesced")
		}
	}
	s.performanceExported = func(fyne.Window, string) {}
	t.Cleanup(func() {
		s.stop()
		waitPerformanceWorkers(t, s)
		s.diagnosticWriters.Wait()
		for _, w := range []fyne.Window{s.diagnosticWindow, s.settings, s.miniWin} {
			if w != nil {
				w.Close()
			}
		}
	})
	return s, r, posts
}

func waitPerformanceWorkers(t *testing.T, s *session) {
	t.Helper()
	done := make(chan struct{})
	go func() { s.performanceWorkers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("performance workers did not exit")
	}
}

// Only the test/UI goroutine executes posted closures, unlike an immediate
// fake fyne.Do which would let workers mutate Fyne objects concurrently.
func pumpPerformance(t *testing.T, posts chan func(), ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case fn := <-posts:
			fn()
		default:
		}
		if ready() {
			return
		}
		select {
		case fn := <-posts:
			fn()
		case <-time.After(time.Millisecond):
		case <-deadline.C:
			t.Fatal("performance UI did not reach expected state")
		}
	}
}

func performancePhaseIs(s *session, phase performancePhase) bool {
	s.performanceMu.Lock()
	defer s.performanceMu.Unlock()
	return s.performanceCurrent != nil && s.performanceCurrent.phase == phase
}

func performanceTabSelected(s *session) bool {
	return s.settingsTabs != nil && s.settingsTabs.Selected() != nil && s.settingsTabs.Selected().Text == performanceTabTitle
}

// The performance frontend lives in 设置 → 性能: no separate window and no
// extra row on the compact overlay. Every entry point lands on that one tab.
func TestPerformanceLivesInSettingsTab(t *testing.T) {
	s, r, _ := performanceTestSession(t)
	s.win.Resize(fyne.NewSize(280, 140))
	fitNativeOverlayPreview(s.win)
	for _, name := range []string{"设置", "换边", "诊断", "关于"} {
		button := overlayButton(s.win.Content(), name)
		if button == nil {
			t.Fatalf("missing overlay button %s", name)
		}
		at, found := overlayObjectPosition(s.win.Content(), button, fyne.NewPos(0, 0))
		bounds := s.win.Canvas().Size()
		if !found || at.X < 0 || at.Y < 0 || at.X+button.Size().Width > bounds.Width || at.Y+button.Size().Height > bounds.Height {
			t.Fatalf("%s clipped: %v %v in %v", name, at, button.Size(), bounds)
		}
	}
	if overlayButton(s.win.Content(), "性能报告") != nil {
		t.Fatal("the overlay still carries a performance button")
	}
	s.openSettings()
	tabs := s.settingsTabs
	if len(tabs.Items) != 4 || tabs.Items[3].Text != performanceTabTitle || tabs.Items[2].Text != "关于与更新" {
		t.Fatalf("settings tabs changed unexpectedly: %d", len(tabs.Items))
	}
	if performanceTabSelected(s) || s.performanceView == nil || !s.performanceView.profiles.Checked {
		t.Fatal("settings must open on 常规 with the performance view built and debug data on by default")
	}
	windows := len(fyne.CurrentApp().Driver().AllWindows())
	s.openPerformance()
	if !performanceTabSelected(s) {
		t.Fatal("openPerformance did not select the tab")
	}
	// The About entry still lands on its own tab; rebuilding keeps the tab and
	// rebinds the view to the new widgets.
	s.openAbout()
	if tabs.Selected().Text != "关于与更新" {
		t.Fatal("about entry broke")
	}
	s.openPerformance()
	before := s.performanceView
	s.rebuildSettings()
	if s.performanceView == before || s.performanceView == nil || !performanceTabSelected(s) {
		t.Fatal("rebuild lost the performance tab or kept a stale view")
	}
	s.settingsTabs.SelectIndex(0)
	s.openDiagnostics()
	entry := overlayButton(s.diagnosticWindow.Content(), "卡顿 / 占用高？打开 设置 → 性能")
	if entry == nil {
		t.Fatal("diagnostics window lacks the pointer to the performance tab")
	}
	entry.OnTapped()
	if !performanceTabSelected(s) {
		t.Fatal("diagnostics entry did not open the tab")
	}
	// Mini mode reaches it through its Settings button.
	s.settings.Close()
	if s.performanceView != nil {
		t.Fatal("closing settings kept a view pointing at destroyed widgets")
	}
	s.setupDetachedMiniWindow(fyne.CurrentApp())
	s.miniWindowButtons[0].OnTapped()
	if s.settings == nil || s.performanceView == nil {
		t.Fatal("mini settings entry did not rebuild the performance view")
	}
	if got := len(fyne.CurrentApp().Driver().AllWindows()); got > windows+2 { // + diagnostics, + mini
		t.Fatalf("a separate performance window was created (%d -> %d)", windows, got)
	}
	if r.creates.Load() != 0 || s.currentDiagnostics() != nil {
		t.Fatal("navigation started recording")
	}
}

func TestPerformanceLifecycleCloseViewContinuesAndStopExports(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	s.openPerformance()
	s.performanceView.profiles.SetChecked(false)
	s.performanceView.start.OnTapped()
	pumpPerformance(t, posts, func() bool { return s.performanceButton.Text == "设置 · 监测中" })
	if s.currentDiagnostics() != nil || s.diagnosticLast != nil {
		t.Fatal("performance enabled image diagnostics")
	}
	r.mu.Lock()
	options := r.options
	r.mu.Unlock()
	if options.Root != s.cfg.Debug.Directory || options.Profiles {
		t.Fatalf("wrong root/profiles: %+v", options)
	}
	if s.startPerformance(true) {
		t.Fatal("second sampler admitted")
	}
	s.settings.Close()
	if s.performanceView != nil || s.settings != nil || !r.Snapshot().Active {
		t.Fatal("closing settings must drop the view and keep monitoring")
	}
	if s.performanceButton.Text != "设置 · 监测中" {
		t.Fatal("overlay lost the recording indicator")
	}
	s.openPerformance()
	if s.performanceView == nil || s.performanceView.stop.Disabled() || !s.performanceView.start.Disabled() {
		t.Fatal("reopened view lost active session")
	}
	s.performanceView.mark.OnTapped()
	pumpPerformance(t, posts, func() bool { return strings.Contains(s.performanceView.status.Text, "已标记卡顿") })
	r.mu.Lock()
	marks := append([]string(nil), r.marks...)
	r.mu.Unlock()
	if len(marks) != 1 || marks[0] != diagnostics.PerformanceUserMark+"：卡顿" {
		t.Fatalf("marks %v", marks)
	}
	var exported string
	var parent fyne.Window
	s.performanceExported = func(w fyne.Window, path string) { parent, exported = w, path }
	s.performanceView.stop.OnTapped()
	// The user closes Settings while the report is being written: the result
	// must still be announced, on the overlay window.
	s.settings.Close()
	pumpPerformance(t, posts, func() bool { return exported != "" })
	if parent != s.win {
		t.Fatal("report finished with Settings closed was not announced on the overlay")
	}
	s.openPerformance()
	if exported != "performance-1.zip" || s.performanceView.exportLabel.Text != exported {
		t.Fatalf("export path lost on reopen: %q / %q", exported, s.performanceView.exportLabel.Text)
	}
	if r.Snapshot().Active || s.performanceActive.Load() != nil || !performancePhaseIs(s, performanceStopped) {
		t.Fatal("stop-and-export did not finish Stop")
	}
	r.mu.Lock()
	active := append([]bool(nil), r.exportActive...)
	r.mu.Unlock()
	if len(active) != 1 || active[0] {
		t.Fatalf("final export happened before Stop: %v", active)
	}
	if s.performanceButton.Text != "设置" || s.performanceView.start.Disabled() {
		t.Fatal("stopped UI remained active")
	}
}

func TestPerformanceFrameHookOffIsInertAndRealFrameDetailIsRecorded(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	f := frame.Frame{Img: image.NewRGBA(image.Rect(0, 0, 400, 250)),
		CapturedAt: time.Now(), CaptureStarted: time.Now().Add(-time.Millisecond), Hold: true,
		Scene: "fight", CaptureMethod: `MuMu 实例 0 · C:\Program Files\MuMu`, Err: errors.New("采集超时，已丢弃迟到画面")}
	s.provider = func() frame.Frame { return f }
	s.captureOnce()
	if r.creates.Load() != 0 || s.performanceCurrent != nil || len(posts) != 0 {
		t.Fatal("off path created monitoring work")
	}
	s.cfg.Capture.MuMu.InstallDir = `D:\MuMu Player 12`
	if !s.startPerformance(false) {
		t.Fatal("start refused")
	}
	pumpPerformance(t, posts, func() bool { return s.performanceActive.Load() != nil && len(posts) == 0 })
	if err := s.startDiagnostics(false); err != nil {
		t.Fatal(err)
	}
	s.captureOnce()
	r.mu.Lock()
	frames, last, size, poll, full := r.frames, r.lastFrame, r.size, r.poll, r.full
	configJSON := string(r.options.Config)
	r.mu.Unlock()
	if frames != 1 || size != image.Pt(400, 250) || poll != 400*time.Millisecond || !full {
		t.Fatalf("observed %d %v %v full=%v", frames, size, poll, full)
	}
	// The report is local and opt-in: the real capture method, the real error
	// text and the effective config reach it unmodified. Pixels never do.
	if last.CaptureMethod != f.CaptureMethod || last.Err == nil || last.Err.Error() != "采集超时，已丢弃迟到画面" || last.Img != nil {
		t.Fatalf("frame detail lost: %q / %v", last.CaptureMethod, last.Err)
	}
	var cfg config.Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil || cfg.Capture.MuMu.InstallDir != `D:\MuMu Player 12` {
		t.Fatalf("effective config not attached: %v", err)
	}
	if len(posts) != 0 {
		t.Fatal("capture added a per-frame performance UI post")
	}
	if s.currentDiagnostics().Snapshot().Attempts != 1 {
		t.Fatal("full diagnostics did not run independently")
	}
	s.stopDiagnostics()
	s.diagnosticWriters.Wait()
	s.mu.Lock()
	s.fighting = true
	s.mu.Unlock()
	s.captureOnce()
	r.mu.Lock()
	if r.frames != 2 || r.full || r.poll != 16*time.Millisecond {
		t.Fatal("full-diagnostic off state or production 16ms fight poll missing")
	}
	r.mu.Unlock()
	s.stopPerformance(false)
	pumpPerformance(t, posts, func() bool { return performancePhaseIs(s, performanceStopped) })
	s.captureOnce()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frames != 2 {
		t.Fatal("stopped performance recorder received another frame")
	}
}

func TestPerformanceSamplePostsCoalesceAndUnavailableGPUIsNotZero(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	s.openPerformance()
	s.startPerformance(false)
	pumpPerformance(t, posts, func() bool { return s.performanceButton.Text == "设置 · 监测中" && len(posts) == 0 })
	for i := 1; i <= 100; i++ {
		r.sample(diagnostics.PerformanceSample{At: time.Now(), Values: map[string]float64{"tree_cpu_percent": float64(i), "tree_working_set_bytes": 16 << 20},
			Unavailable: map[string]string{"system_gpu_busiest_engine_percent": "PDH GPU counters unavailable"},
			Top: []diagnostics.PerformanceTopProcess{
				{PID: 101, Name: "timer-app.exe", Role: "self", CPUPercent: performanceTestFloat(3), WorkingSetBytes: 16 << 20},
				{PID: 500, Name: "MuMuVMMHeadless.exe", Role: "emulator", CPUPercent: performanceTestFloat(40), WorkingSetBytes: 5 << 30},
				{PID: 501, Name: "MuMuVMMHeadless.exe", Role: "emulator", CPUPercent: performanceTestFloat(5), WorkingSetBytes: 1 << 30}}})
	}
	// Wait for the monitor to prepare the newest state, without draining Fyne.
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.performanceMu.Lock()
		latest := s.performanceCurrent.status.Samples
		s.performanceMu.Unlock()
		if latest == 100 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("notifications were lost")
		}
		time.Sleep(time.Millisecond)
	}
	if len(posts) != 1 {
		t.Fatalf("queued %d UI tasks for 100 notifications", len(posts))
	}
	pumpPerformance(t, posts, func() bool { return s.performanceView.tiles[1].Text == "100%" })
	// No GPU data is a dash with the reason, never "0%".
	if s.performanceView.tiles[3].Text != "—" || !strings.Contains(s.performanceView.verdict.Text, "PDH GPU counters unavailable") {
		t.Fatalf("GPU misleading: %q / %q", s.performanceView.tiles[3].Text, s.performanceView.verdict.Text)
	}
	if top := s.performanceView.top.Text; !strings.Contains(top, "MuMuVMMHeadless.exe（模拟器） ×2") || !strings.Contains(top, "计时器（本程序）") ||
		strings.Index(top, "MuMu") > strings.Index(top, "计时器") {
		t.Fatalf("process list: %s", top)
	}
	// The long developer text is built only while its section is open, and
	// nothing is refreshed while another tab is showing.
	if s.performanceView.details.Text != "" {
		t.Fatal("developer details were rendered while collapsed")
	}
	s.performanceView.detailToggle.OnTapped()
	if !strings.Contains(s.performanceView.details.Text, "计时器 CPU（含子进程）：100.0%") {
		t.Fatalf("developer details: %s", s.performanceView.details.Text)
	}
	s.settingsTabs.SelectIndex(0)
	r.sample(diagnostics.PerformanceSample{At: time.Now(), Values: map[string]float64{"tree_cpu_percent": 7}})
	pumpPerformance(t, posts, func() bool {
		s.performanceMu.Lock()
		defer s.performanceMu.Unlock()
		return s.performanceCurrent.status.Samples == 101 && len(posts) == 0
	})
	if s.performanceView.tiles[1].Text != "100%" {
		t.Fatal("hidden tab was refreshed")
	}
	s.openPerformance()
	if s.performanceView.tiles[1].Text != "7%" {
		t.Fatal("tab did not catch up when shown")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queueWaits) == 0 || r.queueWaits[len(r.queueWaits)-1] < 0 {
		t.Fatal("UI queue probe missing")
	}
}

func TestPerformanceShutdownDuringStartStopsLateSamplerAndJoins(t *testing.T) {
	s, r, _ := performanceTestSession(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	s.performanceNew = func(o diagnostics.PerformanceOptions) (performanceRecorder, error) {
		close(entered)
		<-release
		return r.create(o)
	}
	if !s.startPerformance(true) {
		t.Fatal("start refused")
	}
	<-entered
	s.stop() // must return before constructor does
	if s.startPerformance(true) {
		t.Fatal("start admitted after exit")
	}
	joined := make(chan struct{})
	go func() { s.performanceWorkers.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("exit forgot the pending constructor")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("late constructor leaked a sampler")
	}
	select {
	case <-r.done:
	default:
		t.Fatal("late-created sampler was not stopped")
	}
	if s.performanceActive.Load() != nil {
		t.Fatal("late sampler published after exit")
	}
}

func TestPerformanceStopDuringSnapshotExportProducesFinalExport(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	r.exportGate = release
	s.startPerformance(false)
	pumpPerformance(t, posts, func() bool { return s.performanceActive.Load() != nil })
	if !s.exportPerformance() {
		t.Fatal("snapshot export refused")
	}
	<-r.exportStarted
	if s.exportPerformance() {
		t.Fatal("parallel duplicate export admitted")
	}
	if !s.stopPerformance(true) {
		t.Fatal("stop refused during snapshot export")
	}
	pumpPerformance(t, posts, func() bool { return performancePhaseIs(s, performanceStopped) })
	// Export remains blocked, but sampler cleanup is already complete.
	select {
	case <-r.done:
	default:
		t.Fatal("export delayed sampler Stop")
	}
	once.Do(func() { close(release) })
	pumpPerformance(t, posts, func() bool {
		s.performanceMu.Lock()
		defer s.performanceMu.Unlock()
		return s.performanceCurrent.exportPath == "performance-2.zip" && !s.performanceCurrent.exporting
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.exportActive) != 2 || !r.exportActive[0] || r.exportActive[1] {
		t.Fatalf("export boundaries %v", r.exportActive)
	}
}

func performanceTestFloat(v float64) *float64 { return &v }

// Quitting is how a session normally ends: an in-flight snapshot export is
// canceled, the recorder is stopped, and a final report is written before the
// process exits. No queued UI task touches controls afterwards.
func TestPerformanceExitCancelsSnapshotWritesFinalReportAndSkipsQueuedUI(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	gate := make(chan struct{}) // the snapshot export is released only by cancellation
	r.exportGate = gate
	s.openPerformance()
	s.startPerformance(false)
	pumpPerformance(t, posts, func() bool { return s.performanceButton.Text == "设置 · 监测中" })
	s.exportPerformance()
	<-r.exportStarted
	before := s.performanceView.status.Text
	s.stop()
	<-r.exportStarted // the exit export, after the canceled snapshot returned
	close(gate)
	waitPerformanceWorkers(t, s)
	r.mu.Lock()
	exports := append([]bool(nil), r.exportActive...)
	r.mu.Unlock()
	if len(exports) != 2 || !exports[0] || exports[1] {
		t.Fatalf("exit must write one final report after Stop: %v", exports)
	}
	for len(posts) > 0 {
		(<-posts)()
	}
	if s.performanceView.status.Text != before {
		t.Fatal("queued task touched controls after shutdown")
	}
	if s.performanceActive.Load() != nil || s.exportPerformance() || s.markPerformanceStall() {
		t.Fatal("new work after shutdown")
	}
}

func TestPerformanceAutomaticStopAndStartFailureAreVisible(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	s.openPerformance()
	s.startPerformance(false)
	pumpPerformance(t, posts, func() bool { return s.performanceButton.Text == "设置 · 监测中" })
	r.mu.Lock()
	r.status.StopReason = "sample_limit"
	r.mu.Unlock()
	_ = r.Stop()
	pumpPerformance(t, posts, func() bool {
		return strings.Contains(s.performanceView.status.Text, "6 小时") && !s.performanceView.start.Disabled()
	})
	if s.performanceActive.Load() != nil {
		t.Fatal("automatic stop left capture hook active")
	}
	s.performanceNew = func(diagnostics.PerformanceOptions) (performanceRecorder, error) {
		return nil, errors.New("collector unavailable")
	}
	s.startPerformance(false)
	pumpPerformance(t, posts, func() bool { return strings.Contains(s.performanceView.status.Text, "collector unavailable") })
	if s.performanceView.start.Disabled() || s.performanceActive.Load() != nil {
		t.Fatal("failed start cannot be retried")
	}
}

func TestPerformanceSlowStopErrorStillExportsSavedPrefix(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	r.stopGate, r.stopError = release, errors.New("saved prefix; final profile failed")
	s.openPerformance()
	s.startPerformance(true)
	pumpPerformance(t, posts, func() bool { return s.performanceActive.Load() != nil })
	if !s.stopPerformance(true) {
		t.Fatal("stop refused")
	}
	pumpPerformance(t, posts, func() bool { return strings.Contains(s.performanceView.status.Text, "正在后台停止") })
	if s.performanceActive.Load() != nil || s.startPerformance(true) {
		t.Fatal("slow Stop did not close intake")
	}
	select {
	case <-r.exportStarted:
		t.Fatal("export ran before final profiles completed")
	default:
	}
	once.Do(func() { close(release) })
	pumpPerformance(t, posts, func() bool { return s.performanceView.exportLabel.Text != "" })
	if !strings.Contains(s.performanceView.status.Text, "final profile failed") {
		t.Fatal("stop error was hidden")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.exportActive) != 1 || r.exportActive[0] {
		t.Fatalf("saved prefix not exported after Stop error: %v", r.exportActive)
	}
}

func TestPerformanceLiveReadingsAreExplicitAndPlain(t *testing.T) {
	if got := performanceMetricValue(nil, "tree_gpu_busiest_engine_percent", "%"); got != "—" {
		t.Fatalf("missing GPU: %q", got)
	}
	if got := performanceTileValues(nil); got != [4]string{"—", "—", "—", "—"} {
		t.Fatalf("empty tiles: %v", got)
	}
	sample := &diagnostics.PerformanceSample{Values: map[string]float64{"system_cpu_percent": 93, "tree_cpu_percent": 2.4,
		"system_memory_used_percent": 61, "system_gpu_busiest_engine_percent": 0, "tree_working_set_bytes": 120 << 20,
		"emulator_cpu_percent": 55, "emulator_working_set_bytes": 6 << 30}}
	if got := performanceTileValues(sample); got != [4]string{"93%", "2%", "61%", "0%"} { // A measured 0 is shown as 0.
		t.Fatalf("tiles: %v", got)
	}
	verdict := performanceVerdictText(&performanceUpdate{status: diagnostics.PerformanceStatus{Latest: sample}})
	for _, want := range []string{"计时器内存 120 MB", "模拟器 CPU 55%", "内存 6.0 GB", "卡顿主要不是计时器造成的"} {
		if !strings.Contains(verdict, want) {
			t.Errorf("verdict lacks %q: %s", want, verdict)
		}
	}
	status := performanceStatusText(&performanceUpdate{run: &performanceRun{}, phase: performanceRunning,
		status: diagnostics.PerformanceStatus{StartedAt: time.Unix(1000, 0)}}, time.Unix(1065, 0))
	if status != "监测中 · 已记录 1 分 05 秒" {
		t.Fatalf("status: %q", status)
	}
	if strings.Contains(performanceDetailText(&performanceUpdate{recorder: newFakePerformance(), status: diagnostics.PerformanceStatus{Latest: sample}}), "true") {
		t.Fatal("developer text prints raw booleans")
	}
}

// A session that ended without a report (the app was closed mid-recording by
// an older build, or crashed) can be turned into one from the tab.
func TestPerformancePreviousSessionExport(t *testing.T) {
	s, _, posts := performanceTestSession(t)
	s.openPerformance()
	if !s.exportPreviousPerformance() {
		t.Fatal("refused with nothing running")
	}
	pumpPerformance(t, posts, func() bool { return strings.Contains(s.performanceView.status.Text, "还没有任何性能记录") })
	real, err := diagnostics.NewPerformance(diagnostics.PerformanceOptions{Root: s.cfg.Debug.Directory, Version: "v-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := real.Stop(); err != nil {
		t.Fatal(err)
	}
	var exported string
	s.performanceExported = func(_ fyne.Window, path string) { exported = path }
	if !s.exportPreviousPerformance() {
		t.Fatal("refused a finished session")
	}
	pumpPerformance(t, posts, func() bool { return exported != "" })
	if filepath.Dir(exported) != real.Snapshot().Directory || s.performanceView.exportLabel.Text != exported || s.performanceView.previous.Disabled() {
		t.Fatalf("previous-session export: %q", exported)
	}
	// Not while a recording is running: its directory is still being written.
	s.startPerformance(false)
	pumpPerformance(t, posts, func() bool { return s.performanceActive.Load() != nil })
	if s.exportPreviousPerformance() || !s.performanceView.previous.Disabled() {
		t.Fatal("previous-session export admitted during a live recording")
	}
}

func TestPerformanceConcurrentStartStopAndShutdownDoNotLeakCollectors(t *testing.T) {
	s, _, posts := performanceTestSession(t)
	var createdMu sync.Mutex
	var created []*fakePerformanceRecorder
	s.performanceNew = func(o diagnostics.PerformanceOptions) (performanceRecorder, error) {
		r := newFakePerformance()
		createdMu.Lock()
		created = append(created, r)
		createdMu.Unlock()
		return r.create(o)
	}
	s.startPerformance(false)
	pumpPerformance(t, posts, func() bool { return s.performanceActive.Load() != nil })
	start := make(chan struct{})
	var callers sync.WaitGroup
	for i := 0; i < 16; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			s.startPerformance(false)
			s.stopPerformance(false)
		}()
	}
	close(start)
	s.shutdownPerformance() // closes worker admission under the same mutex
	callers.Wait()
	waitPerformanceWorkers(t, s)
	createdMu.Lock()
	defer createdMu.Unlock()
	for _, r := range created {
		select {
		case <-r.Done():
		default:
			t.Fatal("an admitted collector survived shutdown")
		}
	}
	if s.performanceActive.Load() != nil || s.startPerformance(false) {
		t.Fatal("sampler admitted after shutdown")
	}
}

func TestPerformancePendingFinalExportBlocksRestart(t *testing.T) {
	s, _, _ := performanceTestSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.performanceMu.Lock()
	s.performanceCurrent = &performanceRun{ctx: ctx, cancel: cancel, stop: make(chan struct{}), phase: performanceStopped, exportAfterStop: true}
	s.performanceMu.Unlock()
	s.openPerformance()
	if !s.performanceView.start.Disabled() || s.startPerformance(false) {
		t.Fatal("restart could replace a stopped recorder before its final export was admitted")
	}
}

func TestPerformanceStoppedFailedConstructorCanRetry(t *testing.T) {
	s, _, posts := performanceTestSession(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s.performanceNew = func(diagnostics.PerformanceOptions) (performanceRecorder, error) {
		close(entered)
		<-release
		return nil, errors.New("initialization failed")
	}
	s.openPerformance()
	s.startPerformance(false)
	<-entered
	s.stopPerformance(true)
	once.Do(func() { close(release) })
	pumpPerformance(t, posts, func() bool { return strings.Contains(s.performanceView.status.Text, "initialization failed") })
	s.performanceMu.Lock()
	pending := s.performanceCurrent.exportAfterStop
	s.performanceMu.Unlock()
	if pending || s.performanceView.start.Disabled() {
		t.Fatal("failed constructor left a nonexistent export blocking retry")
	}
}
