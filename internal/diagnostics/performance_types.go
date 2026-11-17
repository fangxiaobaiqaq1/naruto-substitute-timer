package diagnostics

import "time"

// Performance reports are independent of image/recognition diagnostics and
// never contain screenshots. Recording is opt-in and stays on the local disk,
// so process names, error text and the effective config are kept verbatim.
// Missing measurements are omitted, never fabricated as 0.
const (
	PerformanceInterval   = time.Second
	PerformanceMaxSamples = 21600 // Six hours at the default interval.
	PerformanceMaxBytes   = 256 << 20
	// PerformanceMaxTop bounds the per-sample system-wide process table.
	PerformanceMaxTop = 20
)

type PerformanceSettings struct {
	CaptureProvider  string `json:"capture_provider"`
	PollMS           int    `json:"fight_poll_ms"`
	IdlePollMS       int    `json:"idle_poll_ms"`
	CaptureTimeoutMS int    `json:"capture_timeout_ms"`
	ContentMode      string `json:"content_mode"`
	Calibrated       bool   `json:"calibrated"`
	OCREnabled       bool   `json:"ocr_enabled"`
	PreMatchOCR      bool   `json:"pre_match_ocr"`
	OCRGPURequested  bool   `json:"ocr_gpu_requested"`
	OverlayMode      string `json:"overlay_mode"`
}

type PerformanceSystem struct {
	OS            string            `json:"os"`
	OSVersion     string            `json:"os_version"`
	NativeArch    string            `json:"native_arch"`
	ProcessArch   string            `json:"process_arch"`
	CPUName       string            `json:"cpu_name"`
	LogicalCPUs   int               `json:"logical_cpus"`
	TotalRAMBytes uint64            `json:"total_ram_bytes,omitempty"`
	GPUs          []PerformanceGPU  `json:"gpus"`
	Unavailable   map[string]string `json:"unavailable,omitempty"`
}

type PerformanceGPU struct {
	Name           string `json:"name"`
	Driver         string `json:"driver,omitempty"`
	Adapter        string `json:"adapter,omitempty"` // Adapter LUID; links PDH rows to this name.
	DedicatedBytes uint64 `json:"dedicated_bytes,omitempty"`
	SharedBytes    uint64 `json:"shared_bytes,omitempty"`
}

type PerformanceMetadata struct {
	SchemaVersion     int                 `json:"schema_version"`
	Version           string              `json:"application_version"`
	StartedAt         time.Time           `json:"started_at"`
	RootPID           int                 `json:"root_pid"`
	GoVersion         string              `json:"go_version"`
	Build             map[string]string   `json:"build,omitempty"`
	System            PerformanceSystem   `json:"system"`
	Settings          PerformanceSettings `json:"settings"`
	IntervalMS        int                 `json:"interval_ms"`
	MaxSamples        int                 `json:"max_samples"`
	MaxBytes          int64               `json:"max_timeline_bytes"`
	ProfilesRequested bool                `json:"profiles_requested"`
	Notes             []string            `json:"notes"`
}

// Values contain gauges and cumulative counters. Platform collectors supply
// cpu_seconds, working_set_bytes, private_bytes (Windows commit, NOT Linux RSS),
// private_resident_bytes (Linux), virtual_bytes, peak_working_set_bytes,
// page_faults, threads, handles or fds, io_read_bytes, io_write_bytes,
// gdi_objects, user_objects, gpu_busiest_engine_percent, gpu_dedicated_bytes,
// gpu_shared_bytes as available. The recorder adds cpu_percent (all logical
// CPUs=100), cpu_cores, io_read_bytes_s/io_write_bytes_s and page_faults_s.
// StartID prevents PID reuse from becoming a false counter delta. CounterAt is
// recorded beside CPU/IO reads, before potentially slow FD/thread enumeration.
type PerformanceProcess struct {
	PID         int                `json:"pid"`
	ParentPID   int                `json:"parent_pid"`
	Name        string             `json:"name"` // Basename only.
	StartID     string             `json:"start_id"`
	CounterAt   time.Time          `json:"counter_at,omitzero"` // Actual CPU/IO read time; monotonic component is used for deltas.
	Values      map[string]float64 `json:"values"`
	Unavailable map[string]string  `json:"unavailable,omitempty"`
}

