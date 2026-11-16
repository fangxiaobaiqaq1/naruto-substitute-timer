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
		for _, w := range []fyne.Window{s.performanceWindow, s.diagnosticWindow, s.settings, s.miniWin} {
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

func TestPerformanceButtonsReachableWithoutStartingRecorder(t *testing.T) {
	s, r, _ := performanceTestSession(t)
	s.win.Resize(fyne.NewSize(280, 140))
	fitNativeOverlayPreview(s.win)
	for _, name := range []string{"设置", "换边", "诊断", "关于", "性能报告"} {
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
	if s.win.Canvas().Size().Width != 280 {
		t.Fatalf("overlay widened: %v", s.win.Canvas().Size())
	}
	first := overlayButton(s.win.Content(), "设置")
	perf := overlayButton(s.win.Content(), "性能报告")
	firstAt, _ := overlayObjectPosition(s.win.Content(), first, fyne.NewPos(0, 0))
	perfAt, _ := overlayObjectPosition(s.win.Content(), perf, fyne.NewPos(0, 0))
	if perfAt.Y < firstAt.Y+first.Size().Height {
		t.Fatal("performance was squeezed into the four-button row")
	}
	perf.OnTapped()
	w := s.performanceWindow
	if w == nil || !s.performanceView.profiles.Checked {
		t.Fatal("missing independent view/default profile option")
	}
	s.setupDetachedMiniWindow(fyne.CurrentApp())
	s.miniWindowButtons[0].OnTapped() // mini's Settings reaches the same entry
	settings := overlayButton(s.settings.Content(), "性能报告（CPU / 内存 / GPU）")
	if settings == nil {
		t.Fatal("settings entry missing (also used by mini mode)")
	}
	settings.OnTapped()
	s.openDiagnostics()
	diag := overlayButton(s.diagnosticWindow.Content(), "性能报告（不录制游戏画面）")
	if diag == nil {
		t.Fatal("full diagnostics entry missing")
	}
	diag.OnTapped()
	if s.performanceWindow != w || r.creates.Load() != 0 || s.currentDiagnostics() != nil {
		t.Fatal("navigation created duplicate windows or enabled recording")
	}
}

func TestPerformanceLifecycleCloseViewContinuesAndStopExports(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	s.openPerformance()
	s.performanceView.profiles.SetChecked(false)
	s.performanceView.start.OnTapped()
	pumpPerformance(t, posts, func() bool { return s.performanceButton.Text == "性能报告 · 进行中" })
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
	old := s.performanceWindow
	old.Close()
	if s.performanceWindow != nil || !r.Snapshot().Active {
		t.Fatal("closing the view stopped monitoring")
	}
	s.openPerformance()
	if s.performanceWindow == old || s.performanceView.stop.Disabled() {
		t.Fatal("reopened view lost active session")
	}
	s.performanceView.mark.OnTapped()
	pumpPerformance(t, posts, func() bool { return strings.Contains(s.performanceView.status.Text, "已标记卡顿") })
	r.mu.Lock()
	marks := append([]string(nil), r.marks...)
	r.mu.Unlock()
	if len(marks) != 1 || marks[0] != "用户标记：卡顿" {
		t.Fatalf("marks %v", marks)
	}
	var exported string
	s.performanceExported = func(_ fyne.Window, path string) { exported = path }
	s.performanceView.stop.OnTapped()
	pumpPerformance(t, posts, func() bool { return exported != "" })
	if r.Snapshot().Active || s.performanceActive.Load() != nil || !performancePhaseIs(s, performanceStopped) {
		t.Fatal("stop-and-export did not finish Stop")
	}
	r.mu.Lock()
	active := append([]bool(nil), r.exportActive...)
	r.mu.Unlock()
	if len(active) != 1 || active[0] {
		t.Fatalf("final export happened before Stop: %v", active)
	}
	if s.performanceButton.Text != "性能报告" || s.performanceView.start.Disabled() {
		t.Fatal("stopped UI remained active")
	}
}

func TestPerformanceFrameHookOffIsInertAndParallelDiagnosticsAreRecorded(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	f := frame.Frame{Img: image.NewRGBA(image.Rect(0, 0, 400, 250)),
		CapturedAt: time.Now(), CaptureStarted: time.Now().Add(-time.Millisecond), Hold: true,
		Scene: "fight", CaptureMethod: `MuMu 实例 0 · C:\secret-install`, Status: "secret-status",
		PlayerName: "secret-account", OppName: "secret-opponent", Err: errors.New("secret-error")}
	s.provider = func() frame.Frame { return f }
	s.captureOnce()
	if r.creates.Load() != 0 || s.performanceCurrent != nil || len(posts) != 0 {
		t.Fatal("off path created monitoring work")
	}
	s.cfg.UI.PlayerNames = []string{"secret-account"}
	s.cfg.Capture.MuMu.InstallDir = "secret-install"
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
	settings := r.options.Settings
	r.mu.Unlock()
	if frames != 1 || size != image.Pt(400, 250) || poll != 400*time.Millisecond || !full {
		t.Fatalf("observed %d %v %v full=%v", frames, size, poll, full)
	}
	data, err := json.Marshal(struct {
		Frame    frame.Frame
		Settings diagnostics.PerformanceSettings
	}{last, settings})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-") || last.CaptureMethod != "mumu-sdk" || last.Img != nil {
		t.Fatalf("sensitive data crossed performance boundary: %s", data)
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
	pumpPerformance(t, posts, func() bool { return s.performanceButton.Text == "性能报告 · 进行中" && len(posts) == 0 })
	for i := 1; i <= 100; i++ {
		r.sample(diagnostics.PerformanceSample{At: time.Now(), Values: map[string]float64{"tree_cpu_percent": float64(i), "tree_working_set_bytes": 16 << 20},
			Unavailable: map[string]string{"gpu": "PDH GPU counters unavailable"},
			Processes:   []diagnostics.PerformanceProcess{{PID: 101, Name: "timer-app.exe", Values: map[string]float64{"working_set_bytes": 16 << 20}}}})
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
	pumpPerformance(t, posts, func() bool { return strings.Contains(s.performanceView.cpu.Text, "100.0%") })
	gpu := s.performanceView.gpu.Text
	if !strings.Contains(gpu, "不可用") || !strings.Contains(gpu, "PDH GPU") || strings.Contains(gpu, "0.0%") {
		t.Fatalf("GPU misleading: %s", gpu)
	}
	if !strings.Contains(s.performanceView.processes.Text, "PID 101") {
		t.Fatal("process detail missing")
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

func TestPerformanceExitCancelsExportAndSkipsQueuedUI(t *testing.T) {
	s, r, posts := performanceTestSession(t)
	r.exportGate = make(chan struct{}) // only cancellation can release Export
	s.openPerformance()
	s.startPerformance(false)
	pumpPerformance(t, posts, func() bool { return s.performanceButton.Text == "性能报告 · 进行中" })
	s.exportPerformance()
	<-r.exportStarted
	before := s.performanceView.status.Text
	s.stop()
	waitPerformanceWorkers(t, s)
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
	pumpPerformance(t, posts, func() bool { return s.performanceButton.Text == "性能报告 · 进行中" })
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

func TestPerformanceUnavailableAndPartialValuesStayExplicit(t *testing.T) {
	values := map[string]float64{"go_memory_limit_disabled": 1, "tree_cpu_percent": 3}
	if got := performanceMetricValue(values, "go_memory_limit_bytes"); got != "未设置（无限制）" {
		t.Fatalf("memory limit: %q", got)
	}
	if got := performanceMetricValue(nil, "tree_gpu_busiest_engine_percent"); got != "不可用" {
		t.Fatalf("missing GPU: %q", got)
	}
	u := &performanceUpdate{status: diagnostics.PerformanceStatus{Latest: &diagnostics.PerformanceSample{
		Values: values, Unavailable: map[string]string{"tree_cpu_percent": "仅1/2个已观测进程有此指标"},
	}}}
	if got := performanceCard(u, [][2]string{{"tree_cpu_percent", "CPU"}}); !strings.Contains(got, "3.0%") || !strings.Contains(got, "1/2") {
		t.Fatalf("partial aggregate: %s", got)
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
