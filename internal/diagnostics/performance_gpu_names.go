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

// PDH instance names contain only a PID, an ephemeral LUID and engine indices.
// Do not use substring PID matching (pid_12 also matches pid_123), or retain
// unrecognised instance names. A physical engine is NOT its engtype: there can
// be several independent Copy/3D engines on each of several adapters.
type performanceGPUInstance struct {
	pid        int
	adapter    string
	physical   uint32
	engine     uint32
	engineType string
	hasEngine  bool
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
		if parts[7] != "eng" || parts[9] != "engtype" || len(parts[10]) == 0 || len(parts[10]) > 64 {
			return performanceGPUInstance{}, false
		}
		engine, ok := performanceGPUDecimal(parts[8])
		if !ok {
			return performanceGPUInstance{}, false
		}
		for _, c := range parts[10] {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == ' ') {
				return performanceGPUInstance{}, false
			}
		}
		n.engine, n.engineType, n.hasEngine = engine, parts[10], true
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

// Only hardware-engine totals are retained for other applications, never their
// PID/instance rows. selected contains filtered, identity-checked PIDs only.
// The two bounds are independent: a selected-row cap must not undercount the
// system's busiest engine, or silently turn a partial tree into a complete one.
type performanceGPUEngineTotals struct {
	system            map[performanceGPUHardwareEngine]float64
	selected          map[performanceGPUProcessEngine]performanceGPUEngineValue
	systemTruncated   bool
	selectedTruncated bool
}

func (a *performanceGPUEngineTotals) add(n performanceGPUInstance, v float64, selected bool) {
	if a.system == nil {
		a.system = make(map[performanceGPUHardwareEngine]float64)
		a.selected = make(map[performanceGPUProcessEngine]performanceGPUEngineValue)
	}
	key := performanceGPUHardwareEngine{n.adapter, n.physical, n.engine}
	if _, exists := a.system[key]; exists || len(a.system) < 1024 {
		a.system[key] += v
	} else {
		a.systemTruncated = true
	}
	if !selected {
		return
	}
	pk := performanceGPUProcessEngine{n.pid, key}
	if _, exists := a.selected[pk]; !exists && len(a.selected) >= performanceMaxGPUEngines {
		a.selectedTruncated = true
		return
	}
	row := a.selected[pk]
	row.percent += v
	row.engineType = n.engineType
	a.selected[pk] = row
}

func (a *performanceGPUEngineTotals) systemBusiest() (float64, bool) {
	if len(a.system) == 0 || a.systemTruncated {
		return 0, false
	}
	var busiest float64
	for _, v := range a.system {
		busiest = max(busiest, v)
	}
	return min(100, busiest), true
}

func (a *performanceGPUEngineTotals) selectedResults() ([]PerformanceGPUEngine, map[int]float64, float64) {
	rows := make([]PerformanceGPUEngine, 0, len(a.selected))
	process := make(map[int]float64)
	tree := make(map[performanceGPUHardwareEngine]float64)
	for key, v := range a.selected {
		rows = append(rows, PerformanceGPUEngine{
			PID: key.pid, Adapter: key.adapter,
			Engine:  fmt.Sprintf("phys_%d_eng_%d_engtype_%s", key.physical, key.engine, v.engineType),
			Percent: min(100, v.percent),
		})
		process[key.pid] = max(process[key.pid], min(100, v.percent))
		tree[key.performanceGPUHardwareEngine] += v.percent
	}
	var busiest float64
	for _, v := range tree {
		busiest = max(busiest, min(100, v))
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.PID != b.PID {
			return a.PID < b.PID
		}
		if a.Adapter != b.Adapter {
			return a.Adapter < b.Adapter
		}
		return a.Engine < b.Engine
	})
	return rows, process, busiest
}

