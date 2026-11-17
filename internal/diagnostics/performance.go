package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"runtime/pprof"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"narutotimer/internal/frame"
)

type performanceLimits struct {
	interval time.Duration
	samples  int
	bytes    int64
}

type PerformanceRecorder struct {
	mu                         sync.Mutex
	exportMu                   sync.Mutex
	stopOnce                   sync.Once
	stop                       chan struct{}
	done                       chan struct{}
	file                       *os.File
	collector                  performanceCollector
	options                    PerformanceOptions
	limits                     performanceLimits
	metadata                   PerformanceMetadata
	status                     PerformanceStatus
	started                    time.Time
	previous                   time.Time
	prevHost                   performancePlatformSample
	prevMem                    runtime.MemStats
	prevGCCPU                  float64
	prevAttempts, prevAnalyzed uint64
	app                        PerformanceApplication
	capture, analysis, attempt performanceWindow
	loop, post, uiQueue        performanceWindow
	windowSlow, windowErrors   uint64
	uiAt                       time.Time
	markers                    []string
	background                 sync.WaitGroup // CPU profile goroutines; joined by finish.
	cpuProfiling               atomic.Bool
	cpuMarks                   int
}

// performanceWindow keeps one sample window of per-frame durations. The
// maximum is exact even when more than 2048 frames arrive; P95 is then partial.
type performanceWindow struct {
	values []float64
	max    float64
	count  int
}

func (w *performanceWindow) add(v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return
	}
	w.count++
	w.max = max(w.max, v)
	if len(w.values) < 2048 {
		w.values = append(w.values, v)
	}
}

// take returns the window maximum and P95 (nil when empty) and resets it.
func (w *performanceWindow) take() (peak, high *float64) {
	if w.count > 0 {
		v := w.max
		peak, high = &v, p95(w.values)
	}
	w.values, w.max, w.count = w.values[:0], 0, 0
	return peak, high
}

// NewPerformance starts a separate, image-free, one-second resource session.
// Root is the debug directory; a new performance/perf-* child is created.
// Native setup and optional profiles can take time: call this off the UI thread.
func NewPerformance(options PerformanceOptions) (*PerformanceRecorder, error) {
	return newPerformance(options, newPerformanceCollector, performanceLimits{PerformanceInterval, PerformanceMaxSamples, PerformanceMaxBytes})
}

func newPerformance(options PerformanceOptions, factory func(int, string) (performanceCollector, error), limits performanceLimits) (*PerformanceRecorder, error) {
	if limits.interval <= 0 || limits.samples < 1 || limits.bytes < 1 {
		return nil, errors.New("invalid performance recording limits")
	}
	root := options.Root
	if root == "" {
		root = "debug"
	}
	root = filepath.Join(root, "performance")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	started := time.Now()
	dir, err := os.MkdirTemp(root, "perf-"+started.Format("20060102-150405")+"-")
	if err != nil {
		return nil, err
	}
	collector, err := factory(os.Getpid(), dir)
	if err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "samples.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		collector.Close()
		_ = os.Remove(dir)
		return nil, err
	}
	if len(options.Config) > 0 {
		// Best effort: a report without the config is still a report.
		_ = atomicWrite(filepath.Join(dir, "config.json"), options.Config)
	}
	system := normalizedPerformanceSystem(collector.System())
	r := &PerformanceRecorder{stop: make(chan struct{}), done: make(chan struct{}), file: file, collector: collector,
		options: options, limits: limits, started: started,
		status: PerformanceStatus{Directory: dir, Active: true, StartedAt: started, ProfileStatus: make(map[string]string)},
		metadata: PerformanceMetadata{SchemaVersion: 1, Version: options.Version, StartedAt: started, RootPID: os.Getpid(),
			GoVersion: runtime.Version(), Build: performanceBuildInfo(), System: system, Settings: options.Settings, IntervalMS: int(limits.interval.Milliseconds()),
			MaxSamples: limits.samples, MaxBytes: limits.bytes, ProfilesRequested: options.Profiles, Notes: []string{
				"CPU 百分比按全部逻辑核归一（与任务管理器“进程”页一致）：100% 表示所有核心满载。",
				"空白/不可用表示没有测到，不是 0；每个进程的 CPU 速率需要连续两次采样。",
				"GPU 为最忙引擎的占用，不把不同引擎相加；计时器内存合计可能重复计算共享页。",
			}}}
	data, err := json.MarshalIndent(r.metadata, "", "  ")
	if err == nil {
		err = atomicWrite(filepath.Join(dir, "metadata.json"), data)
	}
	if err != nil {
		_ = file.Close()
		collector.Close()
		return nil, err
	}
	go r.run()
	return r, nil
}

