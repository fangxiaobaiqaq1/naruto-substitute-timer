package diagnostics

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const performanceMaxGPUEngines = 256

// PDH GPU instance names look like
//
//	pid_1234_luid_0x00000000_0x0000C6F5_phys_0_eng_3_engtype_3D   (GPU Engine)
//	pid_1234_luid_0x00000000_0x0000C6F5_phys_0                    (GPU Process Memory)
//	luid_0x00000000_0x0000C6F5_phys_0                             (GPU Adapter Memory)
//
// Parse the PID exactly (pid_12 must not match pid_123). A physical engine is
// NOT its engtype: there can be several Copy/3D engines on each adapter, and
// some drivers leave the engtype empty.
type performanceGPUInstance struct {
	pid         int
	adapter     string
	physical    uint32
	engine      uint32
	engineType  string
	hasEngine   bool
	adapterOnly bool
}

func parsePerformanceGPUInstance(name string) (performanceGPUInstance, bool) {
	var n performanceGPUInstance
	if len(name) > 256 {
		return n, false
	}
	// PDH disambiguates otherwise identical instance names with #<index>.
	if at := strings.LastIndexByte(name, '#'); at >= 0 {
		if _, ok := performanceGPUDecimal(name[at+1:]); !ok {
			return n, false
		}
		name = name[:at]
	}
	if strings.HasPrefix(name, "luid_") { // Adapter-level instance: no process.
		name, n.adapterOnly = "pid_0_"+name, true
	}
	parts := strings.SplitN(name, "_", 11)
	if (len(parts) != 7 && len(parts) != 11) || parts[0] != "pid" || parts[2] != "luid" || parts[5] != "phys" {
		return n, false
	}
	pid, ok := performanceGPUDecimal(parts[1])
	if !ok || uint64(pid) > uint64(^uint(0)>>1) {
		return n, false
	}
	hi, ok := performanceGPUHex(parts[3])
	if !ok {
		return n, false
	}
	lo, ok := performanceGPUHex(parts[4])
	if !ok {
		return n, false
	}
	physical, ok := performanceGPUDecimal(parts[6])
	if !ok {
		return n, false
	}
	n.pid, n.physical = int(pid), physical
	n.adapter = fmt.Sprintf("luid_0x%08x_0x%08x", hi, lo)
	if len(parts) == 11 {
		if parts[7] != "eng" || parts[9] != "engtype" || n.adapterOnly {
			return performanceGPUInstance{}, false
		}
		engine, ok := performanceGPUDecimal(parts[8])
		if !ok {
			return performanceGPUInstance{}, false
		}
		// The type is only a display label; the grouping key is luid+phys+eng.
		n.engine, n.engineType, n.hasEngine = engine, cleanPerformanceText(parts[10], 64), true
		if n.engineType == "" {
			n.engineType = "unknown"
		}
	}
	return n, true
}

func performanceGPUDecimal(s string) (uint32, bool) {
	if s == "" || len(s) > 10 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 32)
	return uint32(n), err == nil
}

func performanceGPUHex(s string) (uint32, bool) {
	if len(s) < 3 || len(s) > 10 || !(strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
		return 0, false
	}
	n, err := strconv.ParseUint(s[2:], 16, 32)
	return uint32(n), err == nil
}

