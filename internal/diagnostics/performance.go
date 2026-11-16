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
	"slices"
	"sort"
	"strings"
	"sync"
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
	mu                          sync.Mutex
	exportMu                    sync.Mutex
	stopOnce                    sync.Once
	stop                        chan struct{}
	done                        chan struct{}
	file                        *os.File
	collector                   performanceCollector
	options                     PerformanceOptions
	limits                      performanceLimits
	metadata                    PerformanceMetadata
	status                      PerformanceStatus
	started                     time.Time
	previous                    time.Time
	prevHost                    performancePlatformSample
	prevMem                     runtime.MemStats
	prevGCCPU                   float64
	prevAttempts, prevAnalyzed  uint64
	app                         PerformanceApplication
	captureTimes, analysisTimes []float64
	latencyDropped              uint64
	uiAt                        time.Time
	markers                     []string
}

// NewPerformance starts a separate, image-free, one-second resource session.
// Root is the debug directory; only a newly created performance/ child is used.
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
	// A Windows collector can bind a fixed local volume identity to the already
	// opened file, without repeatedly traversing a mutable directory path.
	if binder, ok := collector.(interface{ BindReportFile(*os.File) }); ok {
		binder.BindReportFile(file)
	}
	system := normalizedPerformanceSystem(collector.System())
	r := &PerformanceRecorder{stop: make(chan struct{}), done: make(chan struct{}), file: file, collector: collector,
		options: options, limits: limits, started: started,
		status: PerformanceStatus{Directory: dir, Active: true, StartedAt: started, ProfileStatus: make(map[string]string)},
		metadata: PerformanceMetadata{SchemaVersion: 1, Version: options.Version, StartedAt: started, RootPID: os.Getpid(),
			GoVersion: runtime.Version(), Build: performanceBuildInfo(), System: system, Settings: options.Settings, IntervalMS: int(limits.interval.Milliseconds()),
			MaxSamples: limits.samples, MaxBytes: limits.bytes, ProfilesRequested: options.Profiles, Notes: []string{
				"性能监测与全量诊断独立；不采截图、账号、命令行、环境变量、主机名或安装路径，不自动上传。",
				"CPU为时间口径：进程CPU秒/墙钟秒=核等效；除以系统逻辑核数后×100为全机归一百分比，不等同频率加权CPU utility。",
				"首次采样/新进程/计数器重置没有有效速率，字段缺失或null不是0。缺失能力和部分聚合在unavailable中注明。",
				"进程树只包含当前观测到的根进程及后代；独立模拟器、预先存在的ADB服务不计入，采样之间退出的短命进程可能漏计。",
				"工作集合计可能重复计入共享页；Windows private_bytes是私有提交，Linux private_resident_bytes是私有驻留，不能互换。Go堆不包括ORT/SDK/图形驱动原生内存。",
				"GPU显示最忙引擎，不把无关引擎百分比相加；GPU数据依赖OS/驱动/权限，OCR配置开关不代表实际执行后端。",
				"进程I/O计数可能包含文件、管道和设备，并非纯物理磁盘吞吐；页错误包含软错误，不全是换页读盘。",
				"进程速率按各自计数器读取时刻的单调间隔计算，counter_interval_seconds/counter_age_ms给出时间窗与时效；JSON时间戳为墙钟。collector_ms只测采集和统计，不含随后写盘/导出。UI探针不测GPU呈现或游戏FPS。",
				"内存趋势只是增长线索，缓存预热、原生分配和GC周期都可影响它，不能据此自动认定泄漏。",
				"可选Go堆快照不强制GC，反映最近GC附近的采样堆；协程快照含调用栈而非完整内存转储。快照中的绝对文件路径简化为文件名，保留函数/行号。记录和导出本身有开销。",
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
	out := make(map[string]string)
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision", "vcs.time", "vcs.modified", "GOOS", "GOARCH", "GOAMD64", "GOARM", "GOARM64", "CGO_ENABLED", "-compiler", "-buildmode":
			out[s.Key] = s.Value
		}
	}
	// Do not copy -ldflags, environment or local module replacement paths.
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
	for _, p := range []**float64{&out.Application.CaptureMS, &out.Application.AnalysisMS, &out.Application.DeliveryAgeMS,
		&out.Application.CaptureP95MS, &out.Application.AnalysisP95MS, &out.Application.UIQueueMS} {
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
	_ = r.Mark("性能报告导出开始（后续样本可能包含导出开销）")
	defer func() { _ = r.Mark("性能报告导出结束") }()
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

func performanceError(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Op + ": " + performanceError(pe.Err)
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Op + ": " + performanceError(le.Err)
	}
	return cleanPerformanceText(err.Error(), 400)
}

func performancePanicError(operation string, value any) error {
	if err, ok := value.(error); ok {
		return fmt.Errorf("%s: %s", operation, performanceError(err))
	}
	// An arbitrary panic value might contain a path, account, or raw payload.
	return fmt.Errorf("%s (%T)", operation, value)
}

func (r *PerformanceRecorder) setPerformanceError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	r.status.LastError = performanceError(err)
	r.mu.Unlock()
}

