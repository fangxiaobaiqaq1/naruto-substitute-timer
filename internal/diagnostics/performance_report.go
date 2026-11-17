package diagnostics

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	performanceReportLineLimit    = 1 << 20
	performanceReportMetricLimit  = 512
	performanceReportProcessLimit = 1024
	performanceReportReasonLimit  = 512
	performanceReportChartBuckets = 256
	performanceReportProfileLimit = 16 << 20 // Per optional attachment, independently bounded.
)

//go:embed performance_report.html
var performanceReportHTML string

var performanceReportTemplate = template.Must(template.New("performance-report").Funcs(template.FuncMap{
	"number":  performanceReportNumber,
	"human":   performanceReportHumanValue,
	"size":    performanceReportSize,
	"percent": performanceReportPercent,
	"clock":   performanceReportTime,
	"elapsed": performanceReportClock,
}).Parse(performanceReportHTML))

// exportPerformanceReport exports only a performance-session snapshot. The caller
// owns recorder synchronization and must pass immutable metadata/status snapshots
// and a committed JSONL prefix length. Latest and Directory never enter the ZIP.
// No second read of the live timeline is made, including when producing the CSV.
func exportPerformanceReport(ctx context.Context, metadata PerformanceMetadata, status PerformanceStatus, timelinePath string, timelineBytes int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if timelineBytes < 0 || timelineBytes > PerformanceMaxBytes {
		return "", fmt.Errorf("性能时间线超过 %d MiB 上限", PerformanceMaxBytes>>20)
	}
	directory, err := filepath.Abs(filepath.Dir(timelinePath))
	if err != nil {
		return "", err
	}
	out, err := os.CreateTemp(directory, ".performance-report-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	spool, err := os.CreateTemp(directory, ".performance-snapshot-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(spool.Name())
	defer spool.Close()

	z := zip.NewWriter(performanceReportWriter{ctx, out})
	z.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	defer z.Close()
	entry := func(name string) (io.Writer, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0600)
		w, err := z.CreateHeader(h)
		return performanceReportWriter{ctx, w}, err
	}
	jsonEntry := func(name string, value any) error {
		w, err := entry(name)
		if err != nil {
			return err
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "  ")
		return e.Encode(value)
	}

	report := newPerformanceReport(metadata, status, timelineBytes)
	raw, err := entry("samples.jsonl")
	if err != nil {
		return "", err
	}
	if timelineBytes > 0 {
		f, err := os.Open(timelinePath)
		if err != nil {
			return "", err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return "", err
		}
		if info.Size() < timelineBytes {
			return "", errors.New("performance timeline is shorter than the committed snapshot")
		}
		limited := &io.LimitedReader{R: f, N: timelineBytes}
		reader := io.TeeReader(limited, io.MultiWriter(raw, performanceReportWriter{ctx, spool}))
		if err := performanceReportScan(ctx, reader, &report.Summary.SkippedLines, func(s performanceReportSample) error { return report.add(s) }); err != nil {
			return "", err
		}
		if limited.N != 0 {
			return "", errors.New("performance timeline was truncated while exporting")
		}
	}
	if err := report.finish(ctx); err != nil {
		return "", err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	w, err := entry("samples.csv")
	if err != nil {
		return "", err
	}
	if err = report.csv(ctx, spool, w); err != nil {
		return "", err
	}
	if err = spool.Close(); err != nil {
		return "", err
	}

	// Optional attachments: completed profiles, the effective config and the
	// recorder's final status. A missing or oversized one is noted, not fatal.
	attach := func(name string) (int64, error) {
		f, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			return 0, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() || info.Size() > performanceReportProfileLimit {
			return 0, errors.New("不是普通文件或超过 16 MiB")
		}
		w, err := entry(name)
		if err != nil {
			return 0, err
		}
		return io.Copy(w, f)
	}
	names := make([]string, 0, len(status.ProfileStatus))
	for name := range status.ProfileStatus {
		if name == filepath.Base(name) && !strings.HasPrefix(name, ".") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		p := performanceReportProfile{Name: name, Status: status.ProfileStatus[name]}
		if p.Status != "ok" {
			p.Result = "采集未完成：" + p.Status
		} else if n, err := attach(name); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			p.Result = "未包含：" + err.Error()
		} else {
			p.Included, p.Bytes, p.Result = true, n, "已包含"
		}
		report.Summary.Profiles = append(report.Summary.Profiles, p)
	}
	if _, err := attach("config.json"); err != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err = jsonEntry("status.json", status); err != nil {
		return "", err
	}
	if err = jsonEntry("metadata.json", report.Metadata); err != nil {
		return "", err
	}
	if err = jsonEntry("summary.json", report.Summary); err != nil {
		return "", err
	}
	w, err = entry("report.md")
	if err != nil {
		return "", err
	}
	if _, err = io.WriteString(w, report.markdown()); err != nil {
		return "", err
	}
	w, err = entry("report.html")
	if err != nil {
		return "", err
	}
	view, err := report.view()
	if err != nil {
		return "", err
	}
	if err = performanceReportTemplate.Execute(w, view); err != nil {
		return "", err
	}
	if err = z.Close(); err != nil {
		return "", err
	}
	if err = out.Sync(); err != nil {
		return "", err
	}
	if err = out.Close(); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	target := filepath.Join(directory, "performance-report.zip")
	// Unlike replaceFile's remove-and-retry fallback, a failed rename must leave
	// the previous export intact. Go's Windows os.Rename uses MoveFileEx with
	// REPLACE_EXISTING; a same-directory rename needs no delete-first fallback.
	if err = os.Rename(out.Name(), target); err != nil {
		return "", err
	}
	return target, nil
}

type performanceReportWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w performanceReportWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

// Pointer-valued wire maps intentionally differ from the shared recorder types:
// encoding/json otherwise silently converts a JSON null float64 into a real 0.
type performanceReportSampleAlias PerformanceSample
type performanceReportProcessAlias PerformanceProcess
type performanceReportApplicationAlias PerformanceApplication

type performanceReportProcessWire struct {
	performanceReportProcessAlias
	Values map[string]*float64 `json:"values"`
}

type performanceReportApplicationWire struct {
	performanceReportApplicationAlias
	SlowFrames *uint64 `json:"slow_frames"`
}

type performanceReportSample struct {
	performanceReportSampleAlias
	Elapsed   *float64                          `json:"elapsed_s"`
	Interval  *float64                          `json:"interval_s"`
	Collector *float64                          `json:"collector_ms"`
	Metrics   map[string]*float64               `json:"values"`
	Procs     []performanceReportProcessWire    `json:"processes"`
	App       *performanceReportApplicationWire `json:"application"`
}

// performanceReportScan visits every well-formed sample. A damaged line (for
// example the torn last line of a crashed session) is skipped and counted, so
// one bad record never costs the user the whole report.
func performanceReportScan(ctx context.Context, r io.Reader, skipped *int, visit func(performanceReportSample) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), performanceReportLineLimit+1)
	skip := func() {
		if skipped != nil {
			*skipped++
		}
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		data := scanner.Bytes()
		if len(bytes.TrimSpace(data)) == 0 {
			continue
		}
		var s performanceReportSample
		if !utf8.Valid(data) || json.Unmarshal(data, &s) != nil || s.At.IsZero() || s.Elapsed == nil || *s.Elapsed < 0 {
			skip()
			continue
		}
		s.ElapsedS = *s.Elapsed
		if s.Interval != nil && *s.Interval > 0 {
			s.IntervalS = *s.Interval
		}
		s.Values = performanceReportValues(s.Metrics)
		for _, p := range s.Procs {
			p.performanceReportProcessAlias.Values = performanceReportValues(p.Values)
			s.Processes = append(s.Processes, PerformanceProcess(p.performanceReportProcessAlias))
		}
		if s.App != nil {
			s.Application = PerformanceApplication(s.App.performanceReportApplicationAlias)
			if s.App.SlowFrames != nil {
				s.Application.SlowFrames = *s.App.SlowFrames
			}
		}
		if err := visit(s); err != nil {
			skip()
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取性能时间线（单行上限 1 MiB）：%w", err)
	}
	return ctx.Err()
}