func normalizedPerformanceSystem(system PerformanceSystem) PerformanceSystem {
	system.GPUs, system.Unavailable = slices.Clone(system.GPUs), maps.Clone(system.Unavailable)
	if system.LogicalCPUs < 1 {
		system.LogicalCPUs = runtime.NumCPU()
		if system.Unavailable == nil {
			system.Unavailable = make(map[string]string)
		}
		system.Unavailable["logical_cpus"] = "系统逻辑处理器数不可用，CPU百分比使用Go可见逻辑处理器数"
	}
	if system.ProcessArch == "" {
		system.ProcessArch = runtime.GOARCH
	}
	return system
}

func performanceBuildInfo() map[string]string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	out := map[string]string{"main": info.Main.Path + "@" + info.Main.Version}
	for _, s := range info.Settings {
		out[s.Key] = s.Value
	}
	return out
}

func (r *PerformanceRecorder) Done() <-chan struct{} { return r.done }

func (r *PerformanceRecorder) Metadata() PerformanceMetadata {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.metadata
	m.Notes = slices.Clone(m.Notes)
	m.Build = maps.Clone(m.Build)
	m.System.GPUs = slices.Clone(m.System.GPUs)
	m.System.Unavailable = maps.Clone(m.System.Unavailable)
	return m
}

func clonePerformanceSample(s *PerformanceSample) *PerformanceSample {
	if s == nil {
		return nil
	}
	out := *s
	out.Values, out.Unavailable = maps.Clone(s.Values), maps.Clone(s.Unavailable)
	out.Processes = clonePerformanceProcesses(s.Processes)
	out.GPUEngines, out.Markers = slices.Clone(s.GPUEngines), slices.Clone(s.Markers)
	out.Top = slices.Clone(s.Top)
	a := &out.Application
	pointers := []**float64{&a.CaptureMS, &a.AnalysisMS, &a.DeliveryAgeMS, &a.CaptureP95MS, &a.AnalysisP95MS, &a.UIQueueMS,
		&a.CaptureMaxMS, &a.AnalysisMaxMS, &a.AttemptMaxMS, &a.AttemptP95MS, &a.LoopMaxMS, &a.LoopP95MS, &a.PostMaxMS, &a.UIQueueMaxMS}
	for i := range out.Top {
		pointers = append(pointers, &out.Top[i].CPUPercent, &out.Top[i].GPUPercent, &out.Top[i].GPUDedicatedBytes)
	}
	for _, p := range pointers {
		if *p != nil {
			v := **p
			*p = &v
		}
	}
	return &out
}

func clonePerformanceProcesses(processes []PerformanceProcess) []PerformanceProcess {
	out := slices.Clone(processes)
	for i := range out {
		out[i].Values = maps.Clone(out[i].Values)
		out[i].Unavailable = maps.Clone(out[i].Unavailable)
	}
	return out
}

func (r *PerformanceRecorder) Snapshot() PerformanceStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.status
	s.Latest = clonePerformanceSample(s.Latest)
	s.ProfileStatus = maps.Clone(s.ProfileStatus)
	s.PendingMarkers = slices.Clone(r.markers)
	if s.EndedAt != nil {
		v := *s.EndedAt
		s.EndedAt = &v
	}
	return s
}

func (r *PerformanceRecorder) Stop() error {
	r.stopOnce.Do(func() { close(r.stop) })
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.LastError != "" {
		return errors.New(r.status.LastError)
	}
	return nil
}

// Export reads only the completely published JSONL prefix, even while sampling.
func (r *PerformanceRecorder) Export(ctx context.Context) (string, error) {
	r.exportMu.Lock()
	defer r.exportMu.Unlock()
	s := r.Snapshot()
	return exportPerformanceReport(ctx, r.Metadata(), s, filepath.Join(s.Directory, "samples.jsonl"), s.Bytes)
}

func (r *PerformanceRecorder) run() {
	reason := "user_stopped"
	defer func() {
		if v := recover(); v != nil {
			r.setPerformanceError(performancePanicError("性能采样异常", v))
			reason = "collector_failed"
		}
		r.finish(reason)
	}()
	if !r.sample() {
		reason = r.limitReason()
		return
	}
	if r.options.Profiles {
		r.writeProfiles("start")
		r.startCPUProfile("cpu-start.pprof", 30*time.Second)
	}
	ticker := time.NewTicker(r.limits.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			// Include the stop boundary when it can provide a useful interval.
			if time.Since(r.previous) >= 10*time.Millisecond {
				if !r.sample() {
					reason = r.limitReason()
				}
			}
			return
		case <-ticker.C:
			if !r.sample() {
				reason = r.limitReason()
				return
			}
		}
	}
}

func (r *PerformanceRecorder) limitReason() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.StopReason != "" {
		return r.status.StopReason
	}
	return "write_failed"
}

