package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"narutotimer/internal/frame"
)

type fakePerformanceCollector struct {
	calls  atomic.Int32
	closed atomic.Int32
	read   func() performancePlatformSample
}

func (c *fakePerformanceCollector) System() PerformanceSystem {
	return PerformanceSystem{OS: "test", OSVersion: "test version", LogicalCPUs: 4, Unavailable: map[string]string{"gpu": "test unsupported"}}
}
func (c *fakePerformanceCollector) Collect() performancePlatformSample {
	n := c.calls.Add(1)
	if c.read != nil {
		return c.read()
	}
	return performancePlatformSample{Values: map[string]float64{
		"system_cpu_total_seconds": float64(n) * 4, "system_cpu_idle_seconds": float64(n) * 2,
	}, Processes: []PerformanceProcess{{PID: os.Getpid(), Name: "timer-app.exe", StartID: "same-instance", CounterAt: time.Now(), Values: map[string]float64{
		"cpu_seconds": float64(n) * .01, "working_set_bytes": float64(1000 + n*100), "io_read_bytes": float64(n) * 100,
	}}}, Unavailable: map[string]string{"gpu": "not supported on fake platform"}}
}
func (c *fakePerformanceCollector) Close() { c.closed.Add(1) }

func performanceTestRecorder(t *testing.T, c *fakePerformanceCollector, limits performanceLimits, profiles bool) *PerformanceRecorder {
	t.Helper()
	r, err := newPerformance(PerformanceOptions{Root: t.TempDir(), Version: "v-test", Profiles: profiles},
		func(int, string) (performanceCollector, error) { return c, nil }, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Stop() })
	return r
}

func waitPerformance(t *testing.T, r *PerformanceRecorder) {
	t.Helper()
	select {
	case <-r.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("performance recorder did not stop")
	}
}

func TestPerformanceCounterRatesAndPIDReuse(t *testing.T) {
	base := time.Now()
	previous := performancePlatformSample{Values: map[string]float64{"system_cpu_total_seconds": 20, "system_cpu_idle_seconds": 10},
		Processes: []PerformanceProcess{
			{PID: 1, StartID: "a", CounterAt: base, Values: map[string]float64{"cpu_seconds": 3, "io_read_bytes": 100, "io_write_bytes": 50}},
			{PID: 2, StartID: "old-pid", CounterAt: base, Values: map[string]float64{"cpu_seconds": 99}},
		}}
	// Counter reads span 2s even though delayed Collect completions are 0.1s apart.
	s := PerformanceSample{At: base.Add(2500 * time.Millisecond), IntervalS: .1, Values: map[string]float64{"system_cpu_total_seconds": 40, "system_cpu_idle_seconds": 15}, Unavailable: make(map[string]string),
		Processes: []PerformanceProcess{
			{PID: 1, Name: `C:\Users\secret\timer-app.exe`, StartID: "a", CounterAt: base.Add(2 * time.Second), Values: map[string]float64{"cpu_seconds": 5, "io_read_bytes": 100, "io_write_bytes": 90, "working_set_bytes": 0}},
			{PID: 2, StartID: "new-pid", CounterAt: base.Add(2 * time.Second), Values: map[string]float64{"cpu_seconds": 100}},
		}}
	performanceRates(&s, previous, 1, 4)
	if s.Values["main_cpu_cores"] != 1 || s.Values["main_cpu_percent"] != 25 || s.Values["system_cpu_percent"] != 75 {
		t.Fatalf("CPU units: %+v", s.Values)
	}
	if s.Values["main_counter_interval_seconds"] != 2 || s.Values["main_counter_age_ms"] != 500 {
		t.Fatal("counter timestamp lost")
	}
	if value, ok := s.Values["tree_io_read_bytes_s"]; !ok || value != 0 {
		t.Fatal("real zero read rate was lost")
	}
	if _, ok := s.Processes[1].Values["cpu_percent"]; ok {
		t.Fatal("reused PID produced a false rate")
	}
	if s.Processes[0].Name != "timer-app.exe" {
		t.Fatal("process path escaped sanitization")
	}
	if s.Unavailable["tree_cpu_percent"] == "" {
		t.Fatal("partial tree rate not identified")
	}
	if v, ok := counterRate(map[string]float64{"x": 1}, map[string]float64{"x": 2}, "x", 1); ok || v != 0 {
		t.Fatal("reset became a negative rate")
	}
	if _, ok := counterRate(nil, nil, "x", 0); ok {
		t.Fatal("missing baseline became zero")
	}
	bad := map[string]float64{"nan": math.NaN(), "inf": math.Inf(1), "negative": -1, "zero": 0}
	missing := make(map[string]string)
	finiteValues(bad, missing)
	if len(bad) != 1 || len(missing) != 3 {
		t.Fatalf("invalid values retained: %+v", bad)
	}
}