func performanceReportValues(values map[string]*float64) map[string]float64 {
	out := make(map[string]float64, len(values))
	for key, v := range values {
		if v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && math.Abs(*v) <= 1e100 {
			out[key] = *v
		}
	}
	return out
}

type performanceReportMinute struct {
	Minute int64   `json:"minute"`
	Count  int     `json:"count"`
	Mean   float64 `json:"mean"`
	Peak   float64 `json:"peak"`
}

type performanceReportMetric struct {
	PerformanceMetric
	Count          int                       `json:"count"`
	Missing        int                       `json:"missing"`
	First          *float64                  `json:"first"`
	Last           *float64                  `json:"last"`
	Min            *float64                  `json:"min"`
	Peak           *float64                  `json:"peak"`
	Mean           *float64                  `json:"mean"`
	P50            *float64                  `json:"p50"`
	P95            *float64                  `json:"p95"`
	P99            *float64                  `json:"p99"`
	Change         *float64                  `json:"last_minus_first"`
	TrendPerMinute *float64                  `json:"trend_per_minute"`
	Minutes        []performanceReportMinute `json:"minutes"`
}

type performanceReportAccumulator struct {
	metric performanceReportMetric
	values []float64
	meanT  float64
	meanV  float64
	sTT    float64
	sTV    float64
}

func performanceReportFloat(v float64) *float64 { return &v }

func (a *performanceReportAccumulator) add(t, v float64) {
	a.values = append(a.values, v)
	n := float64(len(a.values))
	if a.metric.First == nil {
		a.metric.First = performanceReportFloat(v)
		a.metric.Last = performanceReportFloat(v)
	}
	*a.metric.Last = v
	dt, dv := t-a.meanT, v-a.meanV
	a.meanT += dt / n
	a.meanV += dv / n
	a.sTT += dt * (t - a.meanT)
	a.sTV += dt * (v - a.meanV)
	minute := int64(math.Floor(t / 60))
	b := &a.metric.Minutes
	if len(*b) == 0 || (*b)[len(*b)-1].Minute != minute {
		*b = append(*b, performanceReportMinute{Minute: minute, Peak: v})
	}
	m := &(*b)[len(*b)-1]
	m.Count++
	m.Mean += (v - m.Mean) / float64(m.Count)
	m.Peak = math.Max(m.Peak, v)
}

// performanceReportLevel reports whether a per-minute growth trend means
// anything for the metric: levels such as memory, handles and threads do;
// percentages, rates, durations and cumulative counters do not.
func performanceReportLevel(m PerformanceMetric) bool {
	if m.Unit != "bytes" && m.Unit != "count" {
		return false
	}
	for _, part := range []string{"_total", "gc_count", "disk_", "system_memory_total", "commit_limit", "memory_limit", "gomaxprocs", "_window", "page_faults", "io_"} {
		if strings.Contains(m.Key, part) {
			return false
		}
	}
	return true
}

func (a *performanceReportAccumulator) finish(samples int, spanS float64) performanceReportMetric {
	m := a.metric
	m.Count, m.Missing = len(a.values), samples-len(a.values)
	if m.Count == 0 {
		return m
	}
	sort.Float64s(a.values)
	quantile := func(p float64) *float64 {
		return performanceReportFloat(a.values[max(0, int(math.Ceil(p*float64(m.Count)))-1)])
	}
	m.Min, m.Peak = quantile(0), quantile(1)
	m.P50, m.P95, m.P99 = quantile(.50), quantile(.95), quantile(.99)
	m.Mean = performanceReportFloat(a.meanV)
	// A slope fitted to a few warm-up samples prints alarming nonsense
	// ("+86 MiB/min"). Only levels observed for five minutes get a trend.
	if m.Count > 1 && performanceReportLevel(m.PerformanceMetric) {
		m.Change = performanceReportFloat(*m.Last - *m.First)
		if a.sTT > 0 && spanS >= 300 && m.Count >= 120 {
			m.TrendPerMinute = performanceReportFloat(60 * a.sTV / a.sTT)
		}
	}
	return m
}

type performanceReportGap struct {
	FromS            float64 `json:"from_s"`
	ToS              float64 `json:"to_s"`
	EstimatedMissing int64   `json:"estimated_missing"`
}

type performanceReportEvent struct {
	ElapsedS float64  `json:"elapsed_s"`
	Markers  []string `json:"markers"`
}

// performanceReportMark is a mark the user made ("it stuttered now").
type performanceReportMark struct {
	ElapsedS float64   `json:"elapsed_s"`
	At       time.Time `json:"at"`
	Label    string    `json:"label"`
}

// performanceReportTop aggregates the per-sample system-wide process table by
// program: same-named processes (an emulator's helpers, browser tabs) are
// summed within a sample. CPUMean is over the whole session, counting samples
// where the program was too light to be listed as 0.
type performanceReportTop struct {
	Name       string   `json:"name"`
	Role       string   `json:"role,omitempty"`
	Samples    int      `json:"samples"`
	Instances  int      `json:"max_instances"`
	CPUMean    *float64 `json:"cpu_percent_mean"`
	CPUPeak    *float64 `json:"cpu_percent_peak"`
	MemoryMean float64  `json:"working_set_bytes_mean"`
	MemoryPeak float64  `json:"working_set_bytes_peak"`
	GPUMean    *float64 `json:"gpu_percent_mean"`
	GPUPeak    *float64 `json:"gpu_percent_peak"`
	VRAMPeak   *float64 `json:"gpu_dedicated_bytes_peak"`

	cpuSum, gpuSum, memorySum float64
	cpuCount, gpuCount        int
}

// performanceReportHeadline is one at-a-glance number on the first screen.
type performanceReportHeadline struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Detail string `json:"detail,omitempty"`
}

type performanceReportUnavailable struct {
	Scope  string  `json:"scope"`
	Metric string  `json:"metric"`
	Reason string  `json:"reason"`
	Count  int     `json:"observations"`
	FirstS float64 `json:"first_s"`
	LastS  float64 `json:"last_s"`
}

type performanceReportProcessMetric struct {
	Count int     `json:"count"`
	First float64 `json:"first"`
	Last  float64 `json:"last"`
	Peak  float64 `json:"peak"`
	Mean  float64 `json:"mean"`
}

type performanceReportProcess struct {
	PID     int                                       `json:"pid"`
	Parent  int                                       `json:"parent_pid"`
	Name    string                                    `json:"name"`
	StartID string                                    `json:"start_id"`
	FirstS  float64                                   `json:"first_s"`
	LastS   float64                                   `json:"last_s"`
	Samples int                                       `json:"samples"`
	Metrics map[string]performanceReportProcessMetric `json:"metrics"`
}

type performanceReportProfile struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Included bool   `json:"included"`
	Bytes    int64  `json:"bytes"`
	Result   string `json:"result"`
}

