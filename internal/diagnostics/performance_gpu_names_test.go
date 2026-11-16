package diagnostics

import (
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPerformanceGPUInstanceNames(t *testing.T) {
	for _, tc := range []struct {
		name             string
		pid              int
		physical, engine uint32
		kind             string
	}{
		{"pid_123_luid_0x00000000_0x000012ab_phys_0_eng_2_engtype_3D", 123, 0, 2, "3D"},
		{"pid_12_luid_0X0_0X12AB_phys_1_eng_10_engtype_Compute_0#2", 12, 1, 10, "Compute_0"},
		{"pid_0_luid_0x0_0x12ab_phys_0_eng_0_engtype_Copy", 0, 0, 0, "Copy"},
		{"pid_123_luid_0x0_0x12ab_phys_3", 123, 3, 0, ""},
		{"pid_123_luid_0x0_0x12ab_phys_3#1", 123, 3, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := parsePerformanceGPUInstance(tc.name)
			if !ok || n.pid != tc.pid || n.adapter != "luid_0x00000000_0x000012ab" || n.physical != tc.physical || n.engine != tc.engine || n.engineType != tc.kind || n.hasEngine != (tc.kind != "") {
				t.Fatalf("parse = %+v, %v", n, ok)
			}
		})
	}
	for _, name := range []string{
		"", "_Total", "pid_1", "other_pid_12_luid_0x0_0x1_phys_0",
		"pid_-1_luid_0x0_0x1_phys_0", "pid_+1_luid_0x0_0x1_phys_0",
		"pid_4294967296_luid_0x0_0x1_phys_0", "pid_1x_luid_0x0_0x1_phys_0",
		"pid_1_luid_0x100000000_0x1_phys_0", "pid_1_luid_0xzz_0x1_phys_0",
		"pid_1_luid_0x0_0x1_phys_-1", "pid_1_luid_0x0_0x1_phys_0_extra",
		"pid_1_luid_0x0_0x1_phys_0_eng_0_engtype_", "pid_1_luid_0x0_0x1_phys_0_eng_x_engtype_3D",
		"pid_1_luid_0x0_0x1_phys_0_eng_0_engtype_3D#", "pid_1_luid_0x0_0x1_phys_0_eng_0_engtype_3D#x",
		"pid_1_luid_0x0_0x1_phys_0_eng_0_engtype_3D#1#2",
		"pid_1_luid_0x0_0x1_phys_0_eng_0_engtype_../private",
		"pid_1_luid_0x0_0x1_phys_0_eng_0_engtype_" + strings.Repeat("a", 65), strings.Repeat("a", 257),
	} {
		if n, ok := parsePerformanceGPUInstance(name); ok {
			t.Errorf("accepted malformed name %q as %+v", name, n)
		}
	}
}

func TestPerformanceGPUEngineAggregation(t *testing.T) {
	var totals performanceGPUEngineTotals
	add := func(pid int, adapter string, phys, eng uint32, kind string, value float64, selected bool) {
		totals.add(performanceGPUInstance{pid: pid, adapter: adapter, physical: phys, engine: eng, engineType: kind, hasEngine: true}, value, selected)
	}
	// Same physical engine across processes: 30+20+40=90 system, 50 tree.
	add(12, "A", 0, 0, "3D", 30, true)
	add(123, "A", 0, 0, "3D", 20, true)
	add(999, "A", 0, 0, "3D", 40, false)
	// A different engine, despite the same engtype, must not be added to it.
	add(12, "A", 0, 1, "3D", 40, true)
	add(12, "A", 0, 1, "3D", 5, true) // duplicate counter contexts, same physical engine
	add(123, "A", 0, 1, "3D", 20, true)
	// Physical-index and adapter boundaries must remain independent too.
	add(12, "A", 1, 0, "3D", 55, true)
	add(12, "B", 0, 0, "3D", 60, true)
	if value, ok := totals.systemBusiest(); !ok || value != 90 {
		t.Fatalf("system busiest = %v, %v; want 90", value, ok)
	}
	rows, processes, tree := totals.selectedResults()
	if tree != 65 || processes[12] != 60 || processes[123] != 20 {
		t.Fatalf("tree=%v processes=%v; expected tree=65 and process maxima=60,20", tree, processes)
	}
	if len(rows) != 6 {
		t.Fatalf("got %d selected hardware-engine rows, want 6", len(rows))
	}
	for _, row := range rows {
		if row.PID != 12 && row.PID != 123 {
			t.Fatalf("retained unrelated process: %+v", row)
		}
	}
	add(12, "C", 0, 0, "Copy", 80, true)
	add(123, "C", 0, 0, "Copy", 70, true)
	if value, _ := totals.systemBusiest(); value != 100 {
		t.Fatalf("system percentage should saturate at 100, got %v", value)
	}
	_, _, tree = totals.selectedResults()
	if tree != 100 {
		t.Fatalf("tree percentage should saturate at 100, got %v", tree)
	}
}

