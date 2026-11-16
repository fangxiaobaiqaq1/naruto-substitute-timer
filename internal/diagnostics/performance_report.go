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
	"html"
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
	"number": performanceReportNumber,
	"human":  performanceReportHumanValue,
	"size":   performanceReportSize,
	"clock":  performanceReportTime,
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
		return "", fmt.Errorf("performance timeline exceeds the 64 MiB snapshot limit")
	}
	directory, err := filepath.Abs(filepath.Dir(timelinePath))
	if err != nil {
		return "", err
	}
	if status.Directory != "" {
		d, err := filepath.Abs(status.Directory)
		if err != nil || d != directory {
			return "", errors.New("performance timeline is outside the session directory")
		}
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
		f, err := performanceReportOpenRegular(timelinePath)
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
		if err := performanceReportScan(ctx, reader, func(s performanceReportSample) error { return report.add(s) }); err != nil {
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

	// Never walk the session directory. Profiles are independent, size-bounded
	// snapshots, not promises of exact alignment with the timeline endpoints.
	for _, name := range []string{"heap-start.pprof", "heap-end.pprof", "goroutines-start.txt", "goroutines-end.txt"} {
		p := performanceReportProfile{Name: name, Status: status.ProfileStatus[name], Result: "文件不存在；未包含"}
		// A coincidentally named file is not evidence of an opted-in, completed
		// profile. Do not even inspect it unless the captured status permits it.
		if !metadata.ProfilesRequested || p.Status != "ok" {
			p.Result = "采集尚未完成或失败（status 非 ok）；未包含"
			if !metadata.ProfilesRequested {
				p.Result = "本会话未请求 profiles；未包含"
			}
			report.Summary.Profiles = append(report.Summary.Profiles, p)
			continue
		}
		path := filepath.Join(directory, name)
		info, statErr := os.Lstat(path)
		switch {
		case os.IsNotExist(statErr):
		case statErr != nil:
			return "", fmt.Errorf("inspect optional performance profile %s: %w", name, statErr)
		case !info.Mode().IsRegular():
			p.Result = "不是普通文件（含符号链接）；未包含"
		case info.Size() > performanceReportProfileLimit:
			p.Result = "超过单文件 16 MiB 限制；未包含"
		default:
			f, err := performanceReportOpenRegular(path)
			if err != nil {
				return "", err
			}
			opened, err := f.Stat()
			if err != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() {
				f.Close()
				return "", errors.New("optional performance profile changed while opening")
			}
			w, err := entry(name)
			if err == nil {
				_, err = io.CopyN(w, f, opened.Size())
			}
			closeErr := f.Close()
			if err != nil {
				return "", err
			}
			if closeErr != nil {
				return "", closeErr
			}
			p.Included, p.Bytes, p.Result = true, info.Size(), "已包含已完成快照（status=ok）"
		}
		report.Summary.Profiles = append(report.Summary.Profiles, p)
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

func performanceReportOpenRegular(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("performance input must be a regular file, not a link or device")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		f.Close()
		return nil, errors.New("performance input changed while opening")
	}
	return f, nil
}

// Pointer-valued wire maps intentionally differ from the shared recorder types:
// encoding/json otherwise silently converts a JSON null float64 into a real 0.
// Unknown struct fields are rejected so raw samples cannot smuggle config dumps.
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

func performanceReportScan(ctx context.Context, r io.Reader, visit func(performanceReportSample) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), performanceReportLineLimit+1)
	line := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line++
		if line > PerformanceMaxSamples {
			return errors.New("performance snapshot exceeds 21600 samples")
		}
		data := scanner.Bytes()
		if len(data) > performanceReportLineLimit || !utf8.Valid(data) {
			return fmt.Errorf("performance sample %d: oversized line or invalid UTF-8", line)
		}
		var s performanceReportSample
		d := json.NewDecoder(bytes.NewReader(data))
		d.DisallowUnknownFields()
		if err := d.Decode(&s); err != nil {
			return fmt.Errorf("performance sample %d: invalid JSON: %w", line, err)
		}
		if err := d.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("performance sample %d: trailing JSON data", line)
		}
		if s.At.IsZero() || s.Elapsed == nil || s.Interval == nil || *s.Elapsed < 0 || *s.Interval < 0 || (s.Collector != nil && *s.Collector < 0) {
			return fmt.Errorf("performance sample %d: missing or invalid timing", line)
		}
		s.ElapsedS, s.IntervalS = *s.Elapsed, *s.Interval
		if len(s.Procs) > 64 || len(s.GPUEngines) > 256 || len(s.Markers) > 64 || len(s.Metrics) > performanceReportMetricLimit {
			return fmt.Errorf("performance sample %d: too many metrics/processes/engines/markers", line)
		}
		s.Values = performanceReportValues(s.Metrics)
		for _, p := range s.Procs {
			if p.PID <= 0 || len(p.Values) > performanceReportMetricLimit {
				return fmt.Errorf("performance sample %d: invalid process", line)
			}
			for key, value := range p.Values {
				if len(key) > 256 || (value != nil && math.Abs(*value) > 1e100) {
					return fmt.Errorf("performance sample %d: process metric exceeds supported range", line)
				}
			}
			p.performanceReportProcessAlias.Values = performanceReportValues(p.Values)
			// Names are basenames by contract. Reject paths instead of exporting a
			// username accidentally embedded in a collector's executable path.
			if strings.ContainsAny(p.Name, `/\\`) {
				return fmt.Errorf("performance sample %d: process name is not a basename", line)
			}
			s.Processes = append(s.Processes, PerformanceProcess(p.performanceReportProcessAlias))
		}
		if s.App != nil {
			s.Application = PerformanceApplication(s.App.performanceReportApplicationAlias)
		}
		if err := visit(s); err != nil {
			return fmt.Errorf("performance sample %d: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read performance snapshot (line limit 1 MiB): %w", err)
	}
	return ctx.Err()
}