type performanceReportSummary struct {
	SchemaVersion int        `json:"schema_version"`
	GeneratedAt   time.Time  `json:"generated_at"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at"`
	Active        bool       `json:"active"`
	StopReason    string     `json:"stop_reason,omitempty"`
	LastError     string     `json:"last_error,omitempty"` // The recorder's own error, verbatim.
	// The first screen: plain-language findings, key numbers, and who used the machine.
	Verdict          []string                    `json:"verdict"`
	Headline         []performanceReportHeadline `json:"headline"`
	Top              []*performanceReportTop     `json:"top_processes"`
	UserMarks        []performanceReportMark     `json:"user_marks"`
	SlowFrames       uint64                      `json:"slow_frames"`
	CaptureErrors    uint64                      `json:"capture_errors"`
	LastCaptureError string                      `json:"last_capture_error,omitempty"`
	Backend          string                      `json:"capture_backend,omitempty"`
	Resolution       string                      `json:"capture_resolution,omitempty"`
	SkippedLines     int                         `json:"skipped_lines"`

	ProfileStatus              map[string]string              `json:"profile_status"`
	TimelineBytes              int64                          `json:"timeline_bytes"`
	Samples                    int                            `json:"samples"`
	Insufficient               bool                           `json:"insufficient_samples"`
	FirstAt                    *time.Time                     `json:"first_at"`
	LastAt                     *time.Time                     `json:"last_at"`
	FirstElapsedS              *float64                       `json:"first_elapsed_s"`
	LastElapsedS               *float64                       `json:"last_elapsed_s"`
	ExpectedIntervalS          float64                        `json:"expected_interval_s"`
	Metrics                    []performanceReportMetric      `json:"metrics"`
	Gaps                       []performanceReportGap         `json:"gaps"`
	Events                     []performanceReportEvent       `json:"events"`
	PendingMarkers             []string                       `json:"pending_markers"`
	Unavailable                []performanceReportUnavailable `json:"unavailable"`
	Processes                  []*performanceReportProcess    `json:"processes"`
	OmittedProcessObservations int                            `json:"omitted_process_observations"`
	OmittedReasons             int                            `json:"omitted_availability_observations"`
	Profiles                   []performanceReportProfile     `json:"profiles"`
	Notes                      []string                       `json:"notes"`
}

type performanceReport struct {
	Metadata PerformanceMetadata
	Summary  performanceReportSummary
	metrics  map[string]*performanceReportAccumulator
	procs    map[string]*performanceReportProcess
	tops     map[string]*performanceReportTop
	topSeen  int // Samples that carried a system-wide process table.
	reasons  map[string]*performanceReportUnavailable
	previous *performanceReportSample
	charts   []performanceReportChart
	width    int
	order    []string // Curated metric order; unknown keys follow alphabetically.
	finished map[string]performanceReportMetric
}

var performanceReportExtraMetrics = []PerformanceMetric{
	{"loop_max_ms", "单轮识别循环最长耗时", "ms"},
	{"loop_p95_ms", "单轮识别循环 P95", "ms"},
	{"attempt_max_ms", "采集到交付最长耗时（含失败/超时）", "ms"},
	{"attempt_p95_ms", "采集到交付 P95", "ms"},
	{"capture_max_ms", "截图最长耗时", "ms"},
	{"capture_p95_ms", "截图 P95", "ms"},
	{"analysis_max_ms", "识别最长耗时", "ms"},
	{"analysis_p95_ms", "识别 P95", "ms"},
	{"post_max_ms", "状态更新与刷新最长耗时", "ms"},
	{"ui_queue_max_ms", "界面线程最长等待", "ms"},
	{"ui_queue_ms", "界面线程等待（最近一次）", "ms"},
	{"slow_frames_window", "本秒慢帧数（超过轮询间隔）", "count"},
	{"errors_window", "本秒采集/识别错误数", "count"},
	{"capture_ms", "截图耗时（最近一帧）", "ms"},
	{"analysis_ms", "识别耗时（最近一帧）", "ms"},
	{"delivery_age_ms", "交付帧龄（最近一帧）", "ms"},
	{"collector_ms", "性能采样自身耗时", "ms"},
	{"main_cpu_cores", "计时器主进程 CPU 核等效", "cores"},
	{"main_private_resident_bytes", "主进程私有驻留（Linux）", "bytes"},
	{"tree_private_resident_bytes", "进程树私有驻留（Linux）", "bytes"},
	{"main_virtual_bytes", "主进程虚拟地址空间", "bytes"},
	{"tree_virtual_bytes", "进程树虚拟地址空间合计", "bytes"},
	{"main_threads", "主进程线程数", "count"},
	{"main_handles", "主进程句柄数（Windows）", "count"},
	{"main_fds", "主进程文件描述符（Linux）", "count"},
	{"main_io_read_bytes_s", "主进程读取速率", "bytes/s"},
	{"main_io_write_bytes_s", "主进程写入速率", "bytes/s"},
	{"main_page_faults_s", "主进程页错误速率", "count/s"},
	{"main_io_read_bytes", "主进程累计读取", "bytes"},
	{"main_io_write_bytes", "主进程累计写入", "bytes"},
	{"main_page_faults", "主进程累计页错误", "count"},
	{"main_cpu_seconds", "主进程累计 CPU 时间", "seconds"},
	{"system_cpu_total_seconds", "整机累计 CPU 时间", "seconds"},
	{"system_cpu_idle_seconds", "整机累计空闲时间", "seconds"},
	{"system_memory_total_bytes", "物理内存总量", "bytes"},
	{"system_swap_total_bytes", "交换分区总量", "bytes"},
	{"system_swap_free_bytes", "交换分区可用", "bytes"},
	{"disk_total_bytes", "报告所在盘总容量", "bytes"},
	{"go_alloc_total_bytes", "Go 累计分配量", "bytes"},
	{"go_next_gc_bytes", "Go 下次 GC 目标堆", "bytes"},
	{"go_mallocs_total", "Go 累计分配对象", "count"},
	{"go_frees_total", "Go 累计释放对象", "count"},
	{"go_memory_limit_disabled", "Go 软内存上限未设置（1=是）", "flag"},
	{"go_gc_disabled", "自动 GC 已关闭（1=是）", "flag"},
}

func newPerformanceReport(m PerformanceMetadata, s PerformanceStatus, n int64) *performanceReport {
	r := &performanceReport{Metadata: m, metrics: make(map[string]*performanceReportAccumulator), procs: make(map[string]*performanceReportProcess),
		tops: make(map[string]*performanceReportTop), reasons: make(map[string]*performanceReportUnavailable), width: 1}
	r.Summary = performanceReportSummary{
		SchemaVersion: 2, GeneratedAt: time.Now(), StartedAt: m.StartedAt, EndedAt: s.EndedAt,
		Active: s.Active, StopReason: s.StopReason, LastError: s.LastError, ProfileStatus: s.ProfileStatus,
		TimelineBytes: n, ExpectedIntervalS: float64(m.IntervalMS) / 1000,
		PendingMarkers: append([]string(nil), s.PendingMarkers...),
		Notes: []string{
			"CPU 百分比按全部逻辑核归一（与任务管理器“进程”页一致）：100% 表示所有核心满载。",
			"空白 / “—” 表示没有测到，不是 0。",
			"GPU 为最忙引擎的占用，不把不同引擎相加；计时器内存合计可能重复计算共享页。",
			"进程榜每秒只记录占用最高的若干进程：平均 CPU 按整段记录计算，未上榜的时刻按 0 计。",
		},
	}
	if r.Summary.ExpectedIntervalS <= 0 {
		r.Summary.ExpectedIntervalS = PerformanceInterval.Seconds()
	}
	for _, metrics := range [][]PerformanceMetric{PerformanceMetrics, performanceReportExtraMetrics} {
		for _, metric := range metrics {
			if _, exists := r.metrics[metric.Key]; !exists {
				r.metrics[metric.Key] = &performanceReportAccumulator{metric: performanceReportMetric{PerformanceMetric: metric}}
				r.order = append(r.order, metric.Key)
			}
		}
	}
	for _, group := range performanceReportChartGroups {
		for _, key := range group.Keys {
			r.charts = append(r.charts, performanceReportChart{Key: key, Group: group.Title})
		}
	}
	r.unavailable("system", m.System.Unavailable, 0)
	return r
}