type PerformanceGPUEngine struct {
	PID     int     `json:"pid"`
	Adapter string  `json:"adapter"`
	Engine  string  `json:"engine"`
	Percent float64 `json:"percent"`
}

// PerformanceTopProcess is one row of the system-wide "who is using the
// machine" table: the timer itself, emulator processes and the heaviest other
// processes by CPU, memory and GPU. Pointer fields are nil when not measured.
type PerformanceTopProcess struct {
	PID               int      `json:"pid"`
	Name              string   `json:"name"`
	Role              string   `json:"role,omitempty"` // self, child, emulator or empty.
	CPUPercent        *float64 `json:"cpu_percent"`    // All logical CPUs = 100.
	WorkingSetBytes   float64  `json:"working_set_bytes"`
	PrivateBytes      float64  `json:"private_bytes,omitempty"`
	Threads           int      `json:"threads,omitempty"`
	GPUPercent        *float64 `json:"gpu_percent"`
	GPUDedicatedBytes *float64 `json:"gpu_dedicated_bytes"`
}

// performanceSystemProcess is the raw per-process row a collector reports for
// EVERY process it can see. The recorder derives CPU rates and the top table.
type performanceSystemProcess struct {
	PID, ParentPID int
	Name           string
	StartID        string
	CPUSeconds     float64
	WorkingSet     float64
	Private        float64
	Threads        int
	GPUPercent     *float64
	GPUDedicated   *float64
}

// Platform Values: system_cpu_total_seconds/system_cpu_idle_seconds cumulative,
// system_memory_total_bytes/system_memory_available_bytes,
// system_commit_used_bytes/system_commit_limit_bytes,
// system_swap_total_bytes/system_swap_free_bytes, disk_total_bytes/disk_free_bytes,
// system_gpu_busiest_engine_percent, system_gpu_dedicated_bytes,
// system_gpu_shared_bytes and tree_gpu_busiest_engine_percent.
// GPU busiest-engine percentages are NOT sums of unrelated engine percentages.
// Processes holds the root and observed descendants with detailed counters;
// AllProcesses holds a cheap row for every process on the machine.
type performancePlatformSample struct {
	Values       map[string]float64
	Processes    []PerformanceProcess
	AllProcesses []performanceSystemProcess
	GPUEngines   []PerformanceGPUEngine
	Unavailable  map[string]string
}

type performanceCollector interface {
	System() PerformanceSystem
	Collect() performancePlatformSample
	Close()
}

// newPerformanceCollector(rootPID, reportDirectory) is platform-specific and
// uses local APIs only.

