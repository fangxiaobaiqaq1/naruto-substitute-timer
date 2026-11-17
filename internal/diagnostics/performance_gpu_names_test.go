package diagnostics

import (
	"strings"
	"testing"
	"time"
)

func TestPerformanceGPUInstanceNames(t *testing.T) {
	n, ok := parsePerformanceGPUInstance("pid_1234_luid_0x00000000_0x0000C6F5_phys_0_eng_3_engtype_VideoDecode")
	if !ok || n.pid != 1234 || n.adapter != "luid_0x00000000_0x0000c6f5" || n.engine != 3 || n.engineType != "VideoDecode" || !n.hasEngine {
		t.Fatalf("engine instance = %+v %v", n, ok)
	}
	// Some drivers leave the engine type empty; that must not reject the row.
	if n, ok := parsePerformanceGPUInstance("pid_1_luid_0x0_0x1_phys_0_eng_5_engtype_"); !ok || n.engineType != "unknown" || n.engine != 5 {
		t.Fatalf("empty engtype = %+v %v", n, ok)
	}
	if n, ok := parsePerformanceGPUInstance("pid_77_luid_0x00000000_0x0000C6F5_phys_0#2"); !ok || n.pid != 77 || n.hasEngine || n.adapterOnly {
		t.Fatalf("process memory instance = %+v %v", n, ok)
	}
	if n, ok := parsePerformanceGPUInstance("luid_0x00000000_0x0000C6F5_phys_0"); !ok || !n.adapterOnly || n.hasEngine {
		t.Fatalf("adapter memory instance = %+v %v", n, ok)
	}
	for _, bad := range []string{"", "pid_x_luid_0x0_0x1_phys_0", "pid_1_luid_0_0x1_phys_0", "pid_1_luid_0x0_0x1_phys_0_eng_x_engtype_3D",
		"pid_12", "_Total", "luid_0x0_0x1_phys_0_eng_0_engtype_3D", strings.Repeat("a", 300)} {
		if _, ok := parsePerformanceGPUInstance(bad); ok {
			t.Errorf("accepted malformed instance %q", bad)
		}
	}
}

func performanceTestGPUPlatform(pids ...int) performancePlatformSample {
	s := performancePlatformSample{Values: map[string]float64{}, Unavailable: map[string]string{}}
	for _, pid := range pids {
		s.Processes = append(s.Processes, PerformanceProcess{PID: pid, Values: map[string]float64{}, Unavailable: map[string]string{}})
		s.AllProcesses = append(s.AllProcesses, performanceSystemProcess{PID: pid})
	}
	return s
}

