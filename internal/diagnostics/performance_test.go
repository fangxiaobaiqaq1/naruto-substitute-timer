package diagnostics

import (
	"bytes"
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

func TestPerformanceErrorPrivacy(t *testing.T) {
	for _, err := range []error{
		&os.PathError{Op: "open", Path: "/home/PRIVATE-ACCOUNT/data", Err: os.ErrPermission},
		&os.LinkError{Op: "rename", Old: "/home/PRIVATE-ACCOUNT/old", New: "/home/PRIVATE-ACCOUNT/new", Err: os.ErrPermission},
	} {
		for _, text := range []string{performanceError(err), performancePanicError("collector", err).Error()} {
			if strings.Contains(text, "PRIVATE-ACCOUNT") || strings.Contains(text, "/home/") {
				t.Fatal("error leaked a path", text)
			}
		}
	}
	if strings.Contains(performancePanicError("collector", "/home/PRIVATE-ACCOUNT").Error(), "PRIVATE-ACCOUNT") {
		t.Fatal("untyped panic payload leaked")
	}
	b, err := json.Marshal(PerformanceSystem{})
	if err != nil || bytes.Contains(b, []byte("total_ram_bytes")) {
		t.Fatal("missing RAM reported as zero")
	}
}

func TestPerformanceLifecyclePrivacyAndSnapshot(t *testing.T) {
	c := &fakePerformanceCollector{}
	r := performanceTestRecorder(t, c, performanceLimits{10 * time.Millisecond, 5, 1 << 20}, false)
	at := time.Now().Add(-20 * time.Millisecond)
	f := frame.Frame{Img: image.NewRGBA(image.Rect(0, 0, 32, 24)), CaptureStarted: at, CapturedAt: at.Add(time.Millisecond),
		AnalysisStarted: at.Add(time.Millisecond), AnalyzedAt: at.Add(2 * time.Millisecond), CaptureMethod: "mumu-sdk",
		Scene: "fight", PlayerName: "SecretAccount", OppName: "SecretOpponent", Status: "SecretRawStatus", LeftNinja: "SecretNinja"}
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
	for _, secret := range []string{"SecretAccount", "SecretOpponent", "SecretRawStatus", "SecretNinja"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("performance capture retained private frame text", secret)
		}
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
		if strings.Contains(s.LastError, s.Directory) {
			t.Fatal("write error leaked report directory")
		}
	})
}

func TestPerformanceOptionalProfilesAndManualStop(t *testing.T) {
	c := &fakePerformanceCollector{}
	r := performanceTestRecorder(t, c, performanceLimits{time.Second, 100, 1 << 20}, true)
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot()
	if s.StopReason != "user_stopped" || s.EndedAt == nil {
		t.Fatal("manual stop not finalized")
	}
	for _, name := range []string{"heap-start.pprof", "heap-end.pprof", "goroutines-start.txt", "goroutines-end.txt"} {
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