func (r *PerformanceRecorder) finish(reason string) {
	defer func() {
		if v := recover(); v != nil {
			r.setPerformanceError(performancePanicError("性能监测收尾异常", v))
			r.mu.Lock()
			r.status.StopReason = "finalize_failed"
			r.mu.Unlock()
		}
		close(r.done)
		r.notify()
	}()
	// Also ends CPU profiles on limit/error stops, where Stop was not called.
	r.stopOnce.Do(func() { close(r.stop) })
	// Publish stopped state before slow profiles/export so the UI can show it.
	r.mu.Lock()
	ended := time.Now()
	r.status.Active, r.status.EndedAt, r.status.StopReason = false, &ended, reason
	r.mu.Unlock()
	if err := r.file.Sync(); err != nil {
		r.setPerformanceError(err)
	}
	if err := r.file.Close(); err != nil {
		r.setPerformanceError(err)
	}
	r.collector.Close()
	r.background.Wait()
	if r.options.Profiles {
		r.writeProfiles("end")
	}
	s := r.Snapshot()
	// GPU metadata can arrive after asynchronous native initialization.
	metadata, metaErr := json.MarshalIndent(r.Metadata(), "", "  ")
	if metaErr == nil {
		metaErr = atomicWrite(filepath.Join(s.Directory, "metadata.json"), metadata)
	}
	if metaErr != nil {
		r.setPerformanceError(metaErr)
	}
	data, err := json.MarshalIndent(r.Snapshot(), "", "  ")
	if err == nil {
		err = atomicWrite(filepath.Join(s.Directory, "status.json"), data)
	}
	if err != nil {
		r.setPerformanceError(err)
	}
}

func (r *PerformanceRecorder) notify() {
	if r.options.OnSample == nil {
		return
	}
	defer func() {
		if v := recover(); v != nil {
			r.setPerformanceError(performancePanicError("性能面板通知异常", v))
		}
	}()
	r.options.OnSample()
}

func performanceError(err error) string { return cleanPerformanceText(err.Error(), 600) }

func performancePanicError(operation string, value any) error {
	stack := debug.Stack()
	if len(stack) > 4096 {
		stack = stack[:4096]
	}
	return fmt.Errorf("%s: %v\n%s", operation, value, stack)
}

func (r *PerformanceRecorder) setPerformanceError(err error) {
	if err == nil {
		return
	}
	text := err.Error() // Keep line breaks: panic errors carry a stack.
	if len(text) > 8192 {
		text = text[:8192]
	}
	r.mu.Lock()
	r.status.LastError = text
	r.mu.Unlock()
}

func cleanPerformanceText(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return ' '
		}
		return r
	}, text)
	runes := []rune(text)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimSpace(string(runes))
}

// PerformanceUserMark prefixes marks made by the user ("it stuttered now").
// The report lists these separately from automatic bookkeeping events.
const PerformanceUserMark = "用户标记"

func (r *PerformanceRecorder) Mark(label string) error {
	label = cleanPerformanceText(label, 120)
	if label == "" {
		return errors.New("标记不能为空")
	}
	r.mu.Lock()
	if !r.status.Active {
		r.mu.Unlock()
		return errors.New("性能监测已停止")
	}
	if len(r.markers) >= 32 {
		r.mu.Unlock()
		return errors.New("标记过于频繁，请等待下一次采样")
	}
	r.markers = append(r.markers, fmt.Sprintf("%.3fs: %s", time.Since(r.started).Seconds(), label))
	profile := ""
	if r.options.Profiles && strings.HasPrefix(label, PerformanceUserMark) && r.cpuMarks < 5 {
		r.cpuMarks++
		profile = fmt.Sprintf("cpu-mark-%d.pprof", r.cpuMarks)
	}
	r.mu.Unlock()
	if profile != "" {
		// A stutter usually repeats: profile the seconds right after the mark.
		r.startCPUProfile(profile, 15*time.Second)
	}
	return nil
}

func durationMS(start, end time.Time) *float64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	v := float64(end.Sub(start)) / float64(time.Millisecond)
	return &v
}