func (r *performanceReport) values(s performanceReportSample) map[string]float64 {
	v := s.Values
	if s.Collector != nil {
		v["collector_ms"] = *s.Collector
	}
	app := s.Application
	for key, value := range map[string]*float64{
		"capture_ms": app.CaptureMS, "analysis_ms": app.AnalysisMS, "delivery_age_ms": app.DeliveryAgeMS,
		"capture_p95_ms": app.CaptureP95MS, "analysis_p95_ms": app.AnalysisP95MS, "ui_queue_ms": app.UIQueueMS,
		"capture_max_ms": app.CaptureMaxMS, "analysis_max_ms": app.AnalysisMaxMS, "attempt_max_ms": app.AttemptMaxMS,
		"attempt_p95_ms": app.AttemptP95MS, "loop_max_ms": app.LoopMaxMS, "loop_p95_ms": app.LoopP95MS,
		"post_max_ms": app.PostMaxMS, "ui_queue_max_ms": app.UIQueueMaxMS,
	} {
		if value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) {
			v[key] = *value
		}
	}
	if s.App != nil {
		v["slow_frames_window"], v["errors_window"] = float64(app.WindowSlow), float64(app.WindowErrors)
	}
	for _, prefix := range []string{"main", "tree"} {
		if _, exists := v[prefix+"_cpu_cores"]; !exists && r.Metadata.System.LogicalCPUs > 0 {
			if percent, ok := v[prefix+"_cpu_percent"]; ok {
				v[prefix+"_cpu_cores"] = percent * (float64(r.Metadata.System.LogicalCPUs) / 100)
			}
		}
	}
	return v
}

func (r *performanceReport) add(s performanceReportSample) error {
	if r.previous != nil && s.ElapsedS <= r.previous.ElapsedS {
		return errors.New("elapsed_s must be strictly increasing")
	}
	if s.ElapsedS > 1e12 {
		return errors.New("timing exceeds supported numeric range")
	}
	values := r.values(s)
	for key, value := range values {
		if r.ensureMetric(key) {
			r.metrics[key].add(s.ElapsedS, value)
		}
	}
	from := 0.0
	if r.previous != nil {
		from = r.previous.ElapsedS
	}
	gap := s.ElapsedS-from > 1.5*r.Summary.ExpectedIntervalS
	if gap {
		r.Summary.Gaps = append(r.Summary.Gaps, performanceReportGap{FromS: from, ToS: s.ElapsedS,
			EstimatedMissing: max(1, int64(math.Round((s.ElapsedS-from)/r.Summary.ExpectedIntervalS))-1)})
	}
	// User marks are the point of the recording: list every one of them, apart
	// from bookkeeping, and never let automatic events crowd them out.
	var events []string
	for _, marker := range s.Markers {
		at, label := s.ElapsedS, marker
		if prefix, rest, ok := strings.Cut(marker, "s: "); ok {
			if v, err := strconv.ParseFloat(prefix, 64); err == nil && v >= 0 && v <= s.ElapsedS {
				at, label = v, rest
			}
		}
		if strings.HasPrefix(label, PerformanceUserMark) {
			r.Summary.UserMarks = append(r.Summary.UserMarks, performanceReportMark{ElapsedS: at,
				At: r.Summary.StartedAt.Add(time.Duration(at * float64(time.Second))), Label: label})
		} else {
			events = append(events, label)
		}
	}
	if r.previous != nil && s.At.Before(r.previous.At) {
		events = append(events, "系统时钟被回拨（曲线按已用时间绘制，不受影响）")
	}
	if len(events) != 0 && len(r.Summary.Events) < 500 {
		r.Summary.Events = append(r.Summary.Events, performanceReportEvent{ElapsedS: s.ElapsedS, Markers: events})
	}
	for _, p := range s.Processes {
		r.addProcess(p, s.ElapsedS)
		r.unavailable("process:"+p.Name, p.Unavailable, s.ElapsedS)
	}
	r.addTop(s.Top)
	if s.App != nil {
		app := s.Application
		r.Summary.SlowFrames, r.Summary.CaptureErrors = app.SlowFrames, app.Errors
		if app.LastError != "" {
			r.Summary.LastCaptureError = app.LastError
		}
		if app.Backend != "" {
			r.Summary.Backend = app.Backend
		}
		if app.Width > 0 && app.Height > 0 {
			r.Summary.Resolution = fmt.Sprintf("%d×%d", app.Width, app.Height)
		}
	}
	r.unavailable("sample", s.Unavailable, s.ElapsedS)
	for i := range r.charts {
		r.charts[i].add(r.Summary.Samples/r.width, s.ElapsedS, values, gap)
	}
	if len(r.charts) > 0 && len(r.charts[0].buckets) > performanceReportChartBuckets {
		for i := range r.charts {
			r.charts[i].compact()
		}
		r.width *= 2
	}
	if r.Summary.Samples == 0 {
		at := s.At
		r.Summary.FirstAt, r.Summary.FirstElapsedS = &at, performanceReportFloat(s.ElapsedS)
	}
	at := s.At
	r.Summary.LastAt, r.Summary.LastElapsedS = &at, performanceReportFloat(s.ElapsedS)
	r.Summary.Samples++
	r.previous = &s
	return nil
}

func (r *performanceReport) ensureMetric(key string) bool {
	if _, ok := r.metrics[key]; ok {
		return true
	}
	if len(r.metrics) >= performanceReportMetricLimit || len(key) > 256 {
		return false
	}
	r.metrics[key] = &performanceReportAccumulator{metric: performanceReportMetric{PerformanceMetric: PerformanceMetric{Key: key, Label: key}}}
	return true
}

func (r *performanceReport) addProcess(p PerformanceProcess, at float64) {
	id := strconv.Itoa(p.PID) + ":" + p.StartID
	row := r.procs[id]
	if row == nil {
		if len(r.procs) >= performanceReportProcessLimit {
			r.Summary.OmittedProcessObservations++
			return
		}
		row = &performanceReportProcess{PID: p.PID, Parent: p.ParentPID, Name: p.Name, StartID: p.StartID, FirstS: at, Metrics: make(map[string]performanceReportProcessMetric)}
		r.procs[id] = row
	}
	row.LastS, row.Samples = at, row.Samples+1
	for key, value := range p.Values {
		m := row.Metrics[key]
		if m.Count == 0 {
			if len(row.Metrics) >= performanceReportMetricLimit {
				continue
			}
			m.First, m.Peak = value, value
		}
		m.Count++
		m.Last, m.Peak = value, math.Max(m.Peak, value)
		m.Mean += (value - m.Mean) / float64(m.Count)
		row.Metrics[key] = m
	}
}