type PerformanceApplication struct {
	Backend             string   `json:"backend"`
	Scene               string   `json:"scene"`
	Width               int      `json:"width"`
	Height              int      `json:"height"`
	PollMS              float64  `json:"poll_ms"`
	FullDiagnostics     bool     `json:"full_diagnostics"`
	Attempts            uint64   `json:"attempts"`
	Analyzed            uint64   `json:"analyzed"`
	Duplicates          uint64   `json:"duplicates"`
	Held                uint64   `json:"held"`
	Errors              uint64   `json:"errors"`
	SlowFrames          uint64   `json:"slow_frames"`
	CaptureMS           *float64 `json:"capture_ms"`
	AnalysisMS          *float64 `json:"analysis_ms"`
	DeliveryAgeMS       *float64 `json:"delivery_age_ms"`
	CaptureP95MS        *float64 `json:"capture_p95_ms"`
	AnalysisP95MS       *float64 `json:"analysis_p95_ms"`
	UIQueueMS           *float64 `json:"ui_queue_ms"`
	WindowCaptureFrames uint64   `json:"window_capture_frames"`
	// Per-sample-window worst cases: a single slow frame must stay visible.
	// attempt = capture start until the frame reached the UI loop, including
	// failed and timed-out captures; loop = one whole capture-loop iteration;
	// post = the part of it after the frame arrived (state update + overlay).
	CaptureMaxMS  *float64 `json:"capture_max_ms"`
	AnalysisMaxMS *float64 `json:"analysis_max_ms"`
	AttemptMaxMS  *float64 `json:"attempt_max_ms"`
	AttemptP95MS  *float64 `json:"attempt_p95_ms"`
	LoopMaxMS     *float64 `json:"loop_max_ms"`
	LoopP95MS     *float64 `json:"loop_p95_ms"`
	PostMaxMS     *float64 `json:"post_max_ms"`
	UIQueueMaxMS  *float64 `json:"ui_queue_max_ms"`
	WindowSlow    uint64   `json:"window_slow_frames"`
	WindowErrors  uint64   `json:"window_errors"`
	LastError     string   `json:"last_error,omitempty"` // Latest capture/analysis error text.
	LayoutProfile string   `json:"layout_profile,omitempty"`
	Engine        string   `json:"engine,omitempty"`
}

type PerformanceSample struct {
	At          time.Time               `json:"at"`
	ElapsedS    float64                 `json:"elapsed_s"`
	IntervalS   float64                 `json:"interval_s"`
	CollectMS   float64                 `json:"collector_ms"`
	Values      map[string]float64      `json:"values"`
	Processes   []PerformanceProcess    `json:"processes"`
	GPUEngines  []PerformanceGPUEngine  `json:"gpu_engines,omitempty"`
	Top         []PerformanceTopProcess `json:"top_processes,omitempty"`
	Application PerformanceApplication  `json:"application"`
	Unavailable map[string]string       `json:"unavailable,omitempty"`
	Markers     []string                `json:"markers,omitempty"`
}

type PerformanceStatus struct {
	Directory      string             `json:"-"`
	Active         bool               `json:"active"`
	StartedAt      time.Time          `json:"started_at"`
	EndedAt        *time.Time         `json:"ended_at"`
	Samples        int                `json:"samples"`
	Bytes          int64              `json:"timeline_bytes"`
	StopReason     string             `json:"stop_reason,omitempty"`
	LastError      string             `json:"last_error,omitempty"`
	Latest         *PerformanceSample `json:"latest,omitempty"`
	ProfileStatus  map[string]string  `json:"profile_status,omitempty"`
	PendingMarkers []string           `json:"pending_markers,omitempty"` // Accepted marks not yet in the published timeline prefix.
}

type PerformanceOptions struct {
	Root     string
	Version  string
	Settings PerformanceSettings
	Config   []byte // Effective config as JSON; stored as config.json beside the samples.
	Profiles bool   // Go heap/goroutine snapshots at start and end plus short CPU profiles.
	OnSample func() // Background callback: must not block; UI should coalesce posts.
}

// Metric descriptors provide units and meanings to both the frontend and the
// offline report. Report unknown keys too; availability is per sample.
type PerformanceMetric struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Unit  string `json:"unit"`
}