// ObserveFrame reads timings and labels synchronously; it never retains f.Img.
// received is when the frame reached the capture loop.
func (r *PerformanceRecorder) ObserveFrame(f frame.Frame, received time.Time, poll time.Duration, fullDiagnostics bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.status.Active {
		return
	}
	r.app.Attempts++
	if !f.AnalysisStarted.IsZero() {
		r.app.Analyzed++
	}
	if f.Duplicate {
		r.app.Duplicates++
	}
	if f.Hold {
		r.app.Held++
	}
	if f.Err != nil {
		r.app.Errors++
		r.windowErrors++
		r.app.LastError = cleanPerformanceText(f.Err.Error(), 300)
	}
	// Failed and timed-out captures have no CapturedAt, yet they are the
	// slowest attempts of all: measure every attempt up to its arrival.
	if attempt := durationMS(f.CaptureStarted, received); attempt != nil {
		r.attempt.add(*attempt)
		if poll > 0 && received.Sub(f.CaptureStarted) > poll {
			r.app.SlowFrames++
			r.windowSlow++
		}
	}
	r.app.Backend, r.app.Scene = cleanPerformanceText(f.CaptureMethod, 160), cleanPerformanceText(f.Scene, 60)
	r.app.LayoutProfile, r.app.Engine = cleanPerformanceText(f.LayoutProfile, 60), cleanPerformanceText(f.Engine, 60)
	if f.Img != nil {
		r.app.Width, r.app.Height = f.Img.Bounds().Dx(), f.Img.Bounds().Dy()
	}
	r.app.PollMS, r.app.FullDiagnostics = float64(poll)/float64(time.Millisecond), fullDiagnostics
	r.app.CaptureMS = durationMS(f.CaptureStarted, f.CapturedAt)
	r.app.AnalysisMS = durationMS(f.AnalysisStarted, f.AnalyzedAt)
	r.app.DeliveryAgeMS = durationMS(f.CapturedAt, received)
	if r.app.CaptureMS != nil {
		r.capture.add(*r.app.CaptureMS)
	}
	if r.app.AnalysisMS != nil {
		r.analysis.add(*r.app.AnalysisMS)
	}
}

// ObserveLoop records one whole capture-loop iteration and the part of it
// spent after the frame arrived (state update, bead logic, overlay post).
func (r *PerformanceRecorder) ObserveLoop(total, post time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.status.Active {
		return
	}
	r.loop.add(float64(total) / float64(time.Millisecond))
	r.post.add(float64(post) / float64(time.Millisecond))
}

// ObserveUIQueue records how long a posted closure waited for the UI thread.
func (r *PerformanceRecorder) ObserveUIQueue(delay time.Duration) {
	if delay < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.status.Active {
		return
	}
	v := float64(delay) / float64(time.Millisecond)
	r.app.UIQueueMS, r.uiAt = &v, time.Now()
	r.uiQueue.add(v)
}

func p95(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	v := values[int(math.Ceil(float64(len(values))*.95))-1]
	return &v
}

func finiteValues(values map[string]float64, unavailable map[string]string) {
	for k, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			delete(values, k)
			unavailable[k] = "无效或负计数器值"
		}
	}
}

func counterRate(now, before map[string]float64, key string, seconds float64) (float64, bool) {
	a, ok := now[key]
	b, previous := before[key]
	if !ok || !previous || seconds <= 0 || a < b {
		return 0, false
	}
	return (a - b) / seconds, true
}

func performanceRates(sample *PerformanceSample, previous performancePlatformSample, rootPID, cpus int) {
	seconds := sample.IntervalS
	prior := make(map[int]PerformanceProcess, len(previous.Processes))
	for _, p := range previous.Processes {
		prior[p.PID] = p
	}
	for i := range sample.Processes {
		p := &sample.Processes[i]
		p.Name = cleanPerformanceText(path.Base(strings.ReplaceAll(p.Name, "\\", "/")), 128)
		if p.Values == nil {
			p.Values = make(map[string]float64)
		}
		if p.Unavailable == nil {
			p.Unavailable = make(map[string]string)
		}
		finiteValues(p.Values, p.Unavailable)
		old, exists := prior[p.PID]
		counterSeconds := 0.0
		if !p.CounterAt.IsZero() && !old.CounterAt.IsZero() {
			counterSeconds = p.CounterAt.Sub(old.CounterAt).Seconds()
		}
		if !p.CounterAt.IsZero() && !sample.At.IsZero() && !p.CounterAt.After(sample.At) {
			p.Values["counter_age_ms"] = float64(sample.At.Sub(p.CounterAt)) / float64(time.Millisecond)
		}
		for _, key := range []string{"cpu_cores", "cpu_percent", "io_read_bytes_s", "io_write_bytes_s", "page_faults_s"} {
			delete(p.Values, key)
		}
		if exists && p.StartID != "" && p.StartID == old.StartID && seconds > 0 && counterSeconds > 0 {
			p.Values["counter_interval_seconds"] = counterSeconds
			for _, rate := range [][2]string{{"cpu_seconds", "cpu_cores"}, {"io_read_bytes", "io_read_bytes_s"}, {"io_write_bytes", "io_write_bytes_s"}, {"page_faults", "page_faults_s"}} {
				if value, ok := counterRate(p.Values, old.Values, rate[0], counterSeconds); ok {
					p.Values[rate[1]] = value
				} else {
					p.Unavailable[rate[1]] = "计数器不可用或已重置"
				}
			}
			if cores, ok := p.Values["cpu_cores"]; ok && cpus > 0 {
				p.Values["cpu_percent"] = cores * 100 / float64(cpus)
			}
		} else {
			p.Unavailable["rates"] = "首次观测/进程身份变化/计数器时间缺失：无法计算可靠速率"
		}
		if p.PID == rootPID {
			for key, v := range p.Values {
				sample.Values["main_"+key] = v
			}
		}
	}
	for _, key := range []string{"cpu_percent", "cpu_cores", "working_set_bytes", "private_bytes", "private_resident_bytes", "virtual_bytes",
		"threads", "handles", "fds", "io_read_bytes_s", "io_write_bytes_s", "page_faults_s", "gpu_dedicated_bytes", "gpu_shared_bytes"} {
		var sum float64
		count := 0
		for _, p := range sample.Processes {
			if v, ok := p.Values[key]; ok {
				sum += v
				count++
			}
		}
		if count > 0 {
			sample.Values["tree_"+key] = sum
		}
		if count < len(sample.Processes) {
			sample.Unavailable["tree_"+key] = fmt.Sprintf("仅%d/%d个已观测进程有此指标", count, len(sample.Processes))
		}
	}
	busy, totalOK := counterRate(sample.Values, previous.Values, "system_cpu_total_seconds", seconds)
	idle, idleOK := counterRate(sample.Values, previous.Values, "system_cpu_idle_seconds", seconds)
	if totalOK && idleOK && busy > 0 && idle <= busy {
		sample.Values["system_cpu_percent"] = (busy - idle) * 100 / busy
	} else {
		sample.Unavailable["system_cpu_percent"] = "等待CPU计数器基线或计数器不可用/已重置"
	}
}