// A tree value requires valid coverage for EVERY selected identity. A valid
// zero from one process is not evidence that another process used zero GPU.
func performanceGPUApplyEngineTotals(s *performancePlatformSample, totals *performanceGPUEngineTotals, invalid map[int]string, systemReason, treeReason string, malformed bool) {
	const key = "gpu_busiest_engine_percent"
	const noData = "no matching GPU Engine instances; absence is not measured zero"
	if totals.systemTruncated {
		systemReason = "system GPU hardware-engine aggregation exceeded 1024-engine bound"
	}
	if busiest, ok := totals.systemBusiest(); ok && systemReason == "" {
		s.Values["system_gpu_busiest_engine_percent"] = busiest
	} else {
		if systemReason == "" {
			systemReason = "no valid GPU Engine data (driver/counters may be unavailable)"
		}
		s.Unavailable["system_gpu_busiest_engine_percent"] = systemReason
	}
	rows, process, tree := totals.selectedResults()
	s.GPUEngines = rows
	if totals.selectedTruncated {
		treeReason = "selected GPU engine instances limited to 256; incomplete aggregates omitted"
	}
	covered := 0
	for i := range s.Processes {
		p := &s.Processes[i]
		p.Unavailable[key] = noData
		if reason := invalid[p.PID]; reason != "" {
			p.Unavailable[key] = reason
		} else if totals.selectedTruncated || malformed {
			p.Unavailable[key] = treeReason
		} else if value, ok := process[p.PID]; ok {
			p.Values[key] = value
			delete(p.Unavailable, key)
			covered++
		}
	}
	if covered == len(s.Processes) && covered != 0 && treeReason == "" {
		s.Values["tree_gpu_busiest_engine_percent"] = tree
	} else {
		if treeReason == "" {
			treeReason = fmt.Sprintf("GPU Engine coverage incomplete: %d/%d selected process identities have valid data; tree aggregate omitted", covered, len(s.Processes))
		}
		delete(s.Values, "tree_gpu_busiest_engine_percent")
		s.Unavailable["tree_gpu_busiest_engine_percent"] = treeReason
		s.Unavailable["gpu_engines"] = treeReason
	}
}

// Merge only GPU fields, preserving the caller's adjacent CPU/IO timestamps and
// counters. Even a timely worker reply may only describe the requested StartIDs.
func performanceGPUApplySample(s *performancePlatformSample, result performancePlatformSample) {
	const mismatch = "GPU sample process identity mismatch; result discarded"
	valid := len(s.Processes) == len(result.Processes)
	for i := range s.Processes {
		if !valid || s.Processes[i].PID != result.Processes[i].PID || s.Processes[i].StartID != result.Processes[i].StartID {
			valid = false
			break
		}
	}
	for _, key := range []string{"system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent", "gpu_engines", "gpu_busiest_engine_percent", "gpu_dedicated_bytes", "gpu_shared_bytes"} {
		if !valid {
			delete(s.Values, key)
			s.Unavailable[key] = mismatch
			continue
		}
		if value, ok := result.Values[key]; ok {
			s.Values[key] = value
		}
		if reason := result.Unavailable[key]; reason != "" {
			s.Unavailable[key] = reason
		}
	}
	for i := range s.Processes {
		p := &s.Processes[i]
		for _, key := range []string{"gpu_busiest_engine_percent", "gpu_dedicated_bytes", "gpu_shared_bytes"} {
			if !valid {
				delete(p.Values, key)
				p.Unavailable[key] = mismatch
				continue
			}
			if value, ok := result.Processes[i].Values[key]; ok {
				p.Values[key] = value
			}
			if reason := result.Processes[i].Unavailable[key]; reason != "" {
				p.Unavailable[key] = reason
			}
		}
	}
	s.GPUEngines = nil
	if valid {
		selected := make(map[int]bool, len(s.Processes))
		for _, p := range s.Processes {
			selected[p.PID] = true
		}
		for _, row := range result.GPUEngines {
			if !selected[row.PID] {
				continue
			}
			if len(s.GPUEngines) == performanceMaxGPUEngines {
				break
			}
			s.GPUEngines = append(s.GPUEngines, row)
		}
	}
}