var PerformanceMetrics = []PerformanceMetric{
	{"system_cpu_percent", "整机 CPU", "%"},
	{"tree_cpu_percent", "计时器 CPU（含子进程）", "%"},
	{"emulator_cpu_percent", "模拟器 CPU", "%"},
	{"main_cpu_percent", "计时器主进程 CPU", "%"},
	{"tree_cpu_cores", "计时器 CPU 核等效", "cores"},
	{"system_memory_used_percent", "整机内存使用率", "%"},
	{"tree_working_set_bytes", "计时器内存（含子进程，共享页可能重复）", "bytes"},
	{"emulator_working_set_bytes", "模拟器内存", "bytes"},
	{"main_working_set_bytes", "计时器主进程内存（工作集）", "bytes"},
	{"main_peak_working_set_bytes", "主进程生命周期工作集峰值（非本会话）", "bytes"},
	{"main_counter_interval_seconds", "主进程计数器实际时间窗", "seconds"},
	{"main_counter_age_ms", "主进程计数器时效", "ms"},
	{"main_private_bytes", "主进程私有提交（Windows）", "bytes"},
	{"tree_private_bytes", "进程树私有提交（Windows）", "bytes"},
	{"system_memory_available_bytes", "系统可用物理内存", "bytes"},
	{"system_commit_used_bytes", "系统提交量", "bytes"},
	{"system_commit_limit_bytes", "系统提交上限", "bytes"},
	{"go_heap_alloc_bytes", "Go 堆已分配", "bytes"},
	{"go_heap_inuse_bytes", "Go 堆在用页", "bytes"},
	{"go_heap_sys_bytes", "Go 堆保留", "bytes"},
	{"go_heap_idle_bytes", "Go 空闲堆", "bytes"},
	{"go_heap_released_bytes", "Go 已归还系统堆", "bytes"},
	{"go_stack_inuse_bytes", "Go 栈", "bytes"},
	{"go_sys_bytes", "Go 运行时保留", "bytes"},
	{"go_alloc_bytes_s", "Go 分配速率", "bytes/s"},
	{"go_heap_objects", "Go 存活对象", "count"},
	{"go_goroutines", "Go 协程", "count"},
	{"go_gc_count", "Go 累计 GC 次数", "count"},
	{"go_gc_cycles_s", "Go GC 频率", "count/s"},
	{"go_gc_pause_ms", "本采样间隔 GC STW 总暂停", "ms"},
	{"go_gc_cpu_cores", "Go GC CPU 核等效", "cores"},
	{"go_gogc_percent", "实际 GOGC", "%"},
	{"go_memory_limit_bytes", "Go 软内存限制", "bytes"},
	{"go_gomaxprocs", "实际 GOMAXPROCS", "count"},
	{"tree_threads", "进程树线程数", "count"},
	{"tree_handles", "进程树句柄数（Windows）", "count"},
	{"tree_fds", "进程树文件描述符（Linux）", "count"},
	{"tree_io_read_bytes_s", "进程树读取速率（含管道/设备）", "bytes/s"},
	{"tree_io_write_bytes_s", "进程树写入速率（含管道/设备）", "bytes/s"},
	{"tree_page_faults_s", "进程树页错误速率（含软错误）", "count/s"},
	{"main_gdi_objects", "主进程 GDI 对象", "count"},
	{"main_user_objects", "主进程 USER 对象", "count"},
	{"system_gpu_busiest_engine_percent", "整机 GPU（最忙引擎）", "%"},
	{"tree_gpu_busiest_engine_percent", "计时器 GPU（最忙引擎）", "%"},
	{"emulator_gpu_percent", "模拟器 GPU（最忙引擎）", "%"},
	{"main_gpu_busiest_engine_percent", "计时器主进程 GPU", "%"},
	{"system_gpu_dedicated_bytes", "整机专用显存占用", "bytes"},
	{"system_gpu_shared_bytes", "整机共享显存占用", "bytes"},
	{"tree_gpu_dedicated_bytes", "计时器专用显存", "bytes"},
	{"tree_gpu_shared_bytes", "计时器共享显存", "bytes"},
	{"emulator_gpu_dedicated_bytes", "模拟器专用显存", "bytes"},
	{"disk_free_bytes", "报告所在卷可用空间", "bytes"},
	{"capture_fps", "采样尝试（含失败/hold，非游戏帧率）", "count/s"},
	{"analysis_fps", "当前采样窗口分析次数", "count/s"},
}