func goPerformanceValues(ms runtime.MemStats) (map[string]float64, float64) {
	v := map[string]float64{
		"go_heap_alloc_bytes": float64(ms.HeapAlloc), "go_heap_inuse_bytes": float64(ms.HeapInuse), "go_heap_sys_bytes": float64(ms.HeapSys),
		"go_heap_idle_bytes": float64(ms.HeapIdle), "go_heap_released_bytes": float64(ms.HeapReleased), "go_stack_inuse_bytes": float64(ms.StackInuse),
		"go_sys_bytes": float64(ms.Sys), "go_heap_objects": float64(ms.HeapObjects), "go_alloc_total_bytes": float64(ms.TotalAlloc),
		"go_mallocs_total": float64(ms.Mallocs), "go_frees_total": float64(ms.Frees), "go_next_gc_bytes": float64(ms.NextGC),
		"go_goroutines": float64(runtime.NumGoroutine()), "go_gomaxprocs": float64(runtime.GOMAXPROCS(0)), "go_gc_count": float64(ms.NumGC),
	}
	m := []metrics.Sample{{Name: "/gc/gogc:percent"}, {Name: "/gc/gomemlimit:bytes"}, {Name: "/cpu/classes/gc/total:cpu-seconds"}}
	metrics.Read(m)
	if m[0].Value.Kind() == metrics.KindUint64 {
		if n := m[0].Value.Uint64(); n != math.MaxUint64 {
			v["go_gogc_percent"] = float64(n)
		} else {
			v["go_gc_disabled"] = 1
		}
	}
	if m[1].Value.Kind() == metrics.KindUint64 {
		if n := m[1].Value.Uint64(); n != math.MaxInt64 {
			v["go_memory_limit_bytes"] = float64(n)
		} else {
			v["go_memory_limit_disabled"] = 1
		}
	}
	gcCPU := 0.0
	if m[2].Value.Kind() == metrics.KindFloat64 {
		gcCPU = m[2].Value.Float64()
	}
	return v, gcCPU
}