func cleanPerformanceText(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
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

func (r *PerformanceRecorder) Mark(label string) error {
	label = cleanPerformanceText(label, 120)
	if label == "" {
		return errors.New("标记不能为空")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.status.Active {
		return errors.New("性能监测已停止")
	}
	if len(r.markers) >= 32 {
		return errors.New("标记过于频繁，请等待下一次采样")
	}
	r.markers = append(r.markers, fmt.Sprintf("%.3fs: %s", time.Since(r.started).Seconds(), label))
	return nil
}

func durationMS(start, end time.Time) *float64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	v := float64(end.Sub(start)) / float64(time.Millisecond)
	return &v
}

// ObserveFrame copies scalar timings only; it never retains f.Img or names.
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
	}
	if !f.CaptureStarted.IsZero() && received.Sub(f.CaptureStarted) > poll && poll > 0 {
		r.app.SlowFrames++
	}
	backend := strings.ToLower(f.CaptureMethod)
	switch {
	case strings.Contains(backend, "mumu"):
		r.app.Backend = "mumu-sdk"
	case strings.Contains(backend, "leidian"):
		r.app.Backend = "leidian"
		if strings.Contains(backend, "raw") {
			r.app.Backend = "leidian-adb-raw"
		}
		if strings.Contains(backend, "window") {
			r.app.Backend = "leidian-window"
		}
	case strings.Contains(backend, "printwindow"):
		r.app.Backend = "printwindow"
	default:
		r.app.Backend = "other/unknown"
	}
	switch f.Scene {
	case "fight", "lobby", "result", "vs", "queue", "pick", "ban", "unknown", "":
		r.app.Scene = f.Scene
	default:
		r.app.Scene = "other"
	}
	if f.Img != nil {
		r.app.Width, r.app.Height = f.Img.Bounds().Dx(), f.Img.Bounds().Dy()
	}
	r.app.PollMS, r.app.FullDiagnostics = float64(poll)/float64(time.Millisecond), fullDiagnostics
	r.app.CaptureMS = durationMS(f.CaptureStarted, f.CapturedAt)
	r.app.AnalysisMS = durationMS(f.AnalysisStarted, f.AnalyzedAt)
	r.app.DeliveryAgeMS = durationMS(f.CapturedAt, received)
	if r.app.CaptureMS != nil {
		if len(r.captureTimes) < 2048 {
			r.captureTimes = append(r.captureTimes, *r.app.CaptureMS)
		} else {
			r.latencyDropped++
		}
	}
	if r.app.AnalysisMS != nil {
		if len(r.analysisTimes) < 2048 {
			r.analysisTimes = append(r.analysisTimes, *r.app.AnalysisMS)
		} else {
			r.latencyDropped++
		}
	}
}

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
	sample.Application.CaptureP95MS, sample.Application.AnalysisP95MS = p95(r.captureTimes), p95(r.analysisTimes)
	r.captureTimes, r.analysisTimes = r.captureTimes[:0], r.analysisTimes[:0]
	if r.latencyDropped > 0 {
		sample.Unavailable["latency_quantiles"] = fmt.Sprintf("本窗口有%d个延迟样本超过2048项上限；分位数为部分样本", r.latencyDropped)
		r.latencyDropped = 0
	}
	sample.Application.WindowCaptureFrames = r.app.Attempts - r.prevAttempts
	if sample.IntervalS > 0 {
		sample.Values["capture_fps"] = float64(r.app.Attempts-r.prevAttempts) / sample.IntervalS
		sample.Values["analysis_fps"] = float64(r.app.Analyzed-r.prevAnalyzed) / sample.IntervalS
	}
	if r.uiAt.IsZero() || (!r.previous.IsZero() && r.uiAt.Before(r.previous)) {
		sample.Application.UIQueueMS = nil
		sample.Unavailable["ui_queue_ms"] = "尚无本采样窗口的UI回执；未连接探针或主线程尚未响应"
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

func (r *PerformanceRecorder) writeProfiles(stage string) {
	for _, kind := range []string{"heap", "goroutine"} {
		name, debug, limit := "heap-"+stage+".pprof", 0, int64(16<<20)
		if kind == "goroutine" {
			name, debug, limit = "goroutines-"+stage+".txt", 2, 4<<20
		}
		err := r.writeProfile(kind, name, debug, limit)
		status := "ok"
		if err != nil {
			status = performanceError(err)
		}
		r.mu.Lock()
		r.status.ProfileStatus[name] = status
		r.mu.Unlock()
	}
}

func (r *PerformanceRecorder) writeProfile(kind, name string, debug int, limit int64) error {
	f, err := os.CreateTemp(r.status.Directory, ".profile-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	err = writeSanitizedPerformanceProfile(kind, &performanceLimitedWriter{w: f, remaining: limit}, debug)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(tmp, filepath.Join(r.status.Directory, name))
}