func performanceGPUValidNumber(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

type performanceGPUHardwareEngine struct {
	adapter          string
	physical, engine uint32
}

type performanceGPUProcessEngine struct {
	pid int
	performanceGPUHardwareEngine
}

type performanceGPUEngineValue struct {
	percent    float64
	engineType string
}

// performanceGPUSample is one system-wide GPU observation: every process's
// engine utilisation and GPU memory, plus adapter-level memory. A row that
// cannot be parsed or carries an invalid status is skipped and counted; it
// never voids the rest of the sample.
type performanceGPUSample struct {
	at                              time.Time
	engineReason                    string // Non-empty: no engine utilisation in this sample.
	memoryReason                    string // Non-empty: no per-process GPU memory in this sample.
	adapterReason                   string // Non-empty: no adapter-level memory in this sample.
	skipped                         int
	engines                         map[performanceGPUProcessEngine]performanceGPUEngineValue
	dedicated                       map[int]float64
	shared                          map[int]float64
	adapterDedicated, adapterShared float64
	hasAdapter                      bool
}

func newPerformanceGPUSample() *performanceGPUSample {
	return &performanceGPUSample{at: time.Now(), engines: make(map[performanceGPUProcessEngine]performanceGPUEngineValue),
		dedicated: make(map[int]float64), shared: make(map[int]float64)}
}

// status is the PDH CStatus of the row: 0 (valid) and 1 (new data) are usable.
func (g *performanceGPUSample) addEngine(name string, value float64, status uint32) {
	n, ok := parsePerformanceGPUInstance(name)
	if !ok || !n.hasEngine || status > 1 || !performanceGPUValidNumber(value) {
		g.skipped++
		return
	}
	key := performanceGPUProcessEngine{n.pid, performanceGPUHardwareEngine{n.adapter, n.physical, n.engine}}
	if _, exists := g.engines[key]; !exists && len(g.engines) >= 16384 {
		g.skipped++
		return
	}
	row := g.engines[key]
	row.percent, row.engineType = row.percent+value, n.engineType
	g.engines[key] = row
}

func (g *performanceGPUSample) addMemory(name string, value float64, status uint32, shared bool) {
	n, ok := parsePerformanceGPUInstance(name)
	if !ok || n.hasEngine || status > 1 || !performanceGPUValidNumber(value) {
		g.skipped++
		return
	}
	switch {
	case n.adapterOnly && shared:
		g.adapterShared, g.hasAdapter = g.adapterShared+value, true
	case n.adapterOnly:
		g.adapterDedicated, g.hasAdapter = g.adapterDedicated+value, true
	case shared:
		g.shared[n.pid] += value
	default:
		g.dedicated[n.pid] += value
	}
}

// busiest returns each process's busiest engine and the busiest hardware
// engine among the processes accepted by keep (nil keeps all).
func (g *performanceGPUSample) busiest(keep func(pid int) bool) (perProcess map[int]float64, overall float64) {
	perProcess = make(map[int]float64)
	hardware := make(map[performanceGPUHardwareEngine]float64)
	for key, v := range g.engines {
		if keep != nil && !keep(key.pid) {
			continue
		}
		perProcess[key.pid] = max(perProcess[key.pid], min(100, v.percent))
		hardware[key.performanceGPUHardwareEngine] += v.percent
	}
	for _, v := range hardware {
		overall = max(overall, min(100, v))
	}
	return perProcess, overall
}

// performanceGPUApply merges a GPU observation into the CPU/process sample.
// Once the counters are warm, a process WITHOUT any GPU instance holds no GPU
// context: that is a measured 0, not a gap. reason is non-empty when there is
// no usable observation at all.
func performanceGPUApply(s *performancePlatformSample, g *performanceGPUSample, reason string) {
	const engineKey, dedicatedKey, sharedKey = "gpu_busiest_engine_percent", "gpu_dedicated_bytes", "gpu_shared_bytes"
	unavailable := func(key, reason string, system ...string) {
		for i := range s.Processes {
			s.Processes[i].Unavailable[key] = reason
		}
		for _, name := range system {
			s.Unavailable[name] = reason
		}
	}
	if g == nil || reason != "" {
		if reason == "" {
			reason = "GPU 数据不可用"
		}
		unavailable(engineKey, reason, "system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent")
		unavailable(dedicatedKey, reason, "system_gpu_dedicated_bytes")
		unavailable(sharedKey, reason, "system_gpu_shared_bytes")
		return
	}
	tree := make(map[int]bool, len(s.Processes))
	for _, p := range s.Processes {
		tree[p.PID] = true
	}
	if g.skipped > 0 {
		s.Unavailable["gpu_rows_skipped"] = fmt.Sprintf("忽略了 %d 行无法识别或状态无效的 GPU 计数器实例（其余数据有效）", g.skipped)
	}
	var perProcess map[int]float64
	if g.engineReason != "" {
		unavailable(engineKey, g.engineReason, "system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent")
	} else {
		var system float64
		perProcess, system = g.busiest(nil)
		_, ours := g.busiest(func(pid int) bool { return tree[pid] })
		s.Values["system_gpu_busiest_engine_percent"], s.Values["tree_gpu_busiest_engine_percent"] = system, ours
		for i := range s.Processes {
			s.Processes[i].Values[engineKey] = perProcess[s.Processes[i].PID]
		}
		for key, v := range g.engines {
			if tree[key.pid] && len(s.GPUEngines) < performanceMaxGPUEngines {
				s.GPUEngines = append(s.GPUEngines, PerformanceGPUEngine{PID: key.pid, Adapter: key.adapter,
					Engine: fmt.Sprintf("phys_%d_eng_%d_engtype_%s", key.physical, key.engine, v.engineType), Percent: min(100, v.percent)})
			}
		}
		sort.Slice(s.GPUEngines, func(i, j int) bool {
			a, b := s.GPUEngines[i], s.GPUEngines[j]
			if a.PID != b.PID {
				return a.PID < b.PID
			}
			if a.Adapter != b.Adapter {
				return a.Adapter < b.Adapter
			}
			return a.Engine < b.Engine
		})
	}
	if g.memoryReason != "" {
		unavailable(dedicatedKey, g.memoryReason)
		unavailable(sharedKey, g.memoryReason)
	} else {
		for i := range s.Processes {
			p := &s.Processes[i]
			p.Values[dedicatedKey], p.Values[sharedKey] = g.dedicated[p.PID], g.shared[p.PID]
		}
	}
	switch {
	case g.adapterReason != "":
		s.Unavailable["system_gpu_dedicated_bytes"], s.Unavailable["system_gpu_shared_bytes"] = g.adapterReason, g.adapterReason
	case g.hasAdapter:
		s.Values["system_gpu_dedicated_bytes"], s.Values["system_gpu_shared_bytes"] = g.adapterDedicated, g.adapterShared
	default:
		const none = "没有 GPU Adapter Memory 实例"
		s.Unavailable["system_gpu_dedicated_bytes"], s.Unavailable["system_gpu_shared_bytes"] = none, none
	}
	for i := range s.AllProcesses {
		p := &s.AllProcesses[i]
		if perProcess != nil {
			v := perProcess[p.PID]
			p.GPUPercent = &v
		}
		if g.memoryReason == "" {
			v := g.dedicated[p.PID]
			p.GPUDedicated = &v
		}
	}
}

// The only production native GPU slot is shared across all recorder sessions:
// a stuck PDH/DXGI call keeps the slot instead of leaking another goroutine
// and query on every restart.
var performanceGPUNativeSlot = make(chan struct{}, 1)

// Kept platform-independent so the worker lifecycle is testable on Linux. The
// Windows worker supplies all three thunks; only the worker goroutine invokes
// them. canceled reports that the session is stopping.
type performanceGPUNativeOps struct {
	initialize func(canceled func() bool) PerformanceSystem
	collect    func(canceled func() bool) *performanceGPUSample
	close      func()
}

// performanceGPUWorker samples the GPU on its own cadence and publishes the
// latest observation. The CPU/memory sampler only reads that cached value, so
// a slow or hung driver call can delay or age the GPU numbers but can never
// stall, void or permanently disable the rest of the recording.
type performanceGPUWorker struct {
	mu       sync.Mutex
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	interval time.Duration
	created  time.Time
	ready    bool
	failed   string
	info     PerformanceSystem
	latest   *performanceGPUSample
}

func newPerformanceGPUWorker(slot chan struct{}, ops performanceGPUNativeOps, interval time.Duration) *performanceGPUWorker {
	w := &performanceGPUWorker{stop: make(chan struct{}), done: make(chan struct{}), interval: interval, created: time.Now()}
	go w.run(slot, ops)
	return w
}

func (w *performanceGPUWorker) stopped() bool {
	select {
	case <-w.stop:
		return true
	default:
		return false
	}
}

func (w *performanceGPUWorker) run(slot chan struct{}, ops performanceGPUNativeOps) {
	defer close(w.done)
	// The previous session's worker releases the slot right after its native
	// cleanup; wait for that instead of giving up on a quick stop/start.
	wait := time.NewTimer(10 * time.Second)
	defer wait.Stop()
	select {
	case slot <- struct{}{}:
	case <-w.stop:
		return
	case <-wait.C:
		w.mu.Lock()
		w.failed = "上一次性能监测的 GPU 原生调用仍未返回，本次无 GPU 数据"
		w.mu.Unlock()
		return
	}
	defer func() { <-slot }()
	defer func() {
		if v := recover(); v != nil {
			w.mu.Lock()
			w.failed = fmt.Sprintf("GPU 采集异常：%v", v)
			w.mu.Unlock()
		}
	}()
	defer ops.close()
	info := ops.initialize(w.stopped)
	w.mu.Lock()
	w.info, w.ready = info, true
	w.mu.Unlock()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for !w.stopped() {
		if sample := ops.collect(w.stopped); sample != nil {
			w.mu.Lock()
			w.latest = sample
			w.mu.Unlock()
		}
		select {
		case <-w.stop:
			return
		case <-ticker.C:
		}
	}
}

// metadata never waits for a native call.
func (w *performanceGPUWorker) metadata() (PerformanceSystem, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	info := PerformanceSystem{GPUs: append([]PerformanceGPU(nil), w.info.GPUs...), Unavailable: make(map[string]string, len(w.info.Unavailable))}
	for key, value := range w.info.Unavailable {
		info.Unavailable[key] = value
	}
	return info, w.statusLocked()
}

func (w *performanceGPUWorker) statusLocked() string {
	switch {
	case w.failed != "":
		return w.failed
	case w.stopped():
		return "GPU 采集已停止"
	case !w.ready:
		if waited := time.Since(w.created); waited > 5*time.Second {
			return fmt.Sprintf("GPU 初始化已 %.0f 秒未完成（驱动或性能计数器响应慢）", waited.Seconds())
		}
		return "GPU 初始化中"
	}
	return ""
}

// sample returns the latest observation, or the reason there is none. It
// never waits: a stale observation is reported as stale, with its age.
func (w *performanceGPUWorker) sample() (*performanceGPUSample, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if reason := w.statusLocked(); reason != "" {
		return nil, reason
	}
	if w.latest == nil {
		return nil, "等待首个 GPU 样本"
	}
	if age := time.Since(w.latest.at); age > 3*w.interval+time.Second {
		return nil, fmt.Sprintf("GPU 数据已 %.0f 秒未更新（驱动或性能计数器响应慢）", age.Seconds())
	}
	return w.latest, ""
}

// close only signals: the worker closes its query after the native call in
// flight returns. No join, so a hung driver call cannot block Stop.
func (w *performanceGPUWorker) close() {
	w.stopOnce.Do(func() { close(w.stop) })
}