func TestPerformanceGPUFilteringAndBounds(t *testing.T) {
	var totals performanceGPUEngineTotals
	if value, ok := totals.systemBusiest(); ok || value != 0 {
		t.Fatal("missing data was reported as measured zero")
	}
	// Many unrelated PID rows must not consume the selected-row budget.
	for pid := 1000; pid < 5000; pid++ {
		totals.add(performanceGPUInstance{pid: pid, adapter: "A", hasEngine: true}, 0.01, false)
	}
	if len(totals.selected) != 0 || len(totals.system) != 1 {
		t.Fatalf("unrelated PID data retained: %+v", totals)
	}
	for engine := 0; engine < performanceMaxGPUEngines+1; engine++ {
		totals.add(performanceGPUInstance{pid: 12, adapter: "A", engine: uint32(engine), hasEngine: true}, 1, true)
	}
	rows, _, _ := totals.selectedResults()
	if len(rows) != 256 || !totals.selectedTruncated || totals.systemTruncated {
		t.Fatalf("selected/system caps not independent: rows=%d selected=%v system=%v", len(rows), totals.selectedTruncated, totals.systemTruncated)
	}
	if _, ok := totals.systemBusiest(); !ok {
		t.Fatal("selected cap incorrectly disabled system total")
	}
	for engine := 0; engine < 1025; engine++ {
		totals.add(performanceGPUInstance{adapter: "B", engine: uint32(engine), hasEngine: true}, 0, false)
	}
	if len(totals.system) != 1024 || !totals.systemTruncated {
		t.Fatal("system hardware-engine cap missing")
	}
	if _, ok := totals.systemBusiest(); ok {
		t.Fatal("partial system aggregate must not be reported as complete")
	}
}

func TestPerformanceGPUValidNumbers(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.01} {
		if performanceGPUValidNumber(value) {
			t.Errorf("accepted invalid PDH number %v", value)
		}
	}
	for _, value := range []float64{0, 1, 100, 100.1, 1 << 40} {
		if !performanceGPUValidNumber(value) {
			t.Errorf("rejected valid PDH number %v", value)
		}
	}
}

func performanceTestGPUSample() performancePlatformSample {
	return performancePlatformSample{
		Values: make(map[string]float64), Unavailable: make(map[string]string),
		Processes: []PerformanceProcess{
			{PID: 10, StartID: "start-A", Values: make(map[string]float64), Unavailable: make(map[string]string)},
			{PID: 20, StartID: "start-B", Values: make(map[string]float64), Unavailable: make(map[string]string)},
		},
	}
}

func TestPerformanceGPUTreeRequiresCompleteCoverage(t *testing.T) {
	var totals performanceGPUEngineTotals
	totals.add(performanceGPUInstance{pid: 10, adapter: "A", engineType: "3D", hasEngine: true}, 0, true)
	s := performanceTestGPUSample()
	performanceGPUApplyEngineTotals(&s, &totals, nil, "", "", false)
	if _, ok := s.Values["tree_gpu_busiest_engine_percent"]; ok || s.Unavailable["tree_gpu_busiest_engine_percent"] == "" {
		t.Fatal("one valid zero and one missing PID must NOT become tree=0")
	}
	if v, ok := s.Processes[0].Values["gpu_busiest_engine_percent"]; !ok || v != 0 {
		t.Fatal("independent measured zero was lost")
	}
	if _, ok := s.Processes[1].Values["gpu_busiest_engine_percent"]; ok || s.Processes[1].Unavailable["gpu_busiest_engine_percent"] == "" {
		t.Fatal("missing PID was fabricated as zero or lost its status")
	}
	if len(s.GPUEngines) != 1 || s.GPUEngines[0].Percent != 0 {
		t.Fatal("valid engine row must survive partial tree coverage")
	}
	totals.add(performanceGPUInstance{pid: 20, adapter: "A", engineType: "3D", hasEngine: true}, 0, true)
	s = performanceTestGPUSample()
	performanceGPUApplyEngineTotals(&s, &totals, nil, "", "", false)
	if v, ok := s.Values["tree_gpu_busiest_engine_percent"]; !ok || v != 0 || s.Unavailable["tree_gpu_busiest_engine_percent"] != "" {
		t.Fatal("fully covered, genuinely idle tree must retain a measured zero")
	}
}

func performanceTestGPUWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker operation did not finish within the test bound")
	}
}

func performanceTestGPURelease(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func performanceTestGPUReady(t *testing.T, w *performanceGPUWorker) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, reason := w.metadata(); reason == "" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker did not initialize")
}

func TestPerformanceGPUWorkerHungCollect(t *testing.T) {
	slot := make(chan struct{}, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls, initializations, closes atomic.Int32
	var inNative, concurrentClose atomic.Bool
	ops := performanceGPUNativeOps{
		initialize: func(func() bool) PerformanceSystem {
			initializations.Add(1)
			return PerformanceSystem{GPUs: []PerformanceGPU{{Name: "cached GPU"}}}
		},
		collect: func(s *performancePlatformSample, _ func() bool) {
			inNative.Store(true)
			defer inNative.Store(false)
			calls.Add(1)
			close(entered)
			<-release
			s.Values["system_gpu_busiest_engine_percent"] = 99
			s.Processes[0].Values["gpu_dedicated_bytes"] = 123
		},
		close: func() {
			if inNative.Load() {
				concurrentClose.Store(true)
			}
			closes.Add(1)
		},
	}
	w := newPerformanceGPUWorker(slot, ops, time.Second, 50*time.Millisecond)
	defer func() { w.close(); performanceTestGPURelease(release); performanceTestGPUWait(t, w.done) }()
	performanceTestGPUReady(t, w)
	s := performanceTestGPUSample()
	s.Processes[0].Values["cpu_seconds"] = 7
	s.Processes[0].CounterAt = time.Now()
	var result performancePlatformSample
	var reason string
	returned := make(chan struct{})
	go func() { result, reason = w.sample(s.Processes); close(returned) }()
	performanceTestGPUWait(t, entered)
	performanceTestGPUWait(t, returned)
	if !strings.Contains(reason, "timed out") || len(result.Values) != 0 {
		t.Fatalf("timeout published a native result: %v / %s", result.Values, reason)
	}
	for i := 0; i < 50; i++ {
		if _, reason := w.sample(s.Processes); !strings.Contains(reason, "timed out") {
			t.Fatalf("session unexpectedly resumed GPU sampling: %s", reason)
		}
		next := newPerformanceGPUWorker(slot, ops, time.Second, time.Second)
		if _, reason := next.metadata(); !strings.Contains(reason, "slot occupied") {
			t.Fatal("a restart forked another native owner")
		}
		next.close()
		select {
		case <-next.done:
		default:
			t.Fatal("rejected session started a goroutine")
		}
	}
	stopped := make(chan struct{})
	go func() { w.close(); w.close(); close(stopped) }()
	performanceTestGPUWait(t, stopped)
	if closes.Load() != 0 || concurrentClose.Load() || initializations.Load() != 1 || calls.Load() != 1 {
		t.Fatal("native owner was closed concurrently, or a second native call was forked")
	}
	if info, reason := w.metadata(); len(info.GPUs) != 1 || !strings.Contains(reason, "timed out") {
		t.Fatal("metadata should remain cached/nonblocking with latched GPU status")
	}
	performanceTestGPURelease(release)
	performanceTestGPUWait(t, w.done)
	if closes.Load() != 1 || concurrentClose.Load() {
		t.Fatal("native cleanup was not performed once by its owner")
	}
	if late, reason := w.sample(s.Processes); len(late.Values) != 0 || !strings.Contains(reason, "timed out") {
		t.Fatal("late collect result was published after native completion")
	}
	if s.Processes[0].Values["cpu_seconds"] != 7 || len(s.Processes[0].Values) != 1 {
		t.Fatal("GPU worker mutated the caller's CPU sample")
	}
}

func TestPerformanceGPUWorkerStopInterruptsWait(t *testing.T) {
	entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	w := newPerformanceGPUWorker(make(chan struct{}, 1), performanceGPUNativeOps{
		initialize: func(func() bool) PerformanceSystem { return PerformanceSystem{} },
		collect:    func(*performancePlatformSample, func() bool) { close(entered); <-release },
		close:      func() { close(closed) },
	}, time.Second, time.Hour) // Stop must wake the caller, not wait for this timer.
	defer func() { w.close(); performanceTestGPURelease(release); performanceTestGPUWait(t, w.done) }()
	performanceTestGPUReady(t, w)
	returned := make(chan struct{})
	go func() {
		if _, reason := w.sample(nil); !strings.Contains(reason, "stopped") {
			t.Errorf("stop result: %s", reason)
		}
		close(returned)
	}()
	performanceTestGPUWait(t, entered)
	stopped := make(chan struct{})
	go func() { w.close(); close(stopped) }()
	performanceTestGPUWait(t, stopped)
	performanceTestGPUWait(t, returned)
	select {
	case <-closed:
		t.Fatal("Close raced a still-running native call")
	default:
	}
	performanceTestGPURelease(release)
	performanceTestGPUWait(t, w.done)
	performanceTestGPUWait(t, closed)
}

func TestPerformanceGPUWorkerLateInitialization(t *testing.T) {
	slot := make(chan struct{}, 1)
	entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	w := newPerformanceGPUWorker(slot, performanceGPUNativeOps{
		initialize: func(canceled func() bool) PerformanceSystem {
			close(entered)
			<-release
			if !canceled() {
				t.Error("initializer should skip further native phases after cancellation")
			}
			return PerformanceSystem{GPUs: []PerformanceGPU{{Name: "late metadata must be discarded"}}}
		},
		collect: func(*performancePlatformSample, func() bool) { t.Error("timed-out initializer must not collect") },
		close:   func() { close(closed) },
	}, time.Hour, time.Second)
	defer func() { w.close(); performanceTestGPURelease(release); performanceTestGPUWait(t, w.done) }()
	performanceTestGPUWait(t, entered)
	checked := make(chan struct{})
	go func() {
		if _, reason := w.metadata(); reason != "GPU initialization pending" {
			t.Errorf("pending metadata: %s", reason)
		}
		if _, reason := w.sample(nil); reason != "GPU initialization pending" {
			t.Errorf("pending sample: %s", reason)
		}
		close(checked)
	}()
	performanceTestGPUWait(t, checked)
	// Expire deterministically without relying on scheduler timing. Production
	// sets this deadline once; this is the only test-only clock adjustment.
	w.mu.Lock()
	w.initDeadline = time.Now().Add(-time.Second)
	w.mu.Unlock()
	if _, reason := w.metadata(); !strings.Contains(reason, "initialization timed out") {
		t.Fatal(reason)
	}
	if _, reason := w.sample(nil); !strings.Contains(reason, "initialization timed out") {
		t.Fatal(reason)
	}
	w.close()
	select {
	case <-closed:
		t.Fatal("Close raced a blocked initializer")
	default:
	}
	performanceTestGPURelease(release)
	performanceTestGPUWait(t, w.done)
	performanceTestGPUWait(t, closed)
	if info, _ := w.metadata(); len(info.GPUs) != 0 {
		t.Fatal("late initialization metadata was published")
	}
}

func TestPerformanceGPUWorkerCloseRetainsSlotUntilNativeCleanup(t *testing.T) {
	slot := make(chan struct{}, 1)
	closing, release := make(chan struct{}), make(chan struct{})
	var initialized atomic.Int32
	ops := performanceGPUNativeOps{
		initialize: func(func() bool) PerformanceSystem { initialized.Add(1); return PerformanceSystem{} },
		collect:    func(*performancePlatformSample, func() bool) {},
		close:      func() { close(closing); <-release },
	}
	w := newPerformanceGPUWorker(slot, ops, time.Second, time.Second)
	defer func() { w.close(); performanceTestGPURelease(release); performanceTestGPUWait(t, w.done) }()
	performanceTestGPUReady(t, w)
	w.close()
	performanceTestGPUWait(t, closing)
	next := newPerformanceGPUWorker(slot, ops, time.Second, time.Second)
	if _, reason := next.metadata(); !strings.Contains(reason, "slot occupied") {
		t.Fatal("cleanup released slot too early")
	}
	next.close()
	if initialized.Load() != 1 {
		t.Fatal("overlapping native cleanup/init")
	}
	close(release)
	performanceTestGPUWait(t, w.done)
	ops.close = func() {}
	next = newPerformanceGPUWorker(slot, ops, time.Second, time.Second)
	performanceTestGPUReady(t, next)
	next.close()
	performanceTestGPUWait(t, next.done)
	if initialized.Load() != 2 {
		t.Fatal("returned native owner failed to release global slot")
	}
}

func TestPerformanceGPUWorkerTimelyResultAndCPUIsolation(t *testing.T) {
	slot := make(chan struct{}, 1)
	w := newPerformanceGPUWorker(slot, performanceGPUNativeOps{
		initialize: func(func() bool) PerformanceSystem { return PerformanceSystem{} },
		collect: func(s *performancePlatformSample, _ func() bool) {
			if s.Processes[0].Name != "" || len(s.Processes[0].Values) != 0 || !s.Processes[0].CounterAt.IsZero() {
				t.Error("CPU sample data was passed to the native GPU owner")
			}
			s.Values["system_gpu_busiest_engine_percent"] = 12
			s.Values["system_cpu_total_seconds"] = 999
			s.Processes[0].Values["gpu_dedicated_bytes"] = 64
			s.Processes[0].Values["cpu_seconds"] = 999
		}, close: func() {},
	}, time.Second, time.Second)
	defer func() { w.close(); performanceTestGPUWait(t, w.done) }()
	performanceTestGPUReady(t, w)
	s := performanceTestGPUSample()
	s.Values["system_cpu_total_seconds"] = 100
	s.Processes[0].Values["cpu_seconds"] = 7
	s.Processes[0].Name = "main.exe"
	s.Processes[0].CounterAt = time.Now()
	counterAt := s.Processes[0].CounterAt
	result, reason := w.sample(s.Processes)
	if reason != "" {
		t.Fatal(reason)
	}
	performanceGPUApplySample(&s, result)
	if s.Values["system_gpu_busiest_engine_percent"] != 12 || s.Processes[0].Values["gpu_dedicated_bytes"] != 64 {
		t.Fatal("timely GPU data was not merged")
	}
	if s.Values["system_cpu_total_seconds"] != 100 || s.Processes[0].Values["cpu_seconds"] != 7 || s.Processes[0].CounterAt != counterAt {
		t.Fatal("GPU result changed CPU counters/timestamp")
	}
	s = performanceTestGPUSample()
	s.Processes[0].StartID = "reused PID"
	performanceGPUApplySample(&s, result)
	if len(s.Values) != 0 || len(s.Processes[0].Values) != 0 || s.Unavailable["gpu_engines"] == "" {
		t.Fatal("GPU result crossed process identities")
	}
}

func FuzzPerformanceGPUInstance(f *testing.F) {
	f.Add("pid_123_luid_0x00000000_0x000012ab_phys_0_eng_2_engtype_3D")
	f.Add("pid_123_luid_0x0_0x12ab_phys_0")
	f.Add("")
	f.Fuzz(func(t *testing.T, name string) {
		n, ok := parsePerformanceGPUInstance(name)
		if !ok {
			return
		}
		canonical := fmt.Sprintf("pid_%d_%s_phys_%d", n.pid, n.adapter, n.physical)
		if n.hasEngine {
			canonical += fmt.Sprintf("_eng_%d_engtype_%s", n.engine, n.engineType)
		}
		again, valid := parsePerformanceGPUInstance(canonical)
		if !valid || again != n {
			t.Fatalf("round trip: %+v -> %q -> %+v, valid=%v", n, canonical, again, valid)
		}
	})
}