// Recording is local and opt-in: errors keep the path and panic detail the
// developer needs to act on them.
func TestPerformanceErrorsKeepDetail(t *testing.T) {
	err := &os.PathError{Op: "open", Path: "/data/report/samples.jsonl", Err: os.ErrPermission}
	if text := performanceError(err); !strings.Contains(text, "/data/report/samples.jsonl") || !strings.Contains(text, "permission denied") {
		t.Fatalf("error text = %q", text)
	}
	text := performancePanicError("性能采样异常", "index out of range [3]").Error()
	if !strings.Contains(text, "index out of range [3]") || !strings.Contains(text, "goroutine") {
		t.Fatalf("panic text lacks value or stack: %q", text)
	}
	if got := cleanPerformanceText("a\x00b\nc", 10); got != "a b c" {
		t.Fatalf("control characters = %q", got)
	}
	b, jsonErr := json.Marshal(PerformanceSystem{})
	if jsonErr != nil || bytes.Contains(b, []byte("total_ram_bytes")) {
		t.Fatal("missing RAM reported as zero")
	}
}

func TestPerformanceLifecycleAndSnapshot(t *testing.T) {
	c := &fakePerformanceCollector{}
	r := performanceTestRecorder(t, c, performanceLimits{10 * time.Millisecond, 5, 1 << 20}, false)
	at := time.Now().Add(-20 * time.Millisecond)
	f := frame.Frame{Img: image.NewRGBA(image.Rect(0, 0, 32, 24)), CaptureStarted: at, CapturedAt: at.Add(time.Millisecond),
		AnalysisStarted: at.Add(time.Millisecond), AnalyzedAt: at.Add(2 * time.Millisecond), CaptureMethod: "MuMu 实例 0 · SDK",
		Scene: "unsupported-resolution", LayoutProfile: "camp", Engine: "rgb"}
	r.ObserveFrame(f, at.Add(3*time.Millisecond), 16*time.Millisecond, false)
	r.ObserveUIQueue(2 * time.Millisecond)
	if err := r.Mark("卡顿"); err != nil {
		t.Fatal(err)
	}
	waitPerformance(t, r)
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot()
	if s.Active || s.Samples != 5 || s.StopReason != "sample_limit" || c.closed.Load() != 1 {
		t.Fatalf("lifecycle: %+v closed=%d", s, c.closed.Load())
	}
	data, err := os.ReadFile(filepath.Join(s.Directory, "samples.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != s.Bytes || len(bytes.Split(bytes.TrimSpace(data), []byte{'\n'})) != s.Samples {
		t.Fatal("published prefix does not match complete samples")
	}
	// The real capture method, scene and layout reach the report unmodified.
	if a := s.Latest.Application; a.Backend != "MuMu 实例 0 · SDK" || a.Scene != "unsupported-resolution" || a.LayoutProfile != "camp" || a.Engine != "rgb" || a.Width != 32 {
		t.Fatalf("application labels = %+v", a)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var sample PerformanceSample
		if err := json.Unmarshal(line, &sample); err != nil {
			t.Fatal(err)
		}
		if _, ok := sample.Values["tree_gpu_busiest_engine_percent"]; ok {
			t.Fatal("unsupported GPU fabricated a zero")
		}
	}
	if !bytes.Contains(data, []byte("卡顿")) {
		t.Fatal("marker was lost")
	}
	s.Latest.Values["go_heap_alloc_bytes"] = -100
	s.Latest.Processes[0].Values["working_set_bytes"] = -100
	if s.Latest.Application.CaptureMS != nil {
		*s.Latest.Application.CaptureMS = -100
	}
	fresh := r.Snapshot()
	if fresh.Latest.Values["go_heap_alloc_bytes"] < 0 || fresh.Latest.Processes[0].Values["working_set_bytes"] < 0 || *fresh.Latest.Application.CaptureMS < 0 {
		t.Fatal("snapshot aliases recorder state")
	}
	m := r.Metadata()
	m.System.Unavailable["gpu"] = "mutated"
	if r.Metadata().System.Unavailable["gpu"] == "mutated" {
		t.Fatal("metadata aliases state")
	}
	if err := r.Mark("too late"); err == nil {
		t.Fatal("stopped session accepted a marker")
	}
}

func TestPerformanceByteLimitAndCollectorFailure(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		c := &fakePerformanceCollector{}
		r := performanceTestRecorder(t, c, performanceLimits{time.Millisecond, 100, 1}, false)
		waitPerformance(t, r)
		s := r.Snapshot()
		if s.StopReason != "byte_limit" || s.Samples != 0 || s.Bytes != 0 || c.closed.Load() != 1 {
			t.Fatalf("byte limit: %+v", s)
		}
	})
	t.Run("panic", func(t *testing.T) {
		c := &fakePerformanceCollector{read: func() performancePlatformSample { panic("injected collector failure") }}
		r := performanceTestRecorder(t, c, performanceLimits{time.Millisecond, 100, 1 << 20}, false)
		waitPerformance(t, r)
		if r.Snapshot().StopReason != "collector_failed" || r.Stop() == nil || c.closed.Load() != 1 {
			t.Fatal("collector panic was not contained")
		}
	})
	t.Run("write failure", func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		c := &fakePerformanceCollector{read: func() performancePlatformSample {
			close(entered)
			<-release
			return performancePlatformSample{}
		}}
		r := performanceTestRecorder(t, c, performanceLimits{time.Millisecond, 100, 1 << 20}, false)
		<-entered
		_ = r.file.Close()
		close(release)
		waitPerformance(t, r)
		s := r.Snapshot()
		if s.StopReason != "write_failed" || s.Bytes != 0 || r.Stop() == nil {
			t.Fatalf("write failure: %+v", s)
		}
		if !strings.Contains(s.LastError, "samples.jsonl") {
			t.Fatalf("write error does not name the file: %q", s.LastError)
		}
	})
}