func (r *PerformanceRecorder) sample() bool {
	r.mu.Lock()
	if r.status.Samples >= r.limits.samples {
		r.status.StopReason = "sample_limit"
		r.mu.Unlock()
		return false
	}
	r.mu.Unlock()
	started := time.Now()
	host := r.collector.Collect()
	system := normalizedPerformanceSystem(r.collector.System()) // Cached/nonblocking native metadata.
	at := time.Now()
	r.mu.Lock()
	r.metadata.System = system
	r.mu.Unlock()
	// Own the observation: collectors may reuse their native query buffers.
	host.Values, host.Unavailable = maps.Clone(host.Values), maps.Clone(host.Unavailable)
	host.Processes = clonePerformanceProcesses(host.Processes)
	host.GPUEngines = slices.Clone(host.GPUEngines)
	host.AllProcesses = slices.Clone(host.AllProcesses)
	if host.Values == nil {
		host.Values = make(map[string]float64)
	}
	if host.Unavailable == nil {
		host.Unavailable = make(map[string]string)
	}
	finiteValues(host.Values, host.Unavailable)
	if total, ok := host.Values["system_memory_total_bytes"]; ok && total > 0 {
		if available, valid := host.Values["system_memory_available_bytes"]; valid && available <= total {
			host.Values["system_memory_used_percent"] = 100 * (total - available) / total
		}
	}
	host.GPUEngines = slices.DeleteFunc(host.GPUEngines, func(e PerformanceGPUEngine) bool {
		bad := math.IsNaN(e.Percent) || math.IsInf(e.Percent, 0) || e.Percent < 0
		if bad {
			host.Unavailable["gpu_engines"] = "忽略无效GPU引擎计数器值"
		}
		return bad
	})
	if len(host.Processes) > 64 {
		host.Processes = host.Processes[:64]
		host.Unavailable["processes"] = "进程数超过64项上限，已截断"
	}
	if len(host.GPUEngines) > 256 {
		host.GPUEngines = host.GPUEngines[:256]
		host.Unavailable["gpu_engines"] = "GPU实例超过256项上限，已截断"
	}
	sample := PerformanceSample{At: at, ElapsedS: at.Sub(r.started).Seconds(), Values: maps.Clone(host.Values),
		Processes: host.Processes, GPUEngines: host.GPUEngines, Unavailable: maps.Clone(host.Unavailable)}
	if !r.previous.IsZero() {
		sample.IntervalS = at.Sub(r.previous).Seconds()
	}
	performanceRates(&sample, r.prevHost, r.metadata.RootPID, system.LogicalCPUs)
	performanceTop(&sample, host, r.prevHost, r.metadata.RootPID, system.LogicalCPUs)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	goValues, gcCPU := goPerformanceValues(ms)
	maps.Copy(sample.Values, goValues)
	if goValues["go_gc_disabled"] == 1 {
		sample.Unavailable["go_gogc_percent"] = "GC已关闭"
	}
	if goValues["go_memory_limit_disabled"] == 1 {
		sample.Unavailable["go_memory_limit_bytes"] = "未设置Go软内存上限"
	}
	if sample.IntervalS > 0 {
		sample.Values["go_alloc_bytes_s"] = float64(ms.TotalAlloc-r.prevMem.TotalAlloc) / sample.IntervalS
		sample.Values["go_gc_cycles_s"] = float64(ms.NumGC-r.prevMem.NumGC) / sample.IntervalS
		sample.Values["go_gc_pause_ms"] = float64(ms.PauseTotalNs-r.prevMem.PauseTotalNs) / 1e6
		if gcCPU >= r.prevGCCPU {
			sample.Values["go_gc_cpu_cores"] = (gcCPU - r.prevGCCPU) / sample.IntervalS
		}
	}
	r.mu.Lock()
	sample.Application = r.app
	a := &sample.Application
	a.CaptureMaxMS, a.CaptureP95MS = r.capture.take()
	a.AnalysisMaxMS, a.AnalysisP95MS = r.analysis.take()
	a.AttemptMaxMS, a.AttemptP95MS = r.attempt.take()
	a.LoopMaxMS, a.LoopP95MS = r.loop.take()
	a.PostMaxMS, _ = r.post.take()
	a.UIQueueMaxMS, _ = r.uiQueue.take()
	a.WindowSlow, a.WindowErrors = r.windowSlow, r.windowErrors
	r.windowSlow, r.windowErrors = 0, 0
	sample.Application.WindowCaptureFrames = r.app.Attempts - r.prevAttempts
	if sample.IntervalS > 0 {
		sample.Values["capture_fps"] = float64(r.app.Attempts-r.prevAttempts) / sample.IntervalS
		sample.Values["analysis_fps"] = float64(r.app.Analyzed-r.prevAnalyzed) / sample.IntervalS
	}
	if r.uiAt.IsZero() || (!r.previous.IsZero() && r.uiAt.Before(r.previous)) {
		sample.Application.UIQueueMS = nil
		sample.Unavailable["ui_queue_ms"] = "本采样窗口没有界面线程回执（界面空闲，或界面线程被卡住）"
	}
	sample.Markers = slices.Clone(r.markers)
	r.prevAttempts, r.prevAnalyzed = r.app.Attempts, r.app.Analyzed
	r.mu.Unlock()
	sample.CollectMS = float64(time.Since(started)) / float64(time.Millisecond)
	finiteValues(sample.Values, sample.Unavailable)
	data, err := json.Marshal(sample)
	if err != nil {
		r.setPerformanceError(err)
		return false
	}
	data = append(data, '\n')
	r.mu.Lock()
	if r.status.Bytes+int64(len(data)) > r.limits.bytes {
		r.status.StopReason = "byte_limit"
		r.mu.Unlock()
		return false
	}
	r.mu.Unlock()
	if n, err := r.file.Write(data); err != nil || n != len(data) {
		if err == nil {
			err = io.ErrShortWrite
		}
		r.setPerformanceError(err)
		return false
	}
	r.mu.Lock()
	r.markers = r.markers[len(sample.Markers):]
	if len(r.markers) == 0 {
		r.markers = nil
	}
	r.status.Latest = &sample
	r.status.Samples++
	r.status.Bytes += int64(len(data))
	r.mu.Unlock()
	r.previous, r.prevHost, r.prevMem, r.prevGCCPU = at, host, ms, gcCPU
	r.notify()
	return true
}

type performanceLimitedWriter struct {
	w         io.Writer
	remaining int64
}