func (r *performanceReport) addTop(rows []PerformanceTopProcess) {
	if len(rows) == 0 {
		return
	}
	r.topSeen++
	type sum struct {
		cpu, gpu, vram, memory  float64
		hasCPU, hasGPU, hasVRAM bool
		instances               int
		name, role              string
	}
	now := make(map[string]*sum)
	for _, row := range rows {
		role := row.Role
		if role == "child" {
			role = "self" // The timer's helpers belong to the timer.
		}
		name := row.Name
		if role == "self" {
			name = "计时器（本程序）"
		}
		key := role + "\x00" + strings.ToLower(name)
		t := now[key]
		if t == nil {
			t = &sum{name: name, role: role}
			now[key] = t
		}
		t.instances++
		t.memory += row.WorkingSetBytes
		if row.CPUPercent != nil {
			t.cpu, t.hasCPU = t.cpu+*row.CPUPercent, true
		}
		if row.GPUPercent != nil {
			t.gpu, t.hasGPU = max(t.gpu, *row.GPUPercent), true
		}
		if row.GPUDedicatedBytes != nil {
			t.vram, t.hasVRAM = t.vram+*row.GPUDedicatedBytes, true
		}
	}
	for key, t := range now {
		row := r.tops[key]
		if row == nil {
			if len(r.tops) >= performanceReportProcessLimit {
				continue
			}
			row = &performanceReportTop{Name: t.name, Role: t.role}
			r.tops[key] = row
		}
		row.Samples++
		row.Instances = max(row.Instances, t.instances)
		row.memorySum += t.memory
		row.MemoryPeak = max(row.MemoryPeak, t.memory)
		if t.hasCPU {
			cpu := min(100, t.cpu)
			row.cpuSum, row.cpuCount = row.cpuSum+cpu, row.cpuCount+1
			if row.CPUPeak == nil || cpu > *row.CPUPeak {
				row.CPUPeak = performanceReportFloat(cpu)
			}
		}
		if t.hasGPU {
			row.gpuSum, row.gpuCount = row.gpuSum+t.gpu, row.gpuCount+1
			if row.GPUPeak == nil || t.gpu > *row.GPUPeak {
				row.GPUPeak = performanceReportFloat(t.gpu)
			}
		}
		if t.hasVRAM && (row.VRAMPeak == nil || t.vram > *row.VRAMPeak) {
			row.VRAMPeak = performanceReportFloat(t.vram)
		}
	}
}

func (r *performanceReport) unavailable(scope string, values map[string]string, at float64) {
	for metric, reason := range values {
		key := scope + "\x00" + metric + "\x00" + reason
		u := r.reasons[key]
		if u == nil {
			if len(r.reasons) >= performanceReportReasonLimit {
				r.Summary.OmittedReasons++
				continue
			}
			u = &performanceReportUnavailable{Scope: scope, Metric: metric, Reason: reason, FirstS: at}
			r.reasons[key] = u
		}
		u.Count++
		u.LastS = at
	}
}