// The only production native GPU slot is shared across all recorder sessions.
// Acquire BEFORE starting a goroutine. A stuck init/collect/close retains this
// slot forever rather than leaking another goroutine/query on every restart.
var performanceGPUNativeSlot = make(chan struct{}, 1)

const (
	performanceGPUInitTimeout = 3 * time.Second
	performanceGPUReadTimeout = 200 * time.Millisecond
)

// Kept platform-independent so blocked-native-call lifecycle tests run on Linux
// too. The Windows worker supplies all three thunks; only its owner invokes
// them, including close. No mutex is held across a thunk/native call.
type performanceGPUNativeOps struct {
	initialize func(canceled func() bool) PerformanceSystem
	collect    func(*performancePlatformSample, func() bool)
	close      func()
}

type performanceGPURequest struct {
	deadline time.Time
	sample   performancePlatformSample // Private GPU-only maps, never the CPU caller's maps.
	reply    chan performancePlatformSample
}

type performanceGPUWorker struct {
	mu           sync.Mutex
	stop         chan struct{}
	done         chan struct{}
	requests     chan *performanceGPURequest
	initDeadline time.Time
	readTimeout  time.Duration
	ready        bool
	pending      bool
	disabled     string            // Latched for this session, including after a late native return.
	info         PerformanceSystem // Immutable after timely initialization.
}

func newPerformanceGPUWorker(slot chan struct{}, ops performanceGPUNativeOps, initTimeout, readTimeout time.Duration) *performanceGPUWorker {
	w := &performanceGPUWorker{
		stop: make(chan struct{}), done: make(chan struct{}), requests: make(chan *performanceGPURequest, 1),
		initDeadline: time.Now().Add(initTimeout), readTimeout: readTimeout,
	}
	select {
	case slot <- struct{}{}:
		go w.run(slot, ops)
	default:
		w.disableLocked("GPU native worker slot occupied by an earlier session; GPU disabled for this session")
		close(w.done) // No goroutine or native initialization was started.
	}
	return w
}

func (w *performanceGPUWorker) run(slot chan struct{}, ops performanceGPUNativeOps) {
	// Release only AFTER native cleanup returns. If cleanup itself blocks, the
	// global slot stays occupied. A recover also prevents Go-side native wrapper
	// panics from crashing CPU sampling; no panic payload/paths are reported.
	defer func() {
		if recover() != nil {
			w.mu.Lock()
			w.disableLocked("GPU native worker failed; GPU disabled for this session")
			w.mu.Unlock()
		}
		<-slot
		close(w.done)
	}()
	defer ops.close()
	w.mu.Lock()
	w.statusLocked()
	stopped := w.disabled != ""
	w.mu.Unlock()
	if stopped {
		return
	}
	info := ops.initialize(w.initializationCanceled)
	w.mu.Lock()
	w.statusLocked()
	if w.disabled != "" {
		w.mu.Unlock()
		return // Timed out or stopped while inside DXGI/PDH initialization.
	}
	w.info, w.ready = info, true
	w.mu.Unlock()
	for {
		select {
		case <-w.stop:
			return
		case request := <-w.requests:
			if !w.requestActive(request) {
				return
			}
			ops.collect(&request.sample, func() bool { return !w.requestActive(request) })
			w.mu.Lock()
			if w.requestActiveLocked(request) {
				// Buffered reply: a canceled/timed-out caller never strands the
				// owner here, and no following native call reuses these maps.
				select {
				case request.reply <- request.sample:
				default:
					w.disableLocked("GPU result channel occupied; GPU disabled for this session")
				}
			}
			stopped := w.disabled != ""
			w.mu.Unlock()
			if stopped {
				return
			}
		}
	}
}

func (w *performanceGPUWorker) disableLocked(reason string) {
	if w.disabled == "" {
		w.disabled = reason
		close(w.stop)
	}
}