func TestPerformanceOptionalProfilesAndManualStop(t *testing.T) {
	c := &fakePerformanceCollector{}
	r := performanceTestRecorder(t, c, performanceLimits{time.Second, 100, 1 << 20}, true)
	// The start CPU profile is still running: a user mark's profile is skipped
	// (one CPU profile per process), and Stop ends the running one early.
	deadline := time.Now().Add(5 * time.Second)
	for !r.cpuProfiling.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := r.Mark(PerformanceUserMark + "：卡顿"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatal("Stop waited for the full CPU profile duration")
	}
	s := r.Snapshot()
	if s.StopReason != "user_stopped" || s.EndedAt == nil {
		t.Fatal("manual stop not finalized")
	}
	if !strings.Contains(s.ProfileStatus["cpu-mark-1.pprof"], "跳过") {
		t.Fatalf("overlapping CPU profile status = %q", s.ProfileStatus["cpu-mark-1.pprof"])
	}
	for _, name := range []string{"heap-start.pprof", "heap-end.pprof", "goroutines-start.txt", "goroutines-end.txt", "cpu-start.pprof"} {
		if s.ProfileStatus[name] != "ok" {
			t.Fatalf("%s: %s", name, s.ProfileStatus[name])
		}
		info, err := os.Stat(filepath.Join(s.Directory, name))
		if err != nil || info.Size() == 0 {
			t.Fatalf("missing profile %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Directory, "status.json")); err != nil {
		t.Fatal(err)
	}
}

func performanceTestFloat(v float64) *float64 { return &v }

// The system-wide table must name the emulator and the heaviest other
// programs, not just the timer's own tree, and stay bounded.
func TestPerformanceTopProcessesNameTheEmulatorAndTheHogs(t *testing.T) {
	previous := performancePlatformSample{}
	host := performancePlatformSample{}
	add := func(pid int, name string, before, now, memory float64) {
		previous.AllProcesses = append(previous.AllProcesses, performanceSystemProcess{PID: pid, Name: name, StartID: "s", CPUSeconds: before})
		host.AllProcesses = append(host.AllProcesses, performanceSystemProcess{PID: pid, Name: name, StartID: "s", CPUSeconds: now, WorkingSet: memory})
	}
	add(0, "System Idle Process", 0, 1000, 8<<10) // Idle time is not CPU usage.
	add(10, "timer-app.exe", 1, 1.08, 150<<20)
	add(11, "ocr-helper.exe", 0, 0, 80<<20)
	add(500, "MuMuVMMHeadless.exe", 10, 12.4, 6<<30)
	add(501, "MuMuNxDevice.exe", 5, 5.4, 1<<30)
	add(600, "dnplayer.exe", 0, 0, 10<<20)
	add(700, "chrome.exe", 3, 3.8, 3<<30)
	for i := 0; i < 60; i++ { // Background noise that must not flood the table.
		add(1000+i, "svchost.exe", 0, 0, float64(i)*(1<<20))
	}
	host.AllProcesses[3].GPUPercent, host.AllProcesses[3].GPUDedicated = performanceTestFloat(77), performanceTestFloat(2<<30)
	// A reused PID must not produce a CPU rate from the old process's counter.
	previous.AllProcesses = append(previous.AllProcesses, performanceSystemProcess{PID: 800, Name: "old.exe", StartID: "old", CPUSeconds: 0})
	host.AllProcesses = append(host.AllProcesses, performanceSystemProcess{PID: 800, Name: "new.exe", StartID: "new", CPUSeconds: 500, WorkingSet: 4 << 30})

	s := PerformanceSample{IntervalS: 2, Values: map[string]float64{}, Unavailable: map[string]string{},
		Processes: []PerformanceProcess{{PID: 10}, {PID: 11}}}
	performanceTop(&s, host, previous, 10, 4)
	if len(s.Top) == 0 || len(s.Top) > PerformanceMaxTop {
		t.Fatalf("top rows = %d", len(s.Top))
	}
	rows := make(map[int]PerformanceTopProcess)
	for _, row := range s.Top {
		rows[row.PID] = row
	}
	if s.Top[0].PID != 500 || math.Abs(*s.Top[0].CPUPercent-30) > 1e-9 { // 2.4 CPU-seconds / 2 s / 4 cores.
		t.Fatalf("heaviest row = %+v", s.Top[0])
	}
	for pid, role := range map[int]string{10: "self", 11: "child", 500: "emulator", 501: "emulator", 600: "emulator", 700: ""} {
		row, ok := rows[pid]
		if !ok || row.Role != role {
			t.Errorf("PID %d: listed=%v role=%q, want %q", pid, ok, row.Role, role)
		}
	}
	if _, ok := rows[0]; ok {
		t.Error("the idle pseudo-process was listed")
	}
	if row, ok := rows[800]; !ok || row.CPUPercent != nil {
		t.Errorf("reused PID: listed=%v cpu=%v (memory-heavy, but no valid CPU baseline)", ok, row.CPUPercent)
	}
	if math.Abs(s.Values["emulator_cpu_percent"]-35) > 1e-9 || s.Values["emulator_working_set_bytes"] != 7<<30+10<<20 ||
		s.Values["emulator_gpu_percent"] != 77 || s.Values["emulator_gpu_dedicated_bytes"] != 2<<30 {
		t.Fatalf("emulator aggregates = %v", s.Values)
	}
	// No emulator and no process list are stated, never reported as 0.
	bare := PerformanceSample{IntervalS: 1, Values: map[string]float64{}, Unavailable: map[string]string{}}
	performanceTop(&bare, performancePlatformSample{AllProcesses: []performanceSystemProcess{{PID: 5, Name: "notepad.exe"}}}, performancePlatformSample{}, 10, 4)
	if _, ok := bare.Values["emulator_cpu_percent"]; ok || bare.Unavailable["emulator_cpu_percent"] == "" {
		t.Fatalf("missing emulator = %v / %v", bare.Values, bare.Unavailable)
	}
	bare = PerformanceSample{Values: map[string]float64{}, Unavailable: map[string]string{}}
	performanceTop(&bare, performancePlatformSample{}, performancePlatformSample{}, 10, 4)
	if bare.Top != nil || bare.Unavailable["top_processes"] == "" {
		t.Fatal("missing process list not explained")
	}
	for name, want := range map[string]bool{"MuMuPlayer.exe": true, "Ld9BoxHeadless.exe": true, "dnplayer.exe": true, "HD-Player.exe": true, "explorer.exe": false, "timer-app.exe": false} {
		if performanceEmulatorName(name) != want {
			t.Errorf("performanceEmulatorName(%q) = %v", name, !want)
		}
	}
}

// One slow frame in a busy second must stay visible: the window keeps the
// maximum, and failed/timed-out captures count toward the attempt time.
func TestPerformanceWindowKeepsWorstFrameAndFailures(t *testing.T) {
	block := make(chan struct{})
	c := &fakePerformanceCollector{}
	first := true
	c.read = func() performancePlatformSample {
		if !first {
			<-block // Hold the second sample until the frames are observed.
		}
		first = false
		return performancePlatformSample{}
	}
	r := performanceTestRecorder(t, c, performanceLimits{5 * time.Millisecond, 2, 1 << 20}, false)
	for deadline := time.Now().Add(5 * time.Second); r.Snapshot().Samples < 1 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond) // The frames below belong to the second window.
	}
	base := time.Now().Add(-10 * time.Second)
	poll := 16 * time.Millisecond
	for i := 0; i < 29; i++ {
		at := base.Add(time.Duration(i) * poll)
		r.ObserveFrame(frame.Frame{CaptureStarted: at, CapturedAt: at.Add(5 * time.Millisecond), AnalysisStarted: at.Add(5 * time.Millisecond),
			AnalyzedAt: at.Add(13 * time.Millisecond)}, at.Add(14*time.Millisecond), poll, false)
		r.ObserveLoop(15*time.Millisecond, time.Millisecond)
	}
	at := base.Add(time.Second)
	r.ObserveFrame(frame.Frame{CaptureStarted: at, CapturedAt: at.Add(600 * time.Millisecond), AnalysisStarted: at.Add(600 * time.Millisecond),
		AnalyzedAt: at.Add(1000 * time.Millisecond)}, at.Add(1001*time.Millisecond), poll, false)
	r.ObserveLoop(1010*time.Millisecond, 9*time.Millisecond)
	// A capture that timed out after two seconds: no CapturedAt at all.
	at = base.Add(3 * time.Second)
	r.ObserveFrame(frame.Frame{CaptureStarted: at, Err: os.ErrDeadlineExceeded, CaptureMethod: "雷电 ADB"}, at.Add(2*time.Second), poll, false)
	r.ObserveUIQueue(250 * time.Millisecond)
	r.ObserveUIQueue(3 * time.Millisecond)
	close(block)
	waitPerformance(t, r)
	a := r.Snapshot().Latest.Application
	for name, got := range map[string]*float64{"capture max": a.CaptureMaxMS, "analysis max": a.AnalysisMaxMS, "attempt max": a.AttemptMaxMS,
		"loop max": a.LoopMaxMS, "post max": a.PostMaxMS, "ui queue max": a.UIQueueMaxMS, "capture p95": a.CaptureP95MS} {
		want := map[string]float64{"capture max": 600, "analysis max": 400, "attempt max": 2000, "loop max": 1010, "post max": 9, "ui queue max": 250, "capture p95": 5}[name]
		if got == nil || math.Abs(*got-want) > 1e-6 {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if a.WindowSlow != 2 || a.SlowFrames != 2 || a.WindowErrors != 1 || a.Errors != 1 || a.Attempts != 31 {
		t.Errorf("counters = slow %d/%d errors %d/%d attempts %d", a.WindowSlow, a.SlowFrames, a.WindowErrors, a.Errors, a.Attempts)
	}
	if !strings.Contains(a.LastError, "timeout") || a.Backend != "雷电 ADB" {
		t.Errorf("error/backend = %q / %q", a.LastError, a.Backend)
	}
}

// A session that was never exported (the app was closed, or crashed with a
// torn last line) can still be turned into a report afterwards.
func TestPerformanceOfflineSessionExport(t *testing.T) {
	root := t.TempDir()
	if PerformanceSessions(root) != nil {
		t.Fatal("sessions listed in an empty directory")
	}
	r, err := newPerformance(PerformanceOptions{Root: root, Version: "v-test", Config: []byte(`{"capture":{"provider":"mumu"}}`)},
		func(int, string) (performanceCollector, error) { return &fakePerformanceCollector{}, nil }, performanceLimits{time.Millisecond, 4, 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	waitPerformance(t, r)
	dir := r.Snapshot().Directory
	sessions := PerformanceSessions(root)
	if len(sessions) != 1 || sessions[0] != dir {
		t.Fatalf("sessions = %v, want %s", sessions, dir)
	}
	// Simulate a crash: no final status, and a half-written last record.
	if err := os.Remove(filepath.Join(dir, "status.json")); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "samples.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"at":"2026-01-02T03:04:05Z","elapsed_s":9,"va`)
	_ = f.Close()
	path, err := ExportPerformanceSession(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	files := performanceReportTestZIP(t, path)
	s := performanceReportTestSummary(t, files)
	if s.Samples != 4 || s.SkippedLines != 1 || s.StopReason != "not_finalized" {
		t.Fatalf("offline summary: samples=%d skipped=%d stop=%q", s.Samples, s.SkippedLines, s.StopReason)
	}
	if string(files["config.json"]) != `{"capture":{"provider":"mumu"}}` || len(files["report.html"]) == 0 {
		t.Fatal("offline export lacks config.json or report.html")
	}
	if _, err := ExportPerformanceSession(context.Background(), t.TempDir()); err == nil {
		t.Fatal("a directory without a session exported")
	}
}