// One bad row, an unusual engine type or a helper process without any GPU
// instance must not void the system, tree or per-process numbers.
func TestPerformanceGPUBadRowsAndMissingInstancesKeepTheRest(t *testing.T) {
	g := newPerformanceGPUSample()
	const adapter = "luid_0x00000000_0x0000C6F5_phys_0"
	g.addEngine("pid_10_"+adapter+"_eng_0_engtype_3D", 30, 0)
	g.addEngine("pid_10_"+adapter+"_eng_1_engtype_VideoDecode", 5, 0)
	g.addEngine("pid_99_"+adapter+"_eng_0_engtype_3D", 50, 1) // Emulator on the same engine.
	g.addEngine("pid_99_"+adapter+"_eng_2_engtype_", 7, 0)    // Empty type.
	g.addEngine("garbage", 99, 0)                             // Unparseable.
	g.addEngine("pid_99_"+adapter+"_eng_3_engtype_3D", 80, 0xC0000BBA)
	g.addMemory("pid_10_"+adapter, 100<<20, 0, false)
	g.addMemory("pid_10_"+adapter, 20<<20, 0, true)
	g.addMemory("pid_99_"+adapter, 2<<30, 0, false)
	g.addMemory(adapter, 3<<30, 0, false)
	g.addMemory(adapter, 1<<30, 0, true)

	s := performanceTestGPUPlatform(10, 11) // 11: a console helper with no GPU context.
	s.AllProcesses = append(s.AllProcesses, performanceSystemProcess{PID: 99})
	performanceGPUApply(&s, g, "")
	if got := s.Values["system_gpu_busiest_engine_percent"]; got != 80 {
		t.Fatalf("system busiest = %v, want 80 (30+50 on engine 0)", got)
	}
	if got := s.Values["tree_gpu_busiest_engine_percent"]; got != 30 {
		t.Fatalf("tree busiest = %v, want 30", got)
	}
	if s.Processes[0].Values["gpu_busiest_engine_percent"] != 30 || s.Processes[0].Values["gpu_dedicated_bytes"] != 100<<20 || s.Processes[0].Values["gpu_shared_bytes"] != 20<<20 {
		t.Fatalf("root GPU values = %v", s.Processes[0].Values)
	}
	if v, ok := s.Processes[1].Values["gpu_busiest_engine_percent"]; !ok || v != 0 || s.Processes[1].Unavailable["gpu_busiest_engine_percent"] != "" {
		t.Fatalf("helper without GPU instance must read 0, got %v / %v", s.Processes[1].Values, s.Processes[1].Unavailable)
	}
	if s.Values["system_gpu_dedicated_bytes"] != 3<<30 || s.Values["system_gpu_shared_bytes"] != 1<<30 {
		t.Fatalf("adapter memory = %v", s.Values)
	}
	if !strings.Contains(s.Unavailable["gpu_rows_skipped"], "2") {
		t.Fatalf("skipped rows not reported: %v", s.Unavailable)
	}
	emulator := s.AllProcesses[2]
	if emulator.GPUPercent == nil || *emulator.GPUPercent != 50 || emulator.GPUDedicated == nil || *emulator.GPUDedicated != 2<<30 {
		t.Fatalf("emulator row = %+v", emulator)
	}
	if len(s.GPUEngines) != 2 || s.GPUEngines[0].PID != 10 {
		t.Fatalf("tree engine rows = %+v", s.GPUEngines)
	}
}

func TestPerformanceGPUUnavailableIsNeverZero(t *testing.T) {
	s := performanceTestGPUPlatform(10)
	performanceGPUApply(&s, nil, "GPU 初始化中")
	if _, ok := s.Values["system_gpu_busiest_engine_percent"]; ok || s.Unavailable["system_gpu_busiest_engine_percent"] != "GPU 初始化中" {
		t.Fatalf("unavailable GPU published a value: %v %v", s.Values, s.Unavailable)
	}
	if _, ok := s.Processes[0].Values["gpu_dedicated_bytes"]; ok || s.Processes[0].Unavailable["gpu_dedicated_bytes"] == "" {
		t.Fatalf("process GPU memory = %v %v", s.Processes[0].Values, s.Processes[0].Unavailable)
	}
	if s.AllProcesses[0].GPUPercent != nil {
		t.Fatal("system-wide row got a GPU value without data")
	}
	// Engine warm-up must not hide memory that is already valid.
	g := newPerformanceGPUSample()
	g.engineReason = "warm-up"
	g.addMemory("pid_10_luid_0x0_0x1_phys_0", 64<<20, 0, false)
	s = performanceTestGPUPlatform(10)
	performanceGPUApply(&s, g, "")
	if s.Processes[0].Values["gpu_dedicated_bytes"] != 64<<20 || s.Unavailable["tree_gpu_busiest_engine_percent"] != "warm-up" {
		t.Fatalf("partial sample = %v %v", s.Processes[0].Values, s.Unavailable)
	}
}

func performanceTestGPUEventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A slow or hung driver call may age the GPU numbers, but sample/metadata and
// close never wait for it, nothing is latched, and data returns afterwards.
func TestPerformanceGPUWorkerHungCollectDoesNotBlockOrLatch(t *testing.T) {
	slot := make(chan struct{}, 1)
	hang, closed := make(chan struct{}), make(chan struct{})
	calls := 0
	w := newPerformanceGPUWorker(slot, performanceGPUNativeOps{
		initialize: func(func() bool) PerformanceSystem {
			return PerformanceSystem{GPUs: []PerformanceGPU{{Name: "Test GPU"}}}
		},
		collect: func(func() bool) *performanceGPUSample {
			calls++
			if calls == 2 {
				<-hang // The driver stalls on the second read.
			}
			return newPerformanceGPUSample()
		},
		close: func() { close(closed) },
	}, 10*time.Millisecond)
	if _, reason := w.sample(); reason == "" {
		// Possible only if the worker already produced a sample; either is fine.
		t.Log("worker was ready immediately")
	}
	performanceTestGPUEventually(t, "first sample", func() bool { g, reason := w.sample(); return g != nil && reason == "" })
	if info, reason := w.metadata(); reason != "" || len(info.GPUs) != 1 {
		t.Fatalf("metadata = %+v %q", info, reason)
	}
	// While the call hangs the cached sample ages; sample() reports it stale
	// without blocking.
	performanceTestGPUEventually(t, "stale report", func() bool {
		started := time.Now()
		_, reason := w.sample()
		if time.Since(started) > 500*time.Millisecond {
			t.Fatal("sample blocked on a hung native call")
		}
		return strings.Contains(reason, "未更新")
	})
	close(hang) // The driver recovers: data must flow again, with no latch.
	performanceTestGPUEventually(t, "recovery", func() bool { g, reason := w.sample(); return g != nil && reason == "" })
	w.close()
	w.close()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("native cleanup did not run")
	}
	<-w.done
	if len(slot) != 0 {
		t.Fatal("native slot not released")
	}
	if _, reason := w.sample(); reason == "" {
		t.Fatal("stopped worker still reports data")
	}
}

// Stop followed immediately by Start must get GPU data: the new worker waits
// for the old one's cleanup instead of giving up on the occupied slot.
func TestPerformanceGPUWorkerRestartWaitsForSlot(t *testing.T) {
	slot := make(chan struct{}, 1)
	release := make(chan struct{})
	ops := func(closeWait chan struct{}) performanceGPUNativeOps {
		return performanceGPUNativeOps{
			initialize: func(func() bool) PerformanceSystem { return PerformanceSystem{} },
			collect:    func(func() bool) *performanceGPUSample { return newPerformanceGPUSample() },
			close: func() {
				if closeWait != nil {
					<-closeWait
				}
			},
		}
	}
	first := newPerformanceGPUWorker(slot, ops(release), 5*time.Millisecond)
	performanceTestGPUEventually(t, "first worker", func() bool { g, _ := first.sample(); return g != nil })
	first.close() // Cleanup is still running...
	second := newPerformanceGPUWorker(slot, ops(nil), 5*time.Millisecond)
	if _, reason := second.sample(); reason == "" {
		t.Fatal("second worker had data before the slot was free")
	}
	close(release)
	performanceTestGPUEventually(t, "second worker", func() bool { g, reason := second.sample(); return g != nil && reason == "" })
	second.close()
	<-second.done
	// A worker stopped while still waiting for the slot never runs native code.
	slot <- struct{}{}
	waiting := newPerformanceGPUWorker(slot, performanceGPUNativeOps{
		initialize: func(func() bool) PerformanceSystem {
			t.Error("initialize ran without the slot")
			return PerformanceSystem{}
		},
		collect: func(func() bool) *performanceGPUSample { return nil },
		close:   func() { t.Error("close ran without the slot") },
	}, time.Millisecond)
	waiting.close()
	<-waiting.done
}

func TestPerformanceGPUWorkerPanicIsReported(t *testing.T) {
	slot := make(chan struct{}, 1)
	w := newPerformanceGPUWorker(slot, performanceGPUNativeOps{
		initialize: func(func() bool) PerformanceSystem { panic("driver exploded") },
		collect:    func(func() bool) *performanceGPUSample { return nil },
		close:      func() {},
	}, time.Millisecond)
	<-w.done
	if _, reason := w.sample(); !strings.Contains(reason, "driver exploded") {
		t.Fatalf("panic reason = %q", reason)
	}
	if len(slot) != 0 {
		t.Fatal("slot leaked after panic")
	}
}

func FuzzPerformanceGPUInstance(f *testing.F) {
	for _, seed := range []string{"pid_1_luid_0x0_0x1_phys_0_eng_0_engtype_3D", "pid_1_luid_0x0_0x1_phys_0", "luid_0x0_0x1_phys_0", "pid__#", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		n, ok := parsePerformanceGPUInstance(name)
		if ok && (n.pid < 0 || n.adapter == "" || len(n.engineType) > 64*4) {
			t.Fatalf("accepted %q as %+v", name, n)
		}
	})
}