func performanceReportValues(values map[string]*float64) map[string]float64 {
	out := make(map[string]float64, len(values))
	for key, v := range values {
		if v != nil {
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

func (a *performanceReportAccumulator) finish(samples int) performanceReportMetric {
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
	if m.Count > 1 {
		m.Change = performanceReportFloat(*m.Last - *m.First)
		if a.sTT > 0 {
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
	// Error details can contain a home directory. Keep a flag, not an OS path.
	RecorderError              bool                           `json:"recorder_error"`
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
	reasons  map[string]*performanceReportUnavailable
	previous *performanceReportSample
	charts   []performanceReportChart
	width    int
}

var performanceReportExtraMetrics = []PerformanceMetric{
	{"main_cpu_cores", "主进程 CPU 核等效（coreEquivalent）", "cores"},
	{"main_private_resident_bytes", "主进程私有驻留（Linux）", "bytes"},
	{"tree_private_resident_bytes", "进程树私有驻留（Linux）", "bytes"},
	{"main_virtual_bytes", "主进程虚拟地址空间", "bytes"},
	{"tree_virtual_bytes", "进程树虚拟地址空间合计", "bytes"},
	{"main_threads", "主进程线程数", "count"},
	{"main_handles", "主进程句柄数（Windows）", "count"},
	{"main_fds", "主进程文件描述符（Linux）", "count"},
	{"go_alloc_total_bytes", "Go 累计分配量", "bytes"},
	{"go_next_gc_bytes", "Go 下次 GC 目标堆", "bytes"},
	{"go_mallocs_total", "Go 累计分配对象", "count"},
	{"go_frees_total", "Go 累计释放对象", "count"},
	{"go_memory_limit_disabled", "Go 软内存上限未设置（1=是）", "flag"},
	{"go_gc_disabled", "自动 GC 已关闭 / GOGC off（1=是）", "flag"},
	{"capture_ms", "采集耗时", "ms"},
	{"analysis_ms", "分析耗时", "ms"},
	{"delivery_age_ms", "交付帧龄", "ms"},
	{"capture_p95_ms", "采集窗口 P95", "ms"},
	{"analysis_p95_ms", "分析窗口 P95", "ms"},
	{"ui_queue_ms", "UI 队列等待", "ms"},
	{"collector_ms", "采样开销（仅采集与统计）", "ms"},
}

func newPerformanceReport(m PerformanceMetadata, s PerformanceStatus, n int64) *performanceReport {
	m.Build = performanceReportBuild(m.Build)
	r := &performanceReport{Metadata: m, metrics: make(map[string]*performanceReportAccumulator), procs: make(map[string]*performanceReportProcess), reasons: make(map[string]*performanceReportUnavailable), width: 1}
	r.Summary = performanceReportSummary{
		SchemaVersion: 1, GeneratedAt: time.Now().UTC(), StartedAt: m.StartedAt, EndedAt: s.EndedAt,
		Active: s.Active, StopReason: s.StopReason, RecorderError: s.LastError != "", ProfileStatus: s.ProfileStatus,
		TimelineBytes: n, ExpectedIntervalS: float64(m.IntervalMS) / 1000,
		PendingMarkers: append([]string(nil), s.PendingMarkers...),
		Notes: []string{
			"仅导出性能元数据和指定字节前缀；不读取活跃尾部、旧诊断、截图、config、用户名、命令行或其他进程名称。Latest 不属于本快照。",
			"CPU 百分比按全机逻辑核归一：100% 表示所有逻辑核满载。coreEquivalent = CPU% × 逻辑核数 / 100；核数未知时不推算。系统/主进程/已观测子进程树不是同一口径。",
			"进程 CPU/IO 速率使用记录器提供的 counter_interval_seconds（实际计数器读取间隔）；原始进程 counter_at 是读取时刻的墙钟表示，counter_age_ms 表示时效。sample.at/interval_s 是样本级时序，不代替各进程时间窗，也不用于重算速率。",
			"进程树仅含已观测后代；短命、无权限或尚未观测的子进程可能缺失。工作集合计可能重复计算共享页，不能视为独占物理内存。Windows 私有提交不等于 Linux RSS/私有驻留。",
			"ORT/ONNX Runtime、显卡驱动及其他原生分配不在 Go heap 中；Go 堆下降不意味着全部进程内存下降。内存首末增长和趋势只提示排查线索，不能据此判定泄漏。",
			"GPU 为最忙引擎百分比，不累加不同引擎；OCR GPU 请求/配置不证明实际执行后端。GPU/显存、句柄、IO 等可能受平台、驱动、权限影响。IO 包含管道/设备，不等于物理磁盘吞吐；磁盘余量是报告所在卷。",
			"缺失值使用 JSON null、CSV 空单元格和页面“不可用”，绝不补 0；真实 0 参与统计。baseline / counter reset 不伪造计数器增量；派生速率采用记录器提供的值。",
			"首末为该指标最早/最晚有效观测；均值和最近秩 P50/P95/P99 按样本等权，不作时间加权或插值。趋势为有效样本对 elapsed_s 的最小二乘斜率 × 60；每分钟桶 mean/peak/count 见 summary.json，不填空桶。",
			"缺失区间按相邻 elapsed_s 超过期望间隔 1.5 倍估计（含首样本前）；暂停/调度抖动也可能产生间隙。墙钟倒退单独标记，曲线和趋势使用 elapsed_s。",
			"卡顿标记来自记录器 markers 或 slow_frames 计数增加；采集/分析 FPS 是当前采样窗口次数率，不是游戏源 FPS。耗时、UI 队列等待和采样开销都不等于物理呈现/端到端延迟。",
			"collector_ms 仅测原生/运行时采集与统计，不含后续序列化、写盘或报告导出，不能当作总监控开销；导出自身可能扰动仍在采集的会话。",
			"pending_markers 是已接受但尚未写入本次 timeline 前缀的标记（可能含导出开始/结束）；独立列示，不伪造时间点、不追加到原始样本或统计曲线。",
			"go_memory_limit_disabled=1 表示没有设置 Go 软内存上限；go_gc_disabled=1 表示 GOGC off。对应限制/百分比省略并注明原因，不是 0 bytes 或 0%；缺少 disabled 标志也不推断为 0。",
			"图表仅保留必要指标；自适应分桶保留首末与最小/最大值，缺失桶以断开的极值点表示，禁止跨缺失连线。精确原始 samples.jsonl、完整 samples.csv 及全量统计随包提供。",
			"限制：输入 64 MiB、单行 1 MiB、21600 样本、512 指标、每样本 64 进程/256 GPU 引擎；进程汇总最多 1024 身份，可用性原因最多 512 类，超出计数可见且原始样本不裁剪。",
			"可选 profile 仅在 metadata.profiles_requested=true 且对应 status.ProfileStatus 为 ok 时读取固定四项普通文件（每项最多 16 MiB）；同名文件不能绕过请求和完成状态检查。它们是独立文件快照，可能缺少结束快照；Go heap profile 不能描述 ORT 原生堆。",
		},
	}
	if r.Summary.ExpectedIntervalS <= 0 {
		r.Summary.ExpectedIntervalS = PerformanceInterval.Seconds()
	}
	for _, metrics := range [][]PerformanceMetric{PerformanceMetrics, performanceReportExtraMetrics} {
		for _, metric := range metrics {
			r.metrics[metric.Key] = &performanceReportAccumulator{metric: performanceReportMetric{PerformanceMetric: metric}}
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
	} {
		if value != nil {
			v[key] = *value
		}
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
	// These guard arithmetic and minute-index overflow, not a six-hour wall-time
	// assumption: suspended recordings can legitimately span longer periods.
	if s.ElapsedS > 1e12 || s.IntervalS > 1e12 {
		return errors.New("timing exceeds supported numeric range")
	}
	values := r.values(s)
	// Include unknown and explicitly null keys, even if never available.
	for key := range s.Metrics {
		if err := r.ensureMetric(key); err != nil {
			return err
		}
	}
	for key, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1e100 {
			return errors.New("metric exceeds supported numeric range")
		}
		if err := r.ensureMetric(key); err != nil {
			return err
		}
		r.metrics[key].add(s.ElapsedS, value)
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
	markers := append([]string(nil), s.Markers...)
	if r.previous == nil {
		markers = append(markers, "baseline：首样本只建立累计计数器基线；缺失速率不是 0")
	} else {
		if s.At.Before(r.previous.At) {
			markers = append(markers, "wall_clock_reset：墙钟倒退，使用 elapsed_s")
		}
		for _, key := range []string{"system_cpu_total_seconds", "system_cpu_idle_seconds", "go_gc_count", "go_alloc_total_bytes", "go_mallocs_total", "go_frees_total"} {
			a, aok := r.previous.Values[key]
			b, bok := s.Values[key]
			if aok && bok && b < a {
				markers = append(markers, "counter reset："+key)
			}
		}
		if s.App != nil && s.App.SlowFrames != nil && r.previous.App != nil && r.previous.App.SlowFrames != nil {
			current, previous := *s.App.SlowFrames, *r.previous.App.SlowFrames
			if current > previous {
				markers = append(markers, fmt.Sprintf("卡顿：slow_frames +%d", current-previous))
			} else if current < previous {
				markers = append(markers, "counter reset：slow_frames")
			}
		}
	}
	baselines, resets := []string{}, []string{}
	previousProcesses := make(map[int]PerformanceProcess)
	if r.previous != nil {
		for _, p := range r.previous.Processes {
			previousProcesses[p.PID] = p
		}
	}
	for _, p := range s.Processes {
		prior, ok := previousProcesses[p.PID]
		if !ok || p.StartID == "" || prior.StartID != p.StartID {
			baselines = append(baselines, strconv.Itoa(p.PID))
		} else {
			for _, key := range []string{"cpu_seconds", "io_read_bytes", "io_write_bytes", "page_faults"} {
				a, aok := prior.Values[key]
				b, bok := p.Values[key]
				if aok && bok && b < a {
					resets = append(resets, fmt.Sprintf("%d/%s", p.PID, key))
				}
			}
		}
		if err := r.addProcess(p, s.ElapsedS); err != nil {
			return err
		}
		r.unavailable("process:"+strconv.Itoa(p.PID), p.Unavailable, s.ElapsedS)
	}
	if len(baselines) != 0 {
		markers = append(markers, "baseline：新出现/重启/重新观测 PID "+strings.Join(baselines, ","))
	}
	if len(resets) != 0 {
		markers = append(markers, "counter reset："+strings.Join(resets, ","))
	}
	if len(markers) != 0 {
		r.Summary.Events = append(r.Summary.Events, performanceReportEvent{ElapsedS: s.ElapsedS, Markers: markers})
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

func (r *performanceReport) ensureMetric(key string) error {
	if _, ok := r.metrics[key]; ok {
		return nil
	}
	if len(r.metrics) >= performanceReportMetricLimit || len(key) > 256 {
		return errors.New("too many metric keys or metric key exceeds 256 bytes")
	}
	r.metrics[key] = &performanceReportAccumulator{metric: performanceReportMetric{PerformanceMetric: PerformanceMetric{Key: key, Label: key}}}
	return nil
}

func (r *performanceReport) addProcess(p PerformanceProcess, at float64) error {
	id := strconv.Itoa(p.PID) + ":" + p.StartID
	row := r.procs[id]
	if row == nil {
		if len(r.procs) >= performanceReportProcessLimit {
			r.Summary.OmittedProcessObservations++
			return nil
		}
		row = &performanceReportProcess{PID: p.PID, Parent: p.ParentPID, Name: p.Name, StartID: p.StartID, FirstS: at, Metrics: make(map[string]performanceReportProcessMetric)}
		r.procs[id] = row
	}
	row.LastS, row.Samples = at, row.Samples+1
	for key, value := range p.Values {
		m := row.Metrics[key]
		if m.Count == 0 {
			if len(row.Metrics) >= performanceReportMetricLimit {
				return errors.New("too many distinct metrics for one process")
			}
			m.First, m.Peak = value, value
		}
		m.Count++
		m.Last, m.Peak = value, math.Max(m.Peak, value)
		m.Mean += (value - m.Mean) / float64(m.Count)
		row.Metrics[key] = m
	}
	return nil
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
	keys := make([]string, 0, len(r.metrics))
	for key := range r.metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.Summary.Metrics = append(r.Summary.Metrics, r.metrics[key].finish(r.Summary.Samples))
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
	for _, reason := range r.reasons {
		r.Summary.Unavailable = append(r.Summary.Unavailable, *reason)
	}
	sort.Slice(r.Summary.Unavailable, func(i, j int) bool {
		a, b := r.Summary.Unavailable[i], r.Summary.Unavailable[j]
		return a.Scope+"\x00"+a.Metric+"\x00"+a.Reason < b.Scope+"\x00"+b.Metric+"\x00"+b.Reason
	})
	// Release quantile storage before serializing the report and attachments.
	r.metrics, r.procs, r.reasons, r.previous = nil, nil, nil, nil
	return nil
}

func (r *performanceReport) csv(ctx context.Context, source io.Reader, dest io.Writer) error {
	w := csv.NewWriter(dest)
	header := []string{"at", "elapsed_s", "interval_s", "markers_json", "unavailable_json"}
	for _, metric := range r.Summary.Metrics {
		header = append(header, performanceReportCSVText(metric.Key))
	}
	if err := w.Write(header); err != nil {
		return err
	}
	err := performanceReportScan(ctx, source, func(s performanceReportSample) error {
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

func performanceReportCSVText(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if (len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0]))) || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") {
		return "'" + s
	}
	return s
}

// A chart bucket retains extrema at their original times, not averaged peaks.
// Compaction merges adjacent buckets, keeping this invariant at every level.
type performanceReportPoint struct{ T, V float64 }
type performanceReportBucket struct {
	first, last, low, high performanceReportPoint
	valid, broken          bool
}

type performanceReportChart struct {
	Key     string   `json:"key"`
	Group   string   `json:"group"`
	Label   string   `json:"label"`
	Unit    string   `json:"unit"`
	Points  [][2]any `json:"points"`
	buckets []performanceReportBucket
}

var performanceReportChartGroups = []struct {
	Title string
	Keys  []string
}{
	{"CPU · 全逻辑核归一 %", []string{"main_cpu_percent", "tree_cpu_percent", "system_cpu_percent"}},
	{"内存 · bytes", []string{"main_working_set_bytes", "tree_working_set_bytes", "tree_private_bytes", "go_heap_alloc_bytes"}},
	{"系统物理内存使用率 · %", []string{"system_memory_used_percent"}},
	{"GPU · 最忙引擎 %", []string{"tree_gpu_busiest_engine_percent", "system_gpu_busiest_engine_percent"}},
	{"显存 · bytes", []string{"tree_gpu_dedicated_bytes", "tree_gpu_shared_bytes"}},
	{"IO · bytes/s", []string{"tree_io_read_bytes_s", "tree_io_write_bytes_s"}},
	{"磁盘余量 · bytes", []string{"disk_free_bytes"}},
	{"窗口交付 / 分析 · count/s", []string{"capture_fps", "analysis_fps"}},
	{"耗时 / UI / 采集统计 · ms", []string{"capture_ms", "analysis_ms", "ui_queue_ms", "collector_ms", "go_gc_pause_ms"}},
	{"线程 / 句柄 / Go 协程 · count", []string{"tree_threads", "tree_handles", "tree_fds", "go_goroutines"}},
}

func (c *performanceReportChart) add(index int, t float64, values map[string]float64, gap bool) {
	if index == len(c.buckets) {
		c.buckets = append(c.buckets, performanceReportBucket{})
	}
	b := &c.buckets[index]
	v, ok := values[c.Key]
	b.broken = b.broken || gap || !ok
	if !ok {
		return
	}
	p := performanceReportPoint{t, v}
	if !b.valid {
		b.first, b.low, b.high, b.valid = p, p, p, true
	}
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
			a.broken = a.broken || b.broken
			if !a.valid {
				broken := a.broken
				a = b
				a.broken = broken
			} else if b.valid {
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

func (c *performanceReportChart) finish() {
	for _, b := range c.buckets {
		if !b.valid {
			c.Points = append(c.Points, [2]any{nil, nil})
			continue
		}
		points := []performanceReportPoint{b.first, b.low, b.high, b.last}
		sort.Slice(points, func(i, j int) bool { return points[i].T < points[j].T })
		var previous *performanceReportPoint
		for i := range points {
			p := &points[i]
			if previous != nil && p.T == previous.T {
				continue
			}
			if b.broken {
				c.Points = append(c.Points, [2]any{nil, nil})
			}
			c.Points = append(c.Points, [2]any{p.T, p.V})
			previous = p
		}
		if b.broken {
			c.Points = append(c.Points, [2]any{nil, nil})
		}
	}
	c.buckets = nil
}

type performanceReportProcessView struct {
	*performanceReportProcess
	CPU, Cores, WorkingSet, Private, PrivateResident, GPU, VRAM, GPUShared, IORead, IOWrite, Threads, Handles, FDs *float64
}

type performanceReportView struct {
	Metadata            PerformanceMetadata
	Summary             performanceReportSummary
	Processes           []performanceReportProcessView
	Events              []performanceReportEvent
	ChartJSON           template.JS
	ObservedSeconds     *float64
	ShortSessionWarning string
}

func (r *performanceReport) view() (performanceReportView, error) {
	v := performanceReportView{Metadata: r.Metadata, Summary: r.Summary,
		ObservedSeconds: r.Summary.observedSeconds(), ShortSessionWarning: r.Summary.shortSessionWarning()}
	for _, p := range r.Summary.Processes {
		peak := func(key string) *float64 {
			if m, ok := p.Metrics[key]; ok {
				return performanceReportFloat(m.Peak)
			}
			return nil
		}
		cores := peak("cpu_cores")
		if cores == nil && peak("cpu_percent") != nil && r.Metadata.System.LogicalCPUs > 0 {
			cores = performanceReportFloat(*peak("cpu_percent") * (float64(r.Metadata.System.LogicalCPUs) / 100))
		}
		v.Processes = append(v.Processes, performanceReportProcessView{performanceReportProcess: p,
			CPU: peak("cpu_percent"), Cores: cores, WorkingSet: peak("working_set_bytes"), Private: peak("private_bytes"), PrivateResident: peak("private_resident_bytes"),
			GPU: peak("gpu_busiest_engine_percent"), VRAM: peak("gpu_dedicated_bytes"), GPUShared: peak("gpu_shared_bytes"), IORead: peak("io_read_bytes_s"), IOWrite: peak("io_write_bytes_s"), Threads: peak("threads"), Handles: peak("handles"), FDs: peak("fds")})
	}
	v.Events = r.Summary.Events[:min(200, len(r.Summary.Events))]
	for i := range r.charts {
		for _, metric := range r.Summary.Metrics {
			if metric.Key == r.charts[i].Key {
				r.charts[i].Label, r.charts[i].Unit = metric.Label, metric.Unit
				break
			}
		}
		r.charts[i].finish()
	}
	stalls := make([]float64, 0)
	for _, event := range r.Summary.Events {
		for _, marker := range event.Markers {
			lower := strings.ToLower(marker)
			if strings.Contains(lower, "counter reset") {
				continue
			}
			if strings.Contains(lower, "slow") || strings.Contains(lower, "stall") || strings.Contains(lower, "jank") || strings.Contains(lower, "卡顿") {
				stalls = append(stalls, event.ElapsedS)
				break
			}
		}
	}
	data, err := json.Marshal(struct {
		Charts []performanceReportChart `json:"charts"`
		Stalls []float64                `json:"stalls"`
		Gaps   []performanceReportGap   `json:"gaps"`
		EndS   *float64                 `json:"end_s"`
	}{r.charts, stalls, r.Summary.Gaps, r.Summary.LastElapsedS}) // Default HTML escaping is mandatory here.
	if err != nil {
		return v, err
	}
	v.ChartJSON = template.JS(data) // Only JSON produced above, never raw JSONL or strings.
	return v, nil
}

func performanceReportNumber(p *float64) string {
	if p == nil {
		return "不可用"
	}
	return strconv.FormatFloat(*p, 'g', 7, 64)
}

// Display-only conversions never modify the raw metrics, CSV or quantiles.
func performanceReportHumanValue(p *float64, unit string) string {
	if p == nil || (unit != "bytes" && unit != "bytes/s") {
		return performanceReportNumber(p)
	}
	value, i := *p, 0
	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	for math.Abs(value) >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	number := performanceReportNumber(&value)
	if i > 0 {
		number = strconv.FormatFloat(value, 'f', 2, 64)
	}
	suffix := units[i]
	if unit == "bytes/s" {
		suffix += "/s"
	}
	return number + " " + suffix
}

func performanceReportSize(value any) string {
	var number float64
	switch v := value.(type) {
	case uint64:
		number = float64(v)
	case int64:
		number = float64(v)
	default:
		return "不可用"
	}
	return performanceReportHumanValue(&number, "bytes")
}

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
		return "不可用"
	}
	return at.Format("2006-01-02 15:04:05.000 UTC-07:00")
}

func (s performanceReportSummary) observedSeconds() *float64 {
	if s.FirstElapsedS == nil || s.LastElapsedS == nil {
		return nil
	}
	return performanceReportFloat(*s.LastElapsedS - *s.FirstElapsedS)
}

func (s performanceReportSummary) shortSessionWarning() string {
	if seconds := s.observedSeconds(); seconds != nil && *seconds < 60 {
		return "短时会话：观测不足 60 秒，趋势可能受预热/GC 周期影响；不能用短期增长判定泄漏。趋势仍按至少 2 个有效样本计算。"
	}
	return ""
}

// Match the recorder's build-identity allowlist, not the complete Go build
// settings (which may contain -ldflags, environment or local module paths).
func performanceReportBuild(build map[string]string) map[string]string {
	out := make(map[string]string)
	for _, key := range []string{"vcs.revision", "vcs.time", "vcs.modified", "GOOS", "GOARCH", "GOAMD64", "GOARM", "GOARM64", "CGO_ENABLED", "-compiler", "-buildmode"} {
		if value, ok := build[key]; ok {
			out[key] = value
		}
	}
	return out
}

func performanceReportMarkdownText(s string) string {
	s = html.EscapeString(s)
	return strings.NewReplacer("|", "&#124;", "\r", " ", "\n", " ", "`", "&#96;", "[", "&#91;", "]", "&#93;", "*", "&#42;", "_", "&#95;").Replace(s)
}

func (r *performanceReport) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 独立性能报告\n\n版本：%s；样本：%d；快照：%d bytes；active：%t；样本不足：%t。\n\n", performanceReportMarkdownText(r.Metadata.Version), r.Summary.Samples, r.Summary.TimelineBytes, r.Summary.Active, r.Summary.Insufficient)
	ended := performanceReportTime(r.Summary.EndedAt)
	if r.Summary.Active && r.Summary.EndedAt == nil {
		ended = "尚未结束（本次快照）"
	}
	fmt.Fprintf(&b, "会话开始：%s；首次观测：%s；最后观测：%s；会话结束：%s。\n\n观测时长（末次减首次 elapsed_s）：%s 秒；生成时间：%s。\n\n", performanceReportTime(r.Summary.StartedAt), performanceReportTime(r.Summary.FirstAt), performanceReportTime(r.Summary.LastAt), ended, performanceReportNumber(r.Summary.observedSeconds()), performanceReportTime(r.Summary.GeneratedAt))
	if warning := r.Summary.shortSessionWarning(); warning != "" {
		fmt.Fprintf(&b, "> %s\n\n", warning)
	}
	fmt.Fprintf(&b, "逻辑核：%d；系统：%s；CPU：%s。\n\nProfilesRequested：%t（是否请求采集；不等于文件已完成）。\n\n", r.Metadata.System.LogicalCPUs, performanceReportMarkdownText(r.Metadata.System.OS), performanceReportMarkdownText(r.Metadata.System.CPUName), r.Metadata.ProfilesRequested)
	buildKeys := make([]string, 0, len(r.Metadata.Build))
	for key := range r.Metadata.Build {
		buildKeys = append(buildKeys, key)
	}
	sort.Strings(buildKeys)
	b.WriteString("构建标识（白名单）：\n\n")
	if len(buildKeys) == 0 {
		b.WriteString("- 未提供。\n")
	}
	for _, key := range buildKeys {
		fmt.Fprintf(&b, "- %s：%s\n", performanceReportMarkdownText(key), performanceReportMarkdownText(r.Metadata.Build[key]))
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "停止原因：%s；记录器错误标志：%t（不导出可能含用户路径的错误原文）。\n\n", performanceReportMarkdownText(r.Summary.StopReason), r.Summary.RecorderError)
	b.WriteString("## 指标汇总\n\nbytes / bytes/s 仅在显示时按 1024 换算；JSON/CSV 保留原始数值和单位。第二列为原始单位，分钟趋势为显示单位/分钟。增长只作线索，不能判定泄漏。\n\n| 指标 | 原始单位 | 有效/缺失 | 首 | 末 | 峰值 | 均值 | P50 | P95 | P99 | 首末差 | 趋势/分钟 |\n|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, m := range r.Summary.Metrics {
		fmt.Fprintf(&b, "| %s | %s | %d/%d | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", performanceReportMarkdownText(m.Label), performanceReportMarkdownText(m.Unit), m.Count, m.Missing, performanceReportHumanValue(m.First, m.Unit), performanceReportHumanValue(m.Last, m.Unit), performanceReportHumanValue(m.Peak, m.Unit), performanceReportHumanValue(m.Mean, m.Unit), performanceReportHumanValue(m.P50, m.Unit), performanceReportHumanValue(m.P95, m.Unit), performanceReportHumanValue(m.P99, m.Unit), performanceReportHumanValue(m.Change, m.Unit), performanceReportHumanValue(m.TrendPerMinute, m.Unit))
	}
	b.WriteString("\n## 进程（按 PID + StartID 区分重启）\n\n| PID | PPID | 名称 | 首/末秒 | 样本 |\n|---:|---:|---|---|---:|\n")
	for _, p := range r.Summary.Processes {
		fmt.Fprintf(&b, "| %d | %d | %s | %.3f / %.3f | %d |\n", p.PID, p.Parent, performanceReportMarkdownText(p.Name), p.FirstS, p.LastS, p.Samples)
	}
	b.WriteString("\n## 缺失区间 / baseline / counter reset / 卡顿\n\n")
	for _, gap := range r.Summary.Gaps {
		fmt.Fprintf(&b, "- %.3f → %.3f 秒：估计缺失 %d 个采样位置。\n", gap.FromS, gap.ToS, gap.EstimatedMissing)
	}
	for _, e := range r.Summary.Events[:min(200, len(r.Summary.Events))] {
		fmt.Fprintf(&b, "- %.3f 秒：%s\n", e.ElapsedS, performanceReportMarkdownText(strings.Join(e.Markers, "；")))
	}
	fmt.Fprintf(&b, "\n事件展示前 200 项；共 %d 项，完整见 summary.json / samples.jsonl。\n\n### 待落盘标记（已接受，尚未进入本次前缀）\n\n", len(r.Summary.Events))
	if len(r.Summary.PendingMarkers) == 0 {
		b.WriteString("无待落盘标记。\n")
	}
	for _, marker := range r.Summary.PendingMarkers {
		fmt.Fprintf(&b, "- %s\n", performanceReportMarkdownText(marker))
	}
	b.WriteString("\n以上 pending_markers 不参与原始样本/统计/曲线。\n\n## 可用性与 profiles\n\n")
	for _, u := range r.Summary.Unavailable {
		fmt.Fprintf(&b, "- %s / %s：%s（%d 次，%.3f–%.3f 秒）\n", performanceReportMarkdownText(u.Scope), performanceReportMarkdownText(u.Metric), performanceReportMarkdownText(u.Reason), u.Count, u.FirstS, u.LastS)
	}
	for _, p := range r.Summary.Profiles {
		fmt.Fprintf(&b, "- %s：%s；status=%s；%d bytes。\n", p.Name, p.Result, performanceReportMarkdownText(p.Status), p.Bytes)
	}
	fmt.Fprintf(&b, "\n汇总省略进程观测 %d 次、可用性原因 %d 次；原始样本完整保留。\n\n## 口径与限制\n\n", r.Summary.OmittedProcessObservations, r.Summary.OmittedReasons)
	for _, note := range r.Summary.Notes {
		fmt.Fprintf(&b, "- %s\n", performanceReportMarkdownText(note))
	}
	return b.String()
}