func (w *performanceGPUWorker) statusLocked() string {
	if w.disabled == "" && !w.ready && !time.Now().Before(w.initDeadline) {
		w.disableLocked("GPU initialization timed out; GPU disabled for this session")
	}
	if w.disabled != "" {
		return w.disabled
	}
	if !w.ready {
		return "GPU initialization pending"
	}
	return ""
}

func (w *performanceGPUWorker) requestActiveLocked(request *performanceGPURequest) bool {
	if w.disabled == "" && !time.Now().Before(request.deadline) {
		w.disableLocked("GPU sampling timed out; GPU disabled for this session")
	}
	return w.disabled == ""
}

func (w *performanceGPUWorker) initializationCanceled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.statusLocked()
	return w.disabled != ""
}

func (w *performanceGPUWorker) requestActive(request *performanceGPURequest) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.requestActiveLocked(request)
}

// metadata never waits for initialization, a sample, or native cleanup. The
// small state mutex is never held by the native thunks or by a channel wait.
func (w *performanceGPUWorker) metadata() (PerformanceSystem, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	reason := w.statusLocked()
	info := PerformanceSystem{GPUs: append([]PerformanceGPU(nil), w.info.GPUs...), Unavailable: make(map[string]string, len(w.info.Unavailable))}
	for key, value := range w.info.Unavailable {
		info.Unavailable[key] = value
	}
	return info, reason
}

func (w *performanceGPUWorker) sample(processes []PerformanceProcess) (performancePlatformSample, string) {
	w.mu.Lock()
	reason := w.statusLocked()
	if reason == "" && w.pending {
		reason = "GPU sample already in flight"
	}
	if reason != "" {
		w.mu.Unlock()
		return performancePlatformSample{}, reason
	}
	w.pending = true
	deadline := time.Now().Add(w.readTimeout)
	w.mu.Unlock()
	defer func() { w.mu.Lock(); w.pending = false; w.mu.Unlock() }()
	request := &performanceGPURequest{
		deadline: deadline, reply: make(chan performancePlatformSample, 1),
		sample: performancePlatformSample{Values: make(map[string]float64), Unavailable: make(map[string]string)},
	}
	// Send identity only, not names, CPU counters or maps owned by the caller.
	for _, p := range processes[:min(len(processes), 64)] {
		request.sample.Processes = append(request.sample.Processes, PerformanceProcess{
			PID: p.PID, StartID: p.StartID, Values: make(map[string]float64), Unavailable: make(map[string]string),
		})
	}
	timer := time.NewTimer(max(0, time.Until(deadline)))
	defer timer.Stop()
	select {
	case w.requests <- request:
	case <-w.stop:
	case <-timer.C:
		w.mu.Lock()
		w.disableLocked("GPU sampling timed out; GPU disabled for this session")
		reason = w.disabled
		w.mu.Unlock()
		return performancePlatformSample{}, reason
	}
	select {
	case result := <-request.reply:
		// Recheck even when the timer and reply are both ready: late or stopped
		// session results must never be published to the current CPU sample.
		w.mu.Lock()
		active := w.requestActiveLocked(request)
		reason = w.disabled
		w.mu.Unlock()
		if active {
			return result, ""
		}
		return performancePlatformSample{}, reason
	case <-timer.C:
		w.mu.Lock()
		w.disableLocked("GPU sampling timed out; GPU disabled for this session")
		reason = w.disabled
		w.mu.Unlock()
	case <-w.stop:
		w.mu.Lock()
		reason = w.disabled
		w.mu.Unlock()
	}
	return performancePlatformSample{}, reason
}

func (w *performanceGPUWorker) close() {
	w.mu.Lock()
	w.disableLocked("GPU collector stopped")
	w.mu.Unlock()
	// The owner alone closes the query after its native call returns. No join,
	// concurrent PdhCloseQuery, per-close goroutine or forced native termination.
}