func (w *performanceLimitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("性能快照超过大小限制")
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func (r *PerformanceRecorder) setProfileStatus(name string, err error) {
	status := "ok"
	if err != nil {
		status = performanceError(err)
	}
	r.mu.Lock()
	r.status.ProfileStatus[name] = status
	r.mu.Unlock()
}

func (r *PerformanceRecorder) writeProfiles(stage string) {
	for _, kind := range []string{"heap", "goroutine"} {
		name, debug, limit := "heap-"+stage+".pprof", 0, int64(16<<20)
		if kind == "goroutine" {
			name, debug, limit = "goroutines-"+stage+".txt", 2, 4<<20
		}
		r.setProfileStatus(name, r.writeProfile(name, limit, func(w io.Writer) error {
			return pprof.Lookup(kind).WriteTo(w, debug)
		}))
	}
}

func (r *PerformanceRecorder) writeProfile(name string, limit int64, produce func(io.Writer) error) error {
	f, err := os.CreateTemp(r.status.Directory, ".profile-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	err = produce(&performanceLimitedWriter{w: f, remaining: limit})
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(tmp, filepath.Join(r.status.Directory, name))
}

// startCPUProfile samples where the timer itself burns CPU for a bounded time.
// The Go runtime allows one CPU profile per process, so overlapping requests
// (and a profile started by another tool) are skipped, never queued.
func (r *PerformanceRecorder) startCPUProfile(name string, duration time.Duration) {
	if !r.cpuProfiling.CompareAndSwap(false, true) {
		r.setProfileStatus(name, errors.New("已有 CPU profile 正在进行，本次跳过"))
		return
	}
	r.background.Add(1)
	go func() {
		defer r.background.Done()
		defer r.cpuProfiling.Store(false)
		r.setProfileStatus(name, r.writeProfile(name, 16<<20, func(w io.Writer) error {
			if err := pprof.StartCPUProfile(w); err != nil {
				return err
			}
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.stop:
			}
			pprof.StopCPUProfile()
			return nil
		}))
	}()
}

// PerformanceSessions lists recorded session directories under root, newest
// first. A session survives a crash or exit as raw samples; ExportPerformanceSession
// turns it into the same ZIP as a live export.
func PerformanceSessions(root string) []string {
	if root == "" {
		root = "debug"
	}
	root = filepath.Join(root, "performance")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "perf-") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if info, err := os.Stat(filepath.Join(dir, "samples.jsonl")); err == nil && info.Size() > 0 {
			out = append(out, dir)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out))) // perf-YYYYMMDD-HHMMSS-* sorts by time.
	return out
}

// ExportPerformanceSession builds performance-report.zip from a finished (or
// crashed) session directory. Do not call it for a session still recording.
func ExportPerformanceSession(ctx context.Context, dir string) (string, error) {
	var metadata PerformanceMetadata
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err == nil {
		err = json.Unmarshal(data, &metadata)
	}
	if err != nil {
		return "", fmt.Errorf("读取 metadata.json：%w", err)
	}
	status := PerformanceStatus{StartedAt: metadata.StartedAt, StopReason: "not_finalized"}
	if data, err := os.ReadFile(filepath.Join(dir, "status.json")); err == nil {
		if err := json.Unmarshal(data, &status); err != nil {
			status.LastError = "status.json 无法解析：" + err.Error()
		}
	}
	status.Directory, status.Active, status.Latest = dir, false, nil
	timeline := filepath.Join(dir, "samples.jsonl")
	info, err := os.Stat(timeline)
	if err != nil {
		return "", err
	}
	return exportPerformanceReport(ctx, metadata, status, timeline, info.Size())
}

// performanceEmulatorNames are lower-case executable-name prefixes of Android
// emulators (MuMu, LeiDian, BlueStacks, Nox, MEmu, Tencent). The emulator is
// never a child of the timer, so it is recognised by name.
var performanceEmulatorNames = []string{"mumu", "nemu", "dnplayer", "ld9box", "ldbox", "ldvbox", "ldplayer", "leidian",
	"hd-player", "bluestacks", "bstk", "nox", "memu", "androidemulator", "aow_exe"}

