package diagnostics

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var performanceReportTestTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func performanceReportTestMetadata() PerformanceMetadata {
	return PerformanceMetadata{SchemaVersion: 1, Version: "测试版本", StartedAt: performanceReportTestTime, RootPID: 42,
		IntervalMS: 1000, System: PerformanceSystem{OS: "synthetic", LogicalCPUs: 8}}
}

func performanceReportTestSample(second float64) PerformanceSample {
	return PerformanceSample{At: performanceReportTestTime.Add(time.Duration(second * float64(time.Second))),
		ElapsedS: second, IntervalS: 1, CollectMS: 0, Values: map[string]float64{},
		Application: PerformanceApplication{Backend: "synthetic"}}
}

func performanceReportTestJSONL(t *testing.T, samples ...PerformanceSample) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, s := range samples {
		if err := json.NewEncoder(&b).Encode(s); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func performanceReportTestWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func performanceReportTestZIP(t *testing.T, path string) map[string][]byte {
	t.Helper()
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	files := make(map[string][]byte)
	for _, f := range z.File {
		if _, duplicate := files[f.Name]; duplicate || filepath.Base(f.Name) != f.Name {
			t.Fatalf("unsafe/duplicate ZIP name: %q", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = data
	}
	return files
}

func performanceReportTestSummary(t *testing.T, files map[string][]byte) performanceReportSummary {
	t.Helper()
	var s performanceReportSummary
	if err := json.Unmarshal(files["summary.json"], &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func performanceReportTestMetric(t *testing.T, s performanceReportSummary, key string) performanceReportMetric {
	t.Helper()
	for _, m := range s.Metrics {
		if m.Key == key {
			return m
		}
	}
	t.Fatalf("missing metric %q", key)
	return performanceReportMetric{}
}

func performanceReportTestNumber(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Fatalf("value %v, want %g", got, want)
	}
}

func performanceReportTestNoTemps(t *testing.T, directory string) {
	t.Helper()
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".performance-") {
			t.Errorf("unremoved temporary file: %s", f.Name())
		}
	}
}

func performanceReportTestExport(t *testing.T, status PerformanceStatus, data []byte) (string, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	timeline := filepath.Join(dir, "samples.jsonl")
	performanceReportTestWrite(t, timeline, data)
	status.Directory = dir
	path, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), status, timeline, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	performanceReportTestNoTemps(t, dir)
	return dir, performanceReportTestZIP(t, path)
}

func performanceReportTestFloat(v float64) *float64 { return &v }

// A missing value is a gap, never 0: it must not enter means, and the CSV cell
// stays empty. A real 0 is a value.
func TestPerformanceReportMissingIsNotZero(t *testing.T) {
	a, b, c := performanceReportTestSample(1), performanceReportTestSample(2), performanceReportTestSample(3)
	a.Values["tree_cpu_percent"], c.Values["tree_cpu_percent"] = 10, 0
	a.Values["tree_working_set_bytes"], b.Values["tree_working_set_bytes"], c.Values["tree_working_set_bytes"] = 100, 200, 300
	_, files := performanceReportTestExport(t, PerformanceStatus{}, performanceReportTestJSONL(t, a, b, c))
	s := performanceReportTestSummary(t, files)
	cpu := performanceReportTestMetric(t, s, "tree_cpu_percent")
	if cpu.Count != 2 || cpu.Missing != 1 {
		t.Fatalf("count/missing = %d/%d", cpu.Count, cpu.Missing)
	}
	performanceReportTestNumber(t, cpu.Mean, 5)
	performanceReportTestNumber(t, cpu.Min, 0)
	rows, err := csv.NewReader(bytes.NewReader(files["samples.csv"])).ReadAll()
	if err != nil || len(rows) != 4 {
		t.Fatalf("csv rows = %d, %v", len(rows), err)
	}
	column := -1
	for i, name := range rows[0] {
		if name == "tree_cpu_percent" {
			column = i
		}
	}
	if column < 0 || rows[1][column] != "10" || rows[2][column] != "" || rows[3][column] != "0" {
		t.Fatalf("csv cpu column = %v", [][]string{rows[1], rows[2], rows[3]})
	}
	// Metrics come in the curated order, not alphabetically.
	if s.Metrics[0].Key != "system_cpu_percent" {
		t.Fatalf("first metric = %s", s.Metrics[0].Key)
	}
}

// The report's first screen must say who used the machine and keep the user's
// own stutter marks visible however many automatic events there are.
func TestPerformanceReportVerdictTopProcessesAndUserMarks(t *testing.T) {
	var samples []PerformanceSample
	for i := 1; i <= 1200; i++ {
		s := performanceReportTestSample(float64(i))
		s.Values["system_cpu_percent"], s.Values["tree_cpu_percent"] = 92, 3
		s.Values["system_memory_used_percent"] = 95
		s.Values["tree_working_set_bytes"] = 200 << 20
		s.Application.SlowFrames, s.Application.WindowSlow = uint64(i*3), 3
		s.Application.LoopMaxMS = performanceReportTestFloat(20)
		if i == 700 {
			s.Application.LoopMaxMS = performanceReportTestFloat(640) // One long hitch.
			s.Application.CaptureMaxMS = performanceReportTestFloat(600)
		}
		s.Markers = []string{"自动事件"}
		if i == 600 {
			s.Markers = append(s.Markers, "599.500s: "+PerformanceUserMark+"：卡顿")
		}
		s.Top = []PerformanceTopProcess{
			{PID: 42, Name: "timer-app.exe", Role: "self", CPUPercent: performanceReportTestFloat(3), WorkingSetBytes: 200 << 20},
			{PID: 500, Name: "MuMuVMMHeadless.exe", Role: "emulator", CPUPercent: performanceReportTestFloat(60), WorkingSetBytes: 6 << 30, GPUPercent: performanceReportTestFloat(70)},
			{PID: 501, Name: "MuMuVMMHeadless.exe", Role: "emulator", CPUPercent: performanceReportTestFloat(10), WorkingSetBytes: 1 << 30},
			{PID: 900, Name: "chrome.exe", CPUPercent: performanceReportTestFloat(8), WorkingSetBytes: 2 << 30},
		}
		if i%2 == 0 { // Listed only half the time: counts as 0 otherwise.
			s.Top = append(s.Top, PerformanceTopProcess{PID: 77, Name: "obs64.exe", CPUPercent: performanceReportTestFloat(20), WorkingSetBytes: 1 << 29})
		}
		samples = append(samples, s)
	}
	_, files := performanceReportTestExport(t, PerformanceStatus{}, performanceReportTestJSONL(t, samples...))
	s := performanceReportTestSummary(t, files)
	if len(s.Top) != 4 || s.Top[0].Name != "MuMuVMMHeadless.exe" || s.Top[0].Role != "emulator" || s.Top[0].Instances != 2 {
		t.Fatalf("top = %+v", s.Top[0])
	}
	performanceReportTestNumber(t, s.Top[0].CPUMean, 70)
	performanceReportTestNumber(t, s.Top[0].CPUPeak, 70)
	if s.Top[0].MemoryPeak != 7<<30 || s.Top[1].Name != "obs64.exe" {
		t.Fatalf("top order/memory = %+v / %+v", s.Top[0], s.Top[1])
	}
	performanceReportTestNumber(t, s.Top[1].CPUMean, 10) // 20% in half of the samples.
	verdict := strings.Join(s.Verdict, "\n")
	for _, want := range []string{"MuMuVMMHeadless.exe", "瓶颈不在计时器", "整机内存紧张", "640 ms", "截图最长 600 ms"} {
		if !strings.Contains(verdict, want) {
			t.Errorf("verdict lacks %q:\n%s", want, verdict)
		}
	}
	if len(s.UserMarks) != 1 || s.UserMarks[0].ElapsedS != 599.5 || !s.UserMarks[0].At.Equal(performanceReportTestTime.Add(599500*time.Millisecond)) {
		t.Fatalf("user marks = %+v", s.UserMarks)
	}
	if s.SlowFrames != 3600 {
		t.Fatalf("slow frames = %d", s.SlowFrames)
	}
	page, text := string(files["report.html"]), string(files["report.md"])
	for name, body := range map[string]string{"html": page, "md": text} {
		for _, want := range []string{"瓶颈不在计时器", PerformanceUserMark + "：卡顿", "MuMuVMMHeadless.exe", "obs64.exe"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
	if strings.Contains(text, "&#") || strings.Contains(text, "e+0") || !strings.Contains(text, "`tree_cpu_percent`") {
		t.Error("report.md is not plain readable text")
	}
	if len(text) > 16<<10 {
		t.Errorf("report.md is %d bytes; it should stay a short summary", len(text))
	}
	if !strings.Contains(page, `"marks":[{"elapsed_s":599.5`) {
		t.Error("chart payload lacks the user mark")
	}
}

func TestPerformanceReportQuietSessionSaysSo(t *testing.T) {
	var samples []PerformanceSample
	for i := 1; i <= 120; i++ {
		s := performanceReportTestSample(float64(i))
		s.Values["system_cpu_percent"], s.Values["tree_cpu_percent"] = 12, 2
		samples = append(samples, s)
	}
	_, files := performanceReportTestExport(t, PerformanceStatus{}, performanceReportTestJSONL(t, samples...))
	s := performanceReportTestSummary(t, files)
	if len(s.Verdict) != 1 || !strings.Contains(s.Verdict[0], "没有发现明显的资源瓶颈") {
		t.Fatalf("verdict = %v", s.Verdict)
	}
	// Three samples: say that the session is too short, and still conclude.
	_, files = performanceReportTestExport(t, PerformanceStatus{}, performanceReportTestJSONL(t, samples[:3]...))
	if s = performanceReportTestSummary(t, files); len(s.Verdict) != 2 || !strings.Contains(s.Verdict[0], "时间太短") {
		t.Fatalf("short verdict = %v", s.Verdict)
	}
}

// A slope fitted to a handful of warm-up samples is noise. Trends exist only
// for level metrics observed for at least five minutes.
func TestPerformanceReportTrendOnlyForLongLevels(t *testing.T) {
	build := func(n int) performanceReportSummary {
		var samples []PerformanceSample
		for i := 1; i <= n; i++ {
			s := performanceReportTestSample(float64(i))
			s.Values["tree_working_set_bytes"] = float64(100<<20 + i*(1<<20))
			s.Values["system_cpu_percent"] = float64(i % 50)
			s.Values["go_alloc_total_bytes"] = float64(i * 1000)
			samples = append(samples, s)
		}
		_, files := performanceReportTestExport(t, PerformanceStatus{}, performanceReportTestJSONL(t, samples...))
		return performanceReportTestSummary(t, files)
	}
	short := build(30)
	if m := performanceReportTestMetric(t, short, "tree_working_set_bytes"); m.TrendPerMinute != nil || m.Change == nil {
		t.Fatalf("short session trend = %v change = %v", m.TrendPerMinute, m.Change)
	}
	long := build(400)
	performanceReportTestNumber(t, performanceReportTestMetric(t, long, "tree_working_set_bytes").TrendPerMinute, 60*(1<<20))
	for _, key := range []string{"system_cpu_percent", "go_alloc_total_bytes"} {
		if m := performanceReportTestMetric(t, long, key); m.TrendPerMinute != nil || m.Change != nil {
			t.Errorf("%s must not get a trend/change", key)
		}
	}
	verdict := strings.Join(long.Verdict, "\n")
	if !strings.Contains(verdict, "计时器内存从") {
		t.Errorf("memory growth not reported:\n%s", verdict)
	}
}

// One damaged record (the torn last line of a crashed session, a field from a
// newer build, an odd process name) must never cost the user the report.
func TestPerformanceReportSurvivesDamagedAndUnknownInput(t *testing.T) {
	a, b := performanceReportTestSample(1), performanceReportTestSample(2)
	a.Values["tree_cpu_percent"] = 4
	a.Processes = []PerformanceProcess{{PID: 42, Name: `C:\odd\name.exe`, StartID: "1", Values: map[string]float64{"cpu_percent": 4}}}
	data := performanceReportTestJSONL(t, a)
	data = append(data, []byte(`{"at":"2026-01-02T03:04:06.5Z","elapsed_s":1.5,"interval_s":0.5,"values":{"tree_cpu_percent":null,"future_metric":7},"future_field":{"x":1}}`+"\n")...)
	data = append(data, []byte("not json at all\n")...)
	data = append(data, performanceReportTestJSONL(t, b)...)
	data = append(data, []byte(`{"at":"2026-01-02T03:04:08Z","elapsed_s":3,"val`)...) // Torn tail.
	_, files := performanceReportTestExport(t, PerformanceStatus{StopReason: "not_finalized"}, data)
	s := performanceReportTestSummary(t, files)
	if s.Samples != 3 || s.SkippedLines != 2 {
		t.Fatalf("samples/skipped = %d/%d", s.Samples, s.SkippedLines)
	}
	if m := performanceReportTestMetric(t, s, "future_metric"); m.Count != 1 {
		t.Fatalf("unknown metric = %+v", m)
	}
	if m := performanceReportTestMetric(t, s, "tree_cpu_percent"); m.Count != 1 {
		t.Fatalf("null was counted as a value: %+v", m)
	}
	if !strings.Contains(string(files["report.html"]), "未正常结束") || !bytes.Equal(files["samples.jsonl"], data) {
		t.Fatal("stop reason missing or raw timeline altered")
	}
}

func TestPerformanceReportEscapesHostileText(t *testing.T) {
	s := performanceReportTestSample(1)
	s.Markers = []string{PerformanceUserMark + `：<img src=x onerror=alert(1)> | pipe`}
	s.Top = []PerformanceTopProcess{{PID: 9, Name: `</script><script>alert(2)</script>`, CPUPercent: performanceReportTestFloat(1)}}
	s.Application.LastError = `<b>boom</b>`
	s.Application.Errors = 1
	_, files := performanceReportTestExport(t, PerformanceStatus{LastError: "<i>recorder</i>\nstack line"}, performanceReportTestJSONL(t, s))
	page := string(files["report.html"])
	for _, raw := range []string{"<img src=x", "<script>alert(2)", "<b>boom</b>", "<i>recorder</i>"} {
		if strings.Contains(page, raw) {
			t.Errorf("report.html contains unescaped %q", raw)
		}
	}
	if !strings.Contains(page, "&lt;i&gt;recorder&lt;/i&gt;") {
		t.Error("recorder error text missing from report.html")
	}
	summary := performanceReportTestSummary(t, files)
	if summary.LastError != "<i>recorder</i>\nstack line" || summary.LastCaptureError != "<b>boom</b>" {
		t.Fatalf("error text not exported verbatim: %q / %q", summary.LastError, summary.LastCaptureError)
	}
}

func performanceReportTestChart(t *testing.T, files map[string][]byte, key string) [][2]*float64 {
	t.Helper()
	page := string(files["report.html"])
	const open = `<script id="performance-chart-data" type="application/json">`
	start := strings.Index(page, open)
	if start < 0 {
		t.Fatal("chart payload missing")
	}
	page = page[start+len(open):]
	var payload struct {
		Charts []struct {
			Key    string
			Points [][2]*float64
		}
	}
	if err := json.Unmarshal([]byte(page[:strings.Index(page, "</script>")]), &payload); err != nil {
		t.Fatal(err)
	}
	for _, c := range payload.Charts {
		if c.Key == key {
			return c.Points
		}
	}
	return nil
}

// Lines break exactly at a discontinuity. The rate metrics' missing first
// sample breaks nothing, peaks survive compaction, and empty series vanish.
func TestPerformanceReportChartBreaksAndPeaks(t *testing.T) {
	var samples []PerformanceSample
	for i := 0; i < 1000; i++ {
		second := float64(i + 1)
		if i >= 500 {
			second += 60 // One sampling gap.
		}
		s := performanceReportTestSample(second)
		if i > 0 { // Rates have no value in the baseline sample.
			s.Values["system_cpu_percent"] = 10
		}
		if i == 333 {
			s.Values["system_cpu_percent"] = 97
		}
		samples = append(samples, s)
	}
	_, files := performanceReportTestExport(t, PerformanceStatus{}, performanceReportTestJSONL(t, samples...))
	points := performanceReportTestChart(t, files, "system_cpu_percent")
	breaks, peak := 0, 0.0
	for i, p := range points {
		if p[0] == nil {
			breaks++
			if i == 0 {
				t.Fatal("chart starts with a break")
			}
			if next := points[i+1]; *next[0] < 500 || *next[0] > 600 {
				t.Fatalf("break is not at the gap: next point at %v", *next[0])
			}
			continue
		}
		peak = math.Max(peak, *p[1])
	}
	if breaks != 1 || peak != 97 || len(points) > 4*performanceReportChartBuckets+8 {
		t.Fatalf("breaks=%d peak=%v points=%d", breaks, peak, len(points))
	}
	if performanceReportTestChart(t, files, "tree_handles") != nil {
		t.Fatal("a series without data was sent to the chart")
	}
	if s := performanceReportTestSummary(t, files); len(s.Gaps) != 1 || s.Gaps[0].FromS != 500 || s.Gaps[0].ToS != 561 {
		t.Fatalf("gaps = %+v", s.Gaps)
	}
}

func TestPerformanceReportAttachments(t *testing.T) {
	dir := t.TempDir()
	timeline := filepath.Join(dir, "samples.jsonl")
	data := performanceReportTestJSONL(t, performanceReportTestSample(1))
	performanceReportTestWrite(t, timeline, data)
	performanceReportTestWrite(t, filepath.Join(dir, "heap-end.pprof"), []byte("heap"))
	performanceReportTestWrite(t, filepath.Join(dir, "cpu-mark-1.pprof"), []byte("cpu"))
	performanceReportTestWrite(t, filepath.Join(dir, "config.json"), []byte(`{"ui":{}}`))
	performanceReportTestWrite(t, filepath.Join(dir, "unrelated.txt"), []byte("no"))
	status := PerformanceStatus{Directory: dir, ProfileStatus: map[string]string{
		"heap-end.pprof": "ok", "cpu-mark-1.pprof": "ok", "heap-start.pprof": "disk full", "goroutines-end.txt": "ok", "../escape": "ok"}}
	path, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), status, timeline, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := performanceReportTestZIP(t, path)
	for name, want := range map[string]string{"heap-end.pprof": "heap", "cpu-mark-1.pprof": "cpu", "config.json": `{"ui":{}}`} {
		if string(files[name]) != want {
			t.Errorf("%s = %q", name, files[name])
		}
	}
	for _, name := range []string{"heap-start.pprof", "goroutines-end.txt", "unrelated.txt", "escape"} {
		if _, ok := files[name]; ok {
			t.Errorf("%s must not be in the ZIP", name)
		}
	}
	for _, name := range []string{"report.html", "report.md", "summary.json", "metadata.json", "status.json", "samples.csv", "samples.jsonl"} {
		if len(files[name]) == 0 {
			t.Errorf("missing %s", name)
		}
	}
	results := make(map[string]string)
	for _, p := range performanceReportTestSummary(t, files).Profiles {
		results[p.Name] = p.Result
	}
	if results["heap-end.pprof"] != "已包含" || !strings.Contains(results["heap-start.pprof"], "disk full") || !strings.Contains(results["goroutines-end.txt"], "未包含") {
		t.Fatalf("profile results = %v", results)
	}
}

type performanceReportCancelContext struct {
	context.Context
	calls, after int
}

func (c *performanceReportCancelContext) Err() error {
	c.calls++
	if c.calls > c.after {
		return context.Canceled
	}
	return nil
}

// A canceled or failed export leaves the previous ZIP and no temporary files;
// an empty session and a mid-recording prefix still export.
func TestPerformanceReportCancelEmptyAndPrefix(t *testing.T) {
	dir := t.TempDir()
	timeline := filepath.Join(dir, "samples.jsonl")
	a, b := performanceReportTestSample(1), performanceReportTestSample(2)
	first := performanceReportTestJSONL(t, a)
	data := append(append([]byte(nil), first...), performanceReportTestJSONL(t, b)...)
	performanceReportTestWrite(t, timeline, append(data, []byte(`{"at":"torn`)...)) // Live tail being written.
	status := PerformanceStatus{Directory: dir, Active: true}
	path, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), status, timeline, int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	files := performanceReportTestZIP(t, path)
	if s := performanceReportTestSummary(t, files); s.Samples != 1 || s.SkippedLines != 0 || !bytes.Equal(files["samples.jsonl"], first) {
		t.Fatalf("committed prefix not respected: %d samples", s.Samples)
	}
	if !strings.Contains(string(files["report.html"]), "仍在记录") {
		t.Error("mid-recording export is not labelled")
	}
	before, _ := os.ReadFile(path)
	for after := 0; after < 40; after += 3 {
		ctx := &performanceReportCancelContext{Context: context.Background(), after: after}
		if _, err := exportPerformanceReport(ctx, performanceReportTestMetadata(), status, timeline, int64(len(data))); err == nil {
			break // Enough budget to finish.
		}
		now, _ := os.ReadFile(path)
		if !bytes.Equal(before, now) {
			t.Fatalf("canceled export (after %d checks) replaced the previous ZIP", after)
		}
		performanceReportTestNoTemps(t, dir)
	}
	if _, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), status, timeline, int64(len(data))+1000); err == nil {
		t.Fatal("a prefix longer than the file must fail")
	}
	performanceReportTestNoTemps(t, dir)
	_, files = performanceReportTestExport(t, PerformanceStatus{}, nil)
	if s := performanceReportTestSummary(t, files); s.Samples != 0 || !s.Insufficient || len(s.Verdict) == 0 {
		t.Fatalf("empty summary = %+v", s)
	}
	if !strings.Contains(string(files["report.html"]), "性能报告") {
		t.Fatal("empty report did not render")
	}
}