func (r *performanceReport) finish(ctx context.Context) error {
	r.Summary.Insufficient = r.Summary.Samples < 2
	span := 0.0
	if seconds := r.Summary.observedSeconds(); seconds != nil {
		span = *seconds
	}
	// Curated order first (what a reader looks for), unknown keys after.
	known := make(map[string]bool, len(r.order))
	for _, key := range r.order {
		known[key] = true
	}
	extra := make([]string, 0)
	for key := range r.metrics {
		if !known[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	r.finished = make(map[string]performanceReportMetric, len(r.metrics))
	for _, key := range append(append([]string(nil), r.order...), extra...) {
		if err := ctx.Err(); err != nil {
			return err
		}
		m := r.metrics[key].finish(r.Summary.Samples, span)
		r.finished[key] = m
		r.Summary.Metrics = append(r.Summary.Metrics, m)
	}
	for _, row := range r.procs {
		r.Summary.Processes = append(r.Summary.Processes, row)
	}
	sort.Slice(r.Summary.Processes, func(i, j int) bool {
		a, b := r.Summary.Processes[i], r.Summary.Processes[j]
		if a.PID != b.PID {
			return a.PID < b.PID
		}
		return a.StartID < b.StartID
	})
	for _, row := range r.tops {
		// Samples where a program was too light to be listed count as 0 CPU.
		if row.cpuCount > 0 && r.topSeen > 0 {
			row.CPUMean = performanceReportFloat(row.cpuSum / float64(r.topSeen))
		}
		if row.gpuCount > 0 && r.topSeen > 0 {
			row.GPUMean = performanceReportFloat(row.gpuSum / float64(r.topSeen))
		}
		row.MemoryMean = row.memorySum / float64(row.Samples)
		r.Summary.Top = append(r.Summary.Top, row)
	}
	score := func(t *performanceReportTop) float64 {
		if t.CPUMean == nil {
			return -1
		}
		return *t.CPUMean
	}
	sort.Slice(r.Summary.Top, func(i, j int) bool {
		a, b := r.Summary.Top[i], r.Summary.Top[j]
		if score(a) != score(b) {
			return score(a) > score(b)
		}
		if a.MemoryPeak != b.MemoryPeak {
			return a.MemoryPeak > b.MemoryPeak
		}
		return a.Name < b.Name
	})
	for _, reason := range r.reasons {
		r.Summary.Unavailable = append(r.Summary.Unavailable, *reason)
	}
	sort.Slice(r.Summary.Unavailable, func(i, j int) bool {
		a, b := r.Summary.Unavailable[i], r.Summary.Unavailable[j]
		return a.Scope+"\x00"+a.Metric+"\x00"+a.Reason < b.Scope+"\x00"+b.Metric+"\x00"+b.Reason
	})
	r.conclude(span)
	// Release quantile storage before serializing the report and attachments.
	r.metrics, r.procs, r.tops, r.reasons, r.previous = nil, nil, nil, nil, nil
	return nil
}

func performanceReportDuration(seconds float64) string {
	total := int64(math.Round(seconds))
	if total >= 3600 {
		return fmt.Sprintf("%d 小时 %02d 分 %02d 秒", total/3600, total%3600/60, total%60)
	}
	if total >= 60 {
		return fmt.Sprintf("%d 分 %02d 秒", total/60, total%60)
	}
	return fmt.Sprintf("%d 秒", total)
}

// conclude writes the first screen: the key numbers and a few rule-based,
// plain-language findings. The rules only point at where to look; they are
// thresholds on measured averages and peaks, not a diagnosis.
func (r *performanceReport) conclude(span float64) {
	metric := func(key string) (performanceReportMetric, bool) {
		m, ok := r.finished[key]
		return m, ok && m.Count > 0
	}
	head := func(label, value, detail string) {
		r.Summary.Headline = append(r.Summary.Headline, performanceReportHeadline{label, value, detail})
	}
	head("记录时长", performanceReportDuration(span), fmt.Sprintf("%d 次采样", r.Summary.Samples))
	for _, item := range [][2]string{
		{"整机 CPU", "system_cpu_percent"}, {"计时器 CPU", "tree_cpu_percent"}, {"模拟器 CPU", "emulator_cpu_percent"},
		{"整机内存", "system_memory_used_percent"}, {"整机 GPU", "system_gpu_busiest_engine_percent"},
		{"计时器 GPU", "tree_gpu_busiest_engine_percent"}, {"模拟器 GPU", "emulator_gpu_percent"},
	} {
		if m, ok := metric(item[1]); ok {
			head(item[0], "平均 "+performanceReportHumanValue(m.Mean, "%"), "峰值 "+performanceReportHumanValue(m.Peak, "%"))
		}
	}
	if m, ok := metric("tree_working_set_bytes"); ok {
		detail := "峰值 " + performanceReportHumanValue(m.Peak, "bytes")
		if m.Change != nil {
			detail += fmt.Sprintf(" · 首末变化 %+.0f MB", *m.Change/(1<<20))
		}
		head("计时器内存", performanceReportHumanValue(m.Last, "bytes"), detail)
	}
	if m, ok := metric("emulator_working_set_bytes"); ok {
		head("模拟器内存", "平均 "+performanceReportHumanValue(m.Mean, "bytes"), "峰值 "+performanceReportHumanValue(m.Peak, "bytes"))
	}
	if m, ok := metric("loop_max_ms"); ok {
		head("单轮识别最长", performanceReportHumanValue(m.Peak, "ms"), "P95 "+performanceReportHumanValue(m.P95, "ms"))
	}
	head("手动卡顿标记", fmt.Sprintf("%d 次", len(r.Summary.UserMarks)), fmt.Sprintf("慢帧 %d · 采集错误 %d", r.Summary.SlowFrames, r.Summary.CaptureErrors))

	say := func(format string, args ...any) {
		r.Summary.Verdict = append(r.Summary.Verdict, fmt.Sprintf(format, args...))
	}
	mean := func(key string) (float64, bool) {
		if m, ok := metric(key); ok {
			return *m.Mean, true
		}
		return 0, false
	}
	heaviest := func(by func(*performanceReportTop) float64) *performanceReportTop {
		var best *performanceReportTop
		for _, t := range r.Summary.Top {
			if t.Role != "self" && (best == nil || by(t) > by(best)) {
				best = t
			}
		}
		return best
	}
	short := span < 30
	if short {
		say("记录只有 %s，时间太短，下面的结论仅供参考；建议在卡顿出现时至少记录一两分钟。", performanceReportDuration(span))
	}
	findings := len(r.Summary.Verdict)
	system, hasSystem := mean("system_cpu_percent")
	timer, hasTimer := mean("tree_cpu_percent")
	if hasSystem && system >= 75 {
		text := fmt.Sprintf("整机 CPU 长时间偏高（平均 %.0f%%）", system)
		if hasTimer {
			text += fmt.Sprintf("，其中计时器占 %.1f%%", timer)
		}
		if top := heaviest(func(t *performanceReportTop) float64 {
			if t.CPUMean == nil {
				return -1
			}
			return *t.CPUMean
		}); top != nil && top.CPUMean != nil && (!hasTimer || *top.CPUMean > timer) {
			text += fmt.Sprintf("；占用最高的是 %s（平均 %.1f%%）", top.Name, *top.CPUMean)
			if hasTimer && timer < 15 {
				text += "，瓶颈不在计时器"
			}
		}
		say("%s。", text)
	}
	if hasTimer && timer >= 20 {
		peak, _ := metric("tree_cpu_percent")
		say("计时器自身 CPU 占用偏高：平均 %.1f%%，峰值 %.1f%%。可查看 ZIP 中的 cpu-*.pprof 定位热点函数。", timer, *peak.Peak)
	}
	if m, ok := metric("system_memory_used_percent"); ok && *m.Peak >= 90 {
		text := fmt.Sprintf("整机内存紧张（峰值 %.0f%%）", *m.Peak)
		if top := heaviest(func(t *performanceReportTop) float64 { return t.MemoryPeak }); top != nil {
			text += fmt.Sprintf("；占用内存最多的是 %s（峰值 %s）", top.Name, performanceReportHumanValue(&top.MemoryPeak, "bytes"))
		}
		say("%s。内存不足时系统会换页，表现为全局卡顿。", text)
	}
	if m, ok := metric("tree_working_set_bytes"); ok && m.Change != nil && span >= 300 && *m.Change >= 100<<20 && *m.Change >= 0.3**m.First {
		say("计时器内存从 %s 增长到 %s（%+.0f MB）。需要更长时间的记录才能确认是否持续增长。",
			performanceReportHumanValue(m.First, "bytes"), performanceReportHumanValue(m.Last, "bytes"), *m.Change/(1<<20))
	}
	if gpu, ok := mean("system_gpu_busiest_engine_percent"); ok && gpu >= 85 {
		text := fmt.Sprintf("显卡长时间接近满载（平均 %.0f%%）", gpu)
		if ours, ok := mean("tree_gpu_busiest_engine_percent"); ok {
			text += fmt.Sprintf("，计时器占 %.1f%%", ours)
		}
		say("%s。", text)
	}
	if m, ok := metric("loop_max_ms"); ok && *m.Peak >= 250 {
		text := fmt.Sprintf("识别循环出现过单轮 %.0f ms 的长耗时", *m.Peak)
		var parts []string
		for _, item := range [][2]string{{"capture_max_ms", "截图"}, {"analysis_max_ms", "识别"}, {"post_max_ms", "状态更新与刷新"}} {
			if stage, ok := metric(item[0]); ok {
				parts = append(parts, fmt.Sprintf("%s最长 %.0f ms", item[1], *stage.Peak))
			}
		}
		if len(parts) > 0 {
			text += "（各阶段分别统计：" + strings.Join(parts, "，") + "）"
		}
		say("%s。", text)
	}
	if m, ok := metric("ui_queue_max_ms"); ok && *m.Peak >= 200 {
		say("界面线程出现过 %.0f ms 的排队等待，期间浮窗刷新会停顿。", *m.Peak)
	}
	if r.Summary.CaptureErrors > 0 {
		text := fmt.Sprintf("采集/识别共报错 %d 次", r.Summary.CaptureErrors)
		if r.Summary.LastCaptureError != "" {
			text += "，最近一次：" + r.Summary.LastCaptureError
		}
		say("%s。", text)
	}
	if r.Summary.LastError != "" {
		say("性能记录器自身报告过错误：%s", r.Summary.LastError)
	}
	if len(r.Summary.Gaps) > 0 {
		say("有 %d 段时间没有采到样本（系统休眠，或整机卡到连采样都被推迟）。", len(r.Summary.Gaps))
	}
	if len(r.Summary.Verdict) == findings {
		say("本次记录没有发现明显的资源瓶颈：整机与计时器的 CPU、内存、显卡占用都在正常范围。")
	}
}

func (r *performanceReport) csv(ctx context.Context, source io.Reader, dest io.Writer) error {
	w := csv.NewWriter(dest)
	header := []string{"at", "elapsed_s", "interval_s", "markers_json", "unavailable_json"}
	for _, metric := range r.Summary.Metrics {
		header = append(header, metric.Key)
	}
	if err := w.Write(header); err != nil {
		return err
	}
	err := performanceReportScan(ctx, source, nil, func(s performanceReportSample) error {
		values := r.values(s)
		markers, _ := json.Marshal(s.Markers)
		unavailable, _ := json.Marshal(s.Unavailable)
		row := []string{s.At.Format(time.RFC3339Nano), strconv.FormatFloat(s.ElapsedS, 'g', -1, 64), strconv.FormatFloat(s.IntervalS, 'g', -1, 64), string(markers), string(unavailable)}
		for _, metric := range r.Summary.Metrics {
			value := ""
			if v, ok := values[metric.Key]; ok {
				value = strconv.FormatFloat(v, 'g', -1, 64)
			}
			row = append(row, value)
		}
		return w.Write(row)
	})
	w.Flush()
	if err != nil {
		return err
	}
	return w.Error()
}

// A chart bucket retains extrema at their original times, not averaged peaks.
// Compaction merges adjacent buckets, keeping this invariant at every level.
// A discontinuity (sampling gap or missing value) is recorded where it
// happened: before the bucket's first point, or somewhere inside it.
type performanceReportPoint struct{ T, V float64 }
type performanceReportBucket struct {
	first, last, low, high   performanceReportPoint
	valid                    bool
	breakBefore, breakInside bool
}

type performanceReportChart struct {
	Key     string   `json:"key"`
	Group   string   `json:"group"`
	Label   string   `json:"label"`
	Unit    string   `json:"unit"`
	Points  [][2]any `json:"points"`
	buckets []performanceReportBucket
	started bool // A valid point exists: later breaks are real discontinuities.
	pending bool // A gap/missing value since the last valid point.
}

var performanceReportChartGroups = []struct {
	Title string
	Keys  []string
}{
	{"CPU 占用 (%)", []string{"system_cpu_percent", "tree_cpu_percent", "emulator_cpu_percent"}},
	{"内存：计时器 / 模拟器", []string{"tree_working_set_bytes", "emulator_working_set_bytes", "go_heap_alloc_bytes"}},
	{"整机内存使用率 (%)", []string{"system_memory_used_percent"}},
	{"GPU 占用 (%)", []string{"system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent", "emulator_gpu_percent"}},
	{"显存", []string{"system_gpu_dedicated_bytes", "tree_gpu_dedicated_bytes", "emulator_gpu_dedicated_bytes"}},
	{"每秒内最慢的一轮 (ms)", []string{"loop_max_ms", "attempt_max_ms", "capture_max_ms", "analysis_max_ms"}},
	{"界面线程等待 / GC 暂停 (ms)", []string{"ui_queue_max_ms", "go_gc_pause_ms"}},
	{"每秒识别次数", []string{"capture_fps", "analysis_fps"}},
	{"每秒慢帧 / 错误数", []string{"slow_frames_window", "errors_window"}},
	{"计时器磁盘读写", []string{"tree_io_read_bytes_s", "tree_io_write_bytes_s"}},
	{"线程 / 协程", []string{"tree_threads", "go_goroutines"}},
	{"句柄", []string{"tree_handles", "tree_fds"}},
}

func (c *performanceReportChart) add(index int, t float64, values map[string]float64, gap bool) {
	for index >= len(c.buckets) {
		c.buckets = append(c.buckets, performanceReportBucket{})
	}
	b := &c.buckets[index]
	v, ok := values[c.Key]
	if (gap || !ok) && c.started {
		c.pending = true
	}
	if !ok {
		return
	}
	p := performanceReportPoint{t, v}
	if !b.valid {
		b.first, b.low, b.high, b.valid = p, p, p, true
		b.breakBefore = c.pending
	} else if c.pending {
		b.breakInside = true
	}
	c.started, c.pending = true, false
	b.last = p
	if v < b.low.V {
		b.low = p
	}
	if v > b.high.V {
		b.high = p
	}
}

func (c *performanceReportChart) compact() {
	for i, j := 0, 0; i < len(c.buckets); i, j = i+2, j+1 {
		a := c.buckets[i]
		if i+1 < len(c.buckets) {
			b := c.buckets[i+1]
			if !a.valid {
				a = b
			} else if b.valid {
				a.breakInside = a.breakInside || b.breakInside || b.breakBefore
				a.last = b.last
				if b.low.V < a.low.V {
					a.low = b.low
				}
				if b.high.V > a.high.V {
					a.high = b.high
				}
			}
		}
		c.buckets[j] = a
	}
	c.buckets = c.buckets[:(len(c.buckets)+1)/2]
}

// finish emits up to four points per bucket in time order. [null,null] lifts
// the pen: once before a bucket that follows a discontinuity, and between the
// points of a bucket that contains one (its exact position was compacted away).
func (c *performanceReportChart) finish() {
	for _, b := range c.buckets {
		if !b.valid {
			continue
		}
		if b.breakBefore {
			c.Points = append(c.Points, [2]any{nil, nil})
		}
		points := []performanceReportPoint{b.first, b.low, b.high, b.last}
		sort.Slice(points, func(i, j int) bool { return points[i].T < points[j].T })
		var previous *performanceReportPoint
		for i := range points {
			p := &points[i]
			if previous != nil && p.T == previous.T {
				continue
			}
			if b.breakInside && previous != nil {
				c.Points = append(c.Points, [2]any{nil, nil})
			}
			c.Points = append(c.Points, [2]any{p.T, p.V})
			previous = p
		}
	}
	c.buckets = nil
}

type performanceReportView struct {
	Metadata  PerformanceMetadata
	Summary   performanceReportSummary
	Key       []performanceReportMetric // Metrics with data, curated order.
	Top       []*performanceReportTop
	ChartJSON template.JS
	Duration  string
	Stop      string
}

// performanceReportStop turns the recorder's stop reason into words.
func performanceReportStop(s performanceReportSummary) string {
	switch s.StopReason {
	case "":
		if s.Active {
			return "仍在记录（本报告是中途生成的）"
		}
		return ""
	case "user_stopped":
		return "手动停止 / 程序退出"
	case "sample_limit":
		return "达到 6 小时记录上限"
	case "byte_limit":
		return "达到数据大小上限"
	case "collector_failed":
		return "采样器异常"
	case "write_failed":
		return "写入磁盘失败"
	case "finalize_failed":
		return "收尾时出错（已有数据仍有效）"
	case "not_finalized":
		return "未正常结束（程序被关闭或崩溃），按已写入的数据生成"
	}
	return s.StopReason
}

func (r *performanceReport) view() (performanceReportView, error) {
	v := performanceReportView{Metadata: r.Metadata, Summary: r.Summary, Stop: performanceReportStop(r.Summary)}
	if seconds := r.Summary.observedSeconds(); seconds != nil {
		v.Duration = performanceReportDuration(*seconds)
	}
	for _, m := range r.Summary.Metrics {
		if m.Count > 0 {
			v.Key = append(v.Key, m)
		}
	}
	v.Top = r.Summary.Top[:min(25, len(r.Summary.Top))]
	charts := make([]performanceReportChart, 0, len(r.charts))
	for i := range r.charts {
		if m, ok := r.finished[r.charts[i].Key]; ok {
			r.charts[i].Label, r.charts[i].Unit = m.Label, m.Unit
		}
		r.charts[i].finish()
		if len(r.charts[i].Points) > 0 { // Never draw a series that has no data.
			charts = append(charts, r.charts[i])
		}
	}
	data, err := json.Marshal(struct {
		Charts    []performanceReportChart `json:"charts"`
		Marks     []performanceReportMark  `json:"marks"`
		Gaps      []performanceReportGap   `json:"gaps"`
		EndS      *float64                 `json:"end_s"`
		StartedMS int64                    `json:"started_ms"`
	}{charts, r.Summary.UserMarks, r.Summary.Gaps, r.Summary.LastElapsedS, r.Summary.StartedAt.UnixMilli()}) // Default HTML escaping is mandatory here.
	if err != nil {
		return v, err
	}
	v.ChartJSON = template.JS(data) // Only JSON produced above, never raw JSONL or strings.
	return v, nil
}

// performanceReportNumber prints a plain decimal: never exponent notation,
// at most two decimals, and "—" when the value was not measured.
func performanceReportNumber(p *float64) string {
	if p == nil {
		return "—"
	}
	v := *p
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// Display-only conversions never modify the raw metrics, CSV or quantiles.
func performanceReportHumanValue(p *float64, unit string) string {
	if p == nil {
		return "—"
	}
	value := *p
	switch unit {
	case "%":
		return strconv.FormatFloat(value, 'f', 1, 64) + "%"
	case "ms":
		return strconv.FormatFloat(value, 'f', 1, 64) + " ms"
	case "cores":
		return strconv.FormatFloat(value, 'f', 2, 64) + " 核"
	case "seconds":
		return strconv.FormatFloat(value, 'f', 1, 64) + " 秒"
	case "count/s":
		return strconv.FormatFloat(value, 'f', 1, 64) + " /秒"
	case "bytes", "bytes/s":
		i := 0
		units := [...]string{"B", "KB", "MB", "GB", "TB"}
		for math.Abs(value) >= 1024 && i < len(units)-1 {
			value /= 1024
			i++
		}
		digits := 1
		if i == 0 {
			digits = 0
		} else if i >= 3 {
			digits = 2
		}
		text := strconv.FormatFloat(value, 'f', digits, 64) + " " + units[i]
		if unit == "bytes/s" {
			text += "/秒"
		}
		return text
	}
	return performanceReportNumber(p)
}

func performanceReportSize(value any) string {
	var number float64
	switch v := value.(type) {
	case uint64:
		number = float64(v)
	case int64:
		number = float64(v)
	case float64:
		number = v
	case *float64:
		return performanceReportHumanValue(v, "bytes")
	default:
		return "—"
	}
	return performanceReportHumanValue(&number, "bytes")
}

func performanceReportPercent(p *float64) string { return performanceReportHumanValue(p, "%") }

// performanceReportClock formats elapsed seconds as m:ss or h:mm:ss.
func performanceReportClock(seconds float64) string {
	total := int64(seconds)
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, total%3600/60, total%60)
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// One local-time format everywhere, so times in the report can be compared
// with the user's own clock ("it lagged at 20:13").
func performanceReportTime(value any) string {
	var at time.Time
	switch v := value.(type) {
	case time.Time:
		at = v
	case *time.Time:
		if v != nil {
			at = *v
		}
	}
	if at.IsZero() {
		return "—"
	}
	return at.Local().Format("2006-01-02 15:04:05")
}

func (s performanceReportSummary) observedSeconds() *float64 {
	if s.FirstElapsedS == nil || s.LastElapsedS == nil {
		return nil
	}
	return performanceReportFloat(*s.LastElapsedS - *s.FirstElapsedS)
}

// Table cells only need the column separator and line breaks neutralised;
// keys such as user_stopped stay greppable plain text.
func performanceReportMarkdownText(s string) string {
	return strings.NewReplacer("|", "/", "\r", " ", "\n", " ").Replace(s)
}

// markdown is the short text version: conclusion, who used the machine, the
// user's marks and the key metrics. Everything else is in summary.json.
func (r *performanceReport) markdown() string {
	var b strings.Builder
	s := r.Summary
	fmt.Fprintf(&b, "# 性能报告\n\n版本 %s · 开始于 %s", r.Metadata.Version, performanceReportTime(s.StartedAt))
	if seconds := s.observedSeconds(); seconds != nil {
		fmt.Fprintf(&b, " · 记录 %s", performanceReportDuration(*seconds))
	}
	if stop := performanceReportStop(s); stop != "" {
		fmt.Fprintf(&b, " · %s", stop)
	}
	b.WriteString("\n\n## 结论\n\n")
	for _, line := range s.Verdict {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	b.WriteString("\n## 关键数字\n\n")
	for _, h := range s.Headline {
		fmt.Fprintf(&b, "- %s：%s", h.Label, h.Value)
		if h.Detail != "" {
			fmt.Fprintf(&b, "（%s）", h.Detail)
		}
		b.WriteByte('\n')
	}
	b.WriteString("\n## 谁在占用资源\n\n")
	if len(s.Top) == 0 {
		b.WriteString("本次没有全系统进程数据。\n")
	} else {
		b.WriteString("| 程序 | 平均 CPU | 峰值 CPU | 平均内存 | 峰值内存 | 平均 GPU | 峰值 GPU | 峰值显存 |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, t := range s.Top[:min(15, len(s.Top))] {
			name := t.Name
			if t.Role == "emulator" {
				name += "（模拟器）"
			}
			if t.Instances > 1 {
				name += fmt.Sprintf(" ×%d", t.Instances)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", performanceReportMarkdownText(name),
				performanceReportPercent(t.CPUMean), performanceReportPercent(t.CPUPeak), performanceReportSize(t.MemoryMean), performanceReportSize(t.MemoryPeak),
				performanceReportPercent(t.GPUMean), performanceReportPercent(t.GPUPeak), performanceReportSize(t.VRAMPeak))
		}
	}
	b.WriteString("\n## 手动卡顿标记\n\n")
	if len(s.UserMarks) == 0 {
		b.WriteString("没有标记。\n")
	}
	for _, m := range s.UserMarks {
		fmt.Fprintf(&b, "- %s（记录第 %s）%s\n", performanceReportTime(m.At), performanceReportClock(m.ElapsedS), m.Label)
	}
	b.WriteString("\n## 环境\n\n")
	sys := r.Metadata.System
	fmt.Fprintf(&b, "- 系统：%s %s（%s）\n- CPU：%s · %d 逻辑核\n- 内存：%s\n", sys.OS, sys.OSVersion, sys.NativeArch, sys.CPUName, sys.LogicalCPUs, performanceReportSize(sys.TotalRAMBytes))
	for _, gpu := range sys.GPUs {
		fmt.Fprintf(&b, "- 显卡：%s · 驱动 %s", gpu.Name, gpu.Driver)
		if gpu.DedicatedBytes > 0 {
			fmt.Fprintf(&b, " · 显存 %s", performanceReportSize(gpu.DedicatedBytes))
		}
		b.WriteByte('\n')
	}
	if s.Backend != "" || s.Resolution != "" {
		fmt.Fprintf(&b, "- 采集：%s %s\n", s.Backend, s.Resolution)
	}
	if s.LastError != "" {
		fmt.Fprintf(&b, "- 记录器错误：%s\n", s.LastError)
	}
	b.WriteString("\n## 主要指标\n\n| 指标 | 平均 | 峰值 | P95 | 首 | 末 |\n|---|---:|---:|---:|---:|---:|\n")
	for _, m := range s.Metrics {
		if m.Count == 0 {
			continue
		}
		fmt.Fprintf(&b, "| %s (`%s`) | %s | %s | %s | %s | %s |\n", performanceReportMarkdownText(m.Label), m.Key, performanceReportHumanValue(m.Mean, m.Unit),
			performanceReportHumanValue(m.Peak, m.Unit), performanceReportHumanValue(m.P95, m.Unit), performanceReportHumanValue(m.First, m.Unit), performanceReportHumanValue(m.Last, m.Unit))
	}
	b.WriteString("\n## 说明\n\n")
	for _, note := range s.Notes {
		fmt.Fprintf(&b, "- %s\n", note)
	}
	b.WriteString("\n完整数据见 summary.json、samples.csv、samples.jsonl；图表见 report.html。\n")
	return b.String()
}