func performanceEmulatorName(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range performanceEmulatorNames {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// performanceTop turns the collector's all-process rows into the per-sample
// "who is using the machine" table and the emulator_* aggregate metrics.
// Rows kept: the timer's own tree, every emulator process, and the heaviest
// other processes by CPU, memory and GPU, bounded by PerformanceMaxTop.
func performanceTop(sample *PerformanceSample, host, previous performancePlatformSample, rootPID, cpus int) {
	if len(host.AllProcesses) == 0 {
		if sample.Unavailable["top_processes"] == "" {
			sample.Unavailable["top_processes"] = "本平台/本次未能枚举全系统进程"
		}
		return
	}
	prior := make(map[int]performanceSystemProcess, len(previous.AllProcesses))
	for _, p := range previous.AllProcesses {
		prior[p.PID] = p
	}
	tree := make(map[int]bool, len(sample.Processes))
	for _, p := range sample.Processes {
		tree[p.PID] = true
	}
	rows := make([]PerformanceTopProcess, 0, len(host.AllProcesses))
	for _, p := range host.AllProcesses {
		if p.PID <= 0 { // PID 0 is the idle pseudo-process: its "CPU" is idle time.
			continue
		}
		row := PerformanceTopProcess{PID: p.PID, Name: cleanPerformanceText(p.Name, 128), WorkingSetBytes: p.WorkingSet,
			PrivateBytes: p.Private, Threads: p.Threads, GPUPercent: p.GPUPercent, GPUDedicatedBytes: p.GPUDedicated}
		switch {
		case p.PID == rootPID:
			row.Role = "self"
		case tree[p.PID]:
			row.Role = "child"
		case performanceEmulatorName(row.Name):
			row.Role = "emulator"
		}
		if old, ok := prior[p.PID]; ok && old.StartID == p.StartID && sample.IntervalS > 0 && cpus > 0 && p.CPUSeconds >= old.CPUSeconds {
			v := min(100, (p.CPUSeconds-old.CPUSeconds)/sample.IntervalS*100/float64(cpus))
			row.CPUPercent = &v
		}
		rows = append(rows, row)
	}
	value := func(p *float64) float64 {
		if p == nil {
			return -1
		}
		return *p
	}
	var emulatorCPU, emulatorMemory, emulatorGPU, emulatorVRAM float64
	emulators, hasCPU, hasGPU, hasVRAM := 0, false, false, false
	for _, row := range rows {
		if row.Role != "emulator" {
			continue
		}
		emulators++
		emulatorMemory += row.WorkingSetBytes
		if row.CPUPercent != nil {
			emulatorCPU, hasCPU = emulatorCPU+*row.CPUPercent, true
		}
		if row.GPUPercent != nil {
			emulatorGPU, hasGPU = max(emulatorGPU, *row.GPUPercent), true
		}
		if row.GPUDedicatedBytes != nil {
			emulatorVRAM, hasVRAM = emulatorVRAM+*row.GPUDedicatedBytes, true
		}
	}
	if emulators == 0 {
		for _, key := range []string{"emulator_cpu_percent", "emulator_working_set_bytes", "emulator_gpu_percent", "emulator_gpu_dedicated_bytes"} {
			sample.Unavailable[key] = "未发现模拟器进程（按进程名识别 MuMu / 雷电等）"
		}
	} else {
		sample.Values["emulator_working_set_bytes"] = emulatorMemory
		if hasCPU {
			sample.Values["emulator_cpu_percent"] = min(100, emulatorCPU)
		}
		if hasGPU {
			sample.Values["emulator_gpu_percent"] = emulatorGPU
		}
		if hasVRAM {
			sample.Values["emulator_gpu_dedicated_bytes"] = emulatorVRAM
		}
	}
	picked := make(map[int]bool)
	var top []PerformanceTopProcess
	pick := func(limit int, keep func(PerformanceTopProcess) bool, less func(a, b PerformanceTopProcess) bool) {
		sort.SliceStable(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
		for _, row := range rows {
			if limit == 0 || len(top) >= PerformanceMaxTop {
				return
			}
			if picked[row.PID] || !keep(row) {
				continue
			}
			picked[row.PID] = true
			top = append(top, row)
			limit--
		}
	}
	byCPU := func(a, b PerformanceTopProcess) bool { return value(a.CPUPercent) > value(b.CPUPercent) }
	byMemory := func(a, b PerformanceTopProcess) bool { return a.WorkingSetBytes > b.WorkingSetBytes }
	pick(-1, func(r PerformanceTopProcess) bool { return r.Role == "self" || r.Role == "child" }, byCPU)
	pick(8, func(r PerformanceTopProcess) bool { return r.Role == "emulator" }, byMemory)
	pick(5, func(r PerformanceTopProcess) bool { return value(r.CPUPercent) > 0 }, byCPU)
	pick(5, func(PerformanceTopProcess) bool { return true }, byMemory)
	pick(3, func(r PerformanceTopProcess) bool { return value(r.GPUPercent) > 0 }, func(a, b PerformanceTopProcess) bool {
		return value(a.GPUPercent) > value(b.GPUPercent)
	})
	pick(2, func(r PerformanceTopProcess) bool { return value(r.GPUDedicatedBytes) > 0 }, func(a, b PerformanceTopProcess) bool {
		return value(a.GPUDedicatedBytes) > value(b.GPUDedicatedBytes)
	})
	sort.SliceStable(top, func(i, j int) bool {
		if a, b := value(top[i].CPUPercent), value(top[j].CPUPercent); a != b {
			return a > b
		}
		return top[i].WorkingSetBytes > top[j].WorkingSetBytes
	})
	sample.Top = top
}