func TestPerformanceReportDisplayFormatting(t *testing.T) {
	for _, c := range []struct {
		value float64
		unit  string
		want  string
	}{{12.345, "%", "12.3%"}, {0, "%", "0.0%"}, {1536, "bytes", "1.5 KB"}, {3 << 30, "bytes", "3.00 GB"}, {512, "bytes", "512 B"},
		{2 << 20, "bytes/s", "2.0 MB/秒"}, {16.04, "ms", "16.0 ms"}, {11175510, "count", "11175510"}, {0.6666667, "count", "0.67"}, {1.5, "cores", "1.50 核"}} {
		if got := performanceReportHumanValue(&c.value, c.unit); got != c.want {
			t.Errorf("%v %s = %q, want %q", c.value, c.unit, got, c.want)
		}
	}
	if performanceReportHumanValue(nil, "%") != "—" || performanceReportNumber(nil) != "—" {
		t.Error("missing values must render as a dash")
	}
	for seconds, want := range map[float64]string{59: "0:59", 61: "1:01", 3725: "1:02:05"} {
		if got := performanceReportClock(seconds); got != want {
			t.Errorf("clock(%v) = %q", seconds, got)
		}
	}
	if performanceReportDuration(3725) != "1 小时 02 分 05 秒" || performanceReportDuration(65) != "1 分 05 秒" {
		t.Error("duration wording")
	}
}
