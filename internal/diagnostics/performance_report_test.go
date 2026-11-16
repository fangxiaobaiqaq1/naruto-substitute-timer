package diagnostics

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestPerformanceReportNullZeroCPUTrendAndCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "samples.jsonl")
	var timeline bytes.Buffer
	for i := 0; i < 4; i++ {
		s := performanceReportTestSample(float64(i * 60))
		s.IntervalS = 60
		s.Values["main_cpu_percent"] = float64(max(0, i-1) * 25)
		s.Values["tree_cpu_percent"] = float64(max(0, i-1) * 25)
		s.Values["go_heap_alloc_bytes"] = float64(100 + i*10)
		if i == 1 || i == 2 {
			s.Application.UIQueueMS = performanceReportFloat(float64((i - 1) * 10))
		}
		s.Application.CaptureMS = performanceReportFloat(0)
		s.Application.AnalysisMS = performanceReportFloat(float64(i))
		s.Application.DeliveryAgeMS = performanceReportFloat(float64(i + 1))
		s.Application.CaptureP95MS = performanceReportFloat(5)
		s.Application.AnalysisP95MS = performanceReportFloat(6)
		// Inject explicit null, which the recorder's map[string]float64 cannot
		// encode, to verify the exporter's wire decoder doesn't turn it into 0.
		data := performanceReportTestJSONL(t, s)
		var object map[string]any
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatal(err)
		}
		v := object["values"].(map[string]any)
		v["future_null"] = nil
		v["future_zero"] = 0
		if i == 0 {
			v["main_cpu_percent"], v["tree_cpu_percent"] = nil, nil
			object["collector_ms"] = nil
		}
		if err := json.NewEncoder(&timeline).Encode(object); err != nil {
			t.Fatal(err)
		}
	}
	performanceReportTestWrite(t, path, timeline.Bytes())
	meta := performanceReportTestMetadata()
	meta.IntervalMS = 60000
	out, err := exportPerformanceReport(context.Background(), meta, PerformanceStatus{Directory: dir, Samples: 999, Bytes: 999}, path, int64(timeline.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := performanceReportTestZIP(t, out)
	s := performanceReportTestSummary(t, files)
	if s.Samples != 4 || s.TimelineBytes != int64(timeline.Len()) || s.Insufficient || len(s.Gaps) != 0 {
		t.Fatalf("incorrect snapshot summary: %+v", s)
	}
	cpu := performanceReportTestMetric(t, s, "main_cpu_percent")
	if cpu.Count != 3 || cpu.Missing != 1 || len(cpu.Minutes) != 3 || cpu.Minutes[0].Minute != 1 {
		t.Fatalf("incorrect valid/missing/minute counts: %+v", cpu)
	}
	for _, pair := range []struct {
		got  *float64
		want float64
	}{
		{cpu.First, 0}, {cpu.Last, 50}, {cpu.Peak, 50}, {cpu.Mean, 25}, {cpu.P50, 25}, {cpu.P95, 50}, {cpu.P99, 50}, {cpu.TrendPerMinute, 25},
	} {
		performanceReportTestNumber(t, pair.got, pair.want)
	}
	cores := performanceReportTestMetric(t, s, "main_cpu_cores")
	performanceReportTestNumber(t, cores.Last, 4)
	performanceReportTestNumber(t, cores.TrendPerMinute, 2)
	performanceReportTestNumber(t, performanceReportTestMetric(t, s, "tree_cpu_cores").Last, 4)
	heap := performanceReportTestMetric(t, s, "go_heap_alloc_bytes")
	performanceReportTestNumber(t, heap.Change, 30)
	performanceReportTestNumber(t, heap.TrendPerMinute, 10)
	null := performanceReportTestMetric(t, s, "future_null")
	if null.Count != 0 || null.Missing != 4 || null.Mean != nil || null.First != nil || null.P95 != nil || null.TrendPerMinute != nil {
		t.Fatalf("null became zero: %+v", null)
	}
	zero := performanceReportTestMetric(t, s, "future_zero")
	performanceReportTestNumber(t, zero.P99, 0)
	ui := performanceReportTestMetric(t, s, "ui_queue_ms")
	if ui.Count != 2 || ui.Missing != 2 {
		t.Fatalf("application pointer timings lost: %+v", ui)
	}
	performanceReportTestNumber(t, ui.First, 0)
	performanceReportTestNumber(t, ui.Mean, 5)
	collector := performanceReportTestMetric(t, s, "collector_ms")
	if collector.Count != 3 || collector.Missing != 1 {
		t.Fatalf("collector null lost: %+v", collector)
	}
	performanceReportTestNumber(t, collector.Peak, 0)
	for _, key := range []string{"capture_ms", "analysis_ms", "delivery_age_ms", "capture_p95_ms", "analysis_p95_ms"} {
		if performanceReportTestMetric(t, s, key).Count != 4 {
			t.Fatalf("missing application field %s", key)
		}
	}
	rows, err := csv.NewReader(bytes.NewReader(files["samples.csv"])).ReadAll()
	if err != nil || len(rows) != 5 {
		t.Fatalf("CSV: rows=%d error=%v", len(rows), err)
	}
	columns := make(map[string]int)
	for i, key := range rows[0] {
		columns[key] = i
	}
	if rows[1][columns["main_cpu_percent"]] != "" || rows[2][columns["main_cpu_percent"]] != "0" || rows[1][columns["future_null"]] != "" || rows[1][columns["future_zero"]] != "0" {
		t.Fatal("CSV confused zero and missing")
	}
	if !bytes.Equal(files["samples.jsonl"], timeline.Bytes()) {
		t.Fatal("raw JSONL was modified")
	}
	if !strings.Contains(string(files["report.html"]), "不能判定泄漏") || !strings.Contains(string(files["report.md"]), "coreEquivalent") {
		t.Fatal("metric limitations are not visible")
	}
	performanceReportTestNoTemps(t, dir)
}

func TestPerformanceReportHTMLInjectionUTF8AndAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "samples.jsonl")
	attack := "</script><script>globalThis.pwned=1</script><img src=x onerror=alert(1)>中文🦊\u2028"
	meta := performanceReportTestMetadata()
	meta.Version = attack
	meta.ProfilesRequested = true
	meta.Notes = []string{attack}
	meta.System.Unavailable = map[string]string{"gpu": attack}
	sample := performanceReportTestSample(0)
	sample.Markers = []string{attack}
	sample.Values["=1+1"] = 0
	sample.Values["comma,quote\"换行\nmetric"] = 2
	sample.Processes = []PerformanceProcess{{PID: 42, Name: "<img src=x onerror=alert(2)>程序.exe", StartID: attack, Values: map[string]float64{"cpu_percent": 0}}}
	raw := performanceReportTestJSONL(t, sample)
	performanceReportTestWrite(t, path, raw)
	performanceReportTestWrite(t, filepath.Join(dir, "heap-start.pprof"), []byte("synthetic heap"))
	performanceReportTestWrite(t, filepath.Join(dir, "goroutines-start.txt"), []byte("合成调用栈 UTF-8"))
	for _, name := range []string{"config.json", "status.json", "report.json", "screenshot.png", "command-line.txt", "heap-other.pprof"} {
		performanceReportTestWrite(t, filepath.Join(dir, name), []byte("PRIVATE_OLD_DIAGNOSTICS"))
	}
	// Symlinks must not allow the fixed filename allowlist to leak unrelated files.
	linked := os.Symlink(filepath.Join(dir, "config.json"), filepath.Join(dir, "goroutines-end.txt")) == nil
	status := PerformanceStatus{Directory: dir, Active: true, ProfileStatus: map[string]string{"heap-start.pprof": "ok", "heap-end.pprof": "未启用结束快照", "goroutines-start.txt": "ok", "goroutines-end.txt": "ok"},
		LastError: "open /home/SECRET_USERNAME/private/config.json", Latest: &PerformanceSample{Markers: []string{"SECRET_ACTIVE_LATEST"}}}
	out, err := exportPerformanceReport(context.Background(), meta, status, path, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := performanceReportTestZIP(t, out)
	var names []string
	for name, data := range files {
		names = append(names, name)
		if bytes.Contains(data, []byte("PRIVATE_OLD_DIAGNOSTICS")) || bytes.Contains(data, []byte("SECRET_USERNAME")) || bytes.Contains(data, []byte("SECRET_ACTIVE_LATEST")) || bytes.Contains(data, []byte(dir)) {
			t.Fatalf("privacy leak in %s", name)
		}
		if strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".csv") {
			if !utf8.Valid(data) {
				t.Fatalf("invalid UTF-8 in %s", name)
			}
		}
	}
	sort.Strings(names)
	want := []string{"goroutines-start.txt", "heap-start.pprof", "metadata.json", "report.html", "report.md", "samples.csv", "samples.jsonl", "summary.json"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("ZIP allowlist: %v", names)
	}
	h := string(files["report.html"])
	if strings.Contains(h, "<script>globalThis.pwned") || strings.Contains(h, "<img src=x") || !strings.Contains(h, "&lt;img") || !strings.Contains(h, "中文🦊") {
		t.Fatal("unsafe HTML escaping or damaged UTF-8")
	}
	if strings.Contains(h, "https://") || strings.Contains(h, "http://") || strings.Contains(h, "fetch(") || strings.Contains(h, "innerHTML") || strings.Contains(h, "<script src=") {
		t.Fatal("HTML depends on network or unsafe DOM interpolation")
	}
	start := strings.Index(h, `<script id="performance-chart-data" type="application/json">`)
	if start < 0 {
		t.Fatal("missing inline chart JSON")
	}
	start += len(`<script id="performance-chart-data" type="application/json">`)
	end := strings.Index(h[start:], "</script>")
	if end < 0 || !json.Valid([]byte(h[start:start+end])) {
		t.Fatal("chart payload is not standalone JSON")
	}
	var roundtrip PerformanceMetadata
	if err := json.Unmarshal(files["metadata.json"], &roundtrip); err != nil || roundtrip.Version != attack {
		t.Fatalf("metadata round trip: %v", err)
	}
	rows, err := csv.NewReader(bytes.NewReader(files["samples.csv"])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(rows[0], "|"), "'=1+1") || !strings.Contains(strings.Join(rows[0], "|"), "comma,quote\"换行\nmetric") {
		t.Fatal("CSV formula neutralization or standard quoting failed")
	}
	summary := performanceReportTestSummary(t, files)
	if !summary.RecorderError || len(summary.Profiles) != 4 || !summary.Profiles[0].Included || summary.Profiles[1].Included {
		t.Fatalf("profile availability not reported: %+v", summary.Profiles)
	}
	if linked && (summary.Profiles[3].Included || !strings.Contains(summary.Profiles[3].Result, "符号链接")) {
		t.Fatal("unsafe profile link was not reported")
	}
}

func TestPerformanceReportActivePrefixAndAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "samples.jsonl")
	prefix := performanceReportTestJSONL(t, performanceReportTestSample(0), performanceReportTestSample(1))
	next := performanceReportTestJSONL(t, performanceReportTestSample(2))
	live := append(append(append([]byte{}, prefix...), next...), []byte(`{"at":"unfinished ACTIVE_TAIL`)...)
	live = append(live, 0xff)
	performanceReportTestWrite(t, path, live)
	status := PerformanceStatus{Directory: dir, Active: true}
	out, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), status, path, int64(len(prefix)))
	if err != nil {
		t.Fatal(err)
	}
	if out != filepath.Join(dir, "performance-report.zip") {
		t.Fatal("non-fixed export filename")
	}
	first := performanceReportTestZIP(t, out)
	if !bytes.Equal(first["samples.jsonl"], prefix) || performanceReportTestSummary(t, first).Samples != 2 {
		t.Fatal("active complete/partial tail was included")
	}
	out2, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), status, path, int64(len(prefix)+len(next)))
	if err != nil || out != out2 {
		t.Fatalf("repeat export: %s %v", out2, err)
	}
	second := performanceReportTestZIP(t, out2)
	if performanceReportTestSummary(t, second).Samples != 3 || !bytes.Equal(second["samples.jsonl"], live[:len(prefix)+len(next)]) {
		t.Fatal("repeat export did not replace snapshot")
	}
	rows, err := csv.NewReader(bytes.NewReader(second["samples.csv"])).ReadAll()
	if err != nil || len(rows) != 4 {
		t.Fatalf("CSV read the live file, not the snapshot: %v", err)
	}
	performanceReportTestNoTemps(t, dir)
}

func TestPerformanceReportGapsBaselineResetAndStalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "samples.jsonl")
	var samples []PerformanceSample
	for i, second := range []float64{0, 1, 4} {
		s := performanceReportTestSample(second)
		s.Values["go_gc_count"] = float64(3 - i)
		s.Processes = []PerformanceProcess{{PID: 42, Name: "程序.exe", StartID: "a", Values: map[string]float64{"cpu_seconds": float64(3 - i), "working_set_bytes": float64(100 + i)}}}
		s.Application.SlowFrames = uint64(i)
		s.Unavailable = map[string]string{"tree_gpu_busiest_engine_percent": "驱动不可用"}
		if i == 2 {
			s.Processes[0].StartID = "b"
			s.At = performanceReportTestTime // Wall clock may move backwards.
		}
		samples = append(samples, s)
	}
	raw := performanceReportTestJSONL(t, samples...)
	performanceReportTestWrite(t, path, raw)
	out, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), PerformanceStatus{}, path, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := performanceReportTestZIP(t, out)
	s := performanceReportTestSummary(t, files)
	if len(s.Gaps) != 1 || s.Gaps[0].FromS != 1 || s.Gaps[0].ToS != 4 || s.Gaps[0].EstimatedMissing != 2 {
		t.Fatalf("gap missing: %+v", s.Gaps)
	}
	if len(s.Processes) != 2 || s.Processes[0].Samples != 2 || s.Processes[1].Samples != 1 {
		t.Fatalf("PID reuse merged identities: %+v", s.Processes)
	}
	if len(s.Unavailable) != 1 || s.Unavailable[0].Count != 3 {
		t.Fatalf("unavailability missing: %+v", s.Unavailable)
	}
	h := string(files["report.html"])
	for _, text := range []string{"baseline", "counter reset", "cpu_seconds", "wall_clock_reset", "卡顿", "驱动不可用"} {
		if !strings.Contains(h, text) {
			t.Fatalf("missing visible %q", text)
		}
	}
	if performanceReportTestMetric(t, s, "tree_gpu_busiest_engine_percent").Mean != nil {
		t.Fatal("unavailable GPU replaced with zero")
	}
}

func TestPerformanceReportReadableTimesUnitsBuildAndCounterWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "samples.jsonl")
	meta := performanceReportTestMetadata()
	meta.StartedAt = time.Now() // Retain the monotonic part that Time.String exposes.
	meta.System.TotalRAMBytes = 16 << 30
	meta.ProfilesRequested = true
	meta.Build = map[string]string{"vcs.revision": "deadbeef<script>BUILD_ATTACK</script>", "vcs.modified": "true", "vcs.time": "2026-01-02T03:04:05Z", "GOARCH": "amd64", "-ldflags": "PRIVATE_BUILD_ARGUMENTS"}
	first, last := performanceReportTestSample(5), performanceReportTestSample(35)
	first.At, last.At = meta.StartedAt.Add(5*time.Second), meta.StartedAt.Add(35*time.Second)
	last.IntervalS = 30
	first.Values["go_heap_alloc_bytes"], last.Values["go_heap_alloc_bytes"] = 1<<20, 1.5*(1<<20)
	last.Values["main_cpu_percent"], last.Values["main_counter_interval_seconds"] = 25, 2
	first.Processes = []PerformanceProcess{{PID: 42, Name: "程序.exe", StartID: "one", CounterAt: meta.StartedAt.Add(time.Second), Values: map[string]float64{"cpu_seconds": 1, "working_set_bytes": 2048}}}
	last.Processes = []PerformanceProcess{{PID: 42, Name: "程序.exe", StartID: "one", CounterAt: meta.StartedAt.Add(3 * time.Second), Values: map[string]float64{
		"cpu_seconds": 5, "cpu_percent": 25, "counter_interval_seconds": 2, "counter_age_ms": 32000,
		"working_set_bytes": 4 << 20, "private_bytes": 8 << 20, "gpu_dedicated_bytes": 1 << 30, "io_read_bytes_s": 2048,
	}}}
	raw := performanceReportTestJSONL(t, first, last)
	performanceReportTestWrite(t, path, raw)
	ended := meta.StartedAt.Add(40 * time.Second)
	out, err := exportPerformanceReport(context.Background(), meta, PerformanceStatus{Directory: dir, EndedAt: &ended}, path, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := performanceReportTestZIP(t, out)
	summary := performanceReportTestSummary(t, files)
	if summary.Insufficient || summary.Samples != 2 {
		t.Fatal("short-session warning changed the two-sample statistical minimum")
	}
	heap := performanceReportTestMetric(t, summary, "go_heap_alloc_bytes")
	performanceReportTestNumber(t, heap.Mean, 1.25*(1<<20))
	performanceReportTestNumber(t, heap.TrendPerMinute, 1<<20)
	performanceReportTestNumber(t, performanceReportTestMetric(t, summary, "main_cpu_percent").Mean, 25)
	performanceReportTestNumber(t, performanceReportTestMetric(t, summary, "main_cpu_cores").Mean, 2)
	if !bytes.Equal(files["samples.jsonl"], raw) {
		t.Fatal("raw counter_at or measurements changed")
	}
	var decoded PerformanceMetadata
	if err := json.Unmarshal(files["metadata.json"], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Build["vcs.revision"] != meta.Build["vcs.revision"] || decoded.Build["vcs.modified"] != "true" || !decoded.ProfilesRequested || len(decoded.Build) != 4 {
		t.Fatalf("build allowlist/profile metadata lost: %+v", decoded)
	}
	if meta.Build["-ldflags"] != "PRIVATE_BUILD_ARGUMENTS" {
		t.Fatal("export mutated caller-owned build metadata")
	}
	rows, err := csv.NewReader(bytes.NewReader(files["samples.csv"])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for column, key := range rows[0] {
		switch key {
		case "go_heap_alloc_bytes":
			for i, want := range []float64{1048576, 1572864} {
				got, err := strconv.ParseFloat(rows[i+1][column], 64)
				if err != nil || got != want {
					t.Fatal("display conversion altered CSV values")
				}
			}
		case "main_counter_interval_seconds":
			if rows[1][column] != "" || rows[2][column] != "2" {
				t.Fatal("sample interval replaced the real counter window")
			}
		}
	}
	for name, data := range files {
		if bytes.Contains(data, []byte("PRIVATE_BUILD_ARGUMENTS")) {
			t.Fatalf("non-allowlisted build setting exported in %s", name)
		}
	}
	h := string(files["report.html"])
	for _, text := range []string{
		"会话开始：" + performanceReportTime(meta.StartedAt), "最后观测：" + performanceReportTime(last.At),
		"会话结束：" + performanceReportTime(ended), "观测时长：30 秒", "UTC", "16.00 GiB", "1.50 MiB", "4.00 MiB", "8.00 MiB", "1.00 GiB", "2.00 KiB/s",
		"JSON/CSV 保留原始数值和单位", "vcs.revision", "vcs.modified", "deadbeef&lt;script&gt;", "profiles_requested=true",
		"counter_interval_seconds", "counter_at", "短时会话", "预热/GC",
	} {
		if !strings.Contains(h, text) && !strings.Contains(html.UnescapeString(h), text) {
			t.Errorf("missing readable HTML detail: %s", text)
		}
	}
	for _, name := range []string{"report.html", "report.md"} {
		text := string(files[name])
		if strings.Contains(text, "m=+") || strings.Contains(text, "m=-") || strings.Contains(text, "<script>BUILD_ATTACK") || !strings.Contains(text, "短时会话") {
			t.Fatalf("unsafe/monotonic time rendering or missing short-session warning in %s", name)
		}
	}
}

func TestPerformanceReportDisplayFormattingAndShortSessionBoundary(t *testing.T) {
	for _, tc := range []struct {
		value      *float64
		unit, want string
	}{
		{nil, "bytes", "不可用"}, {performanceReportFloat(0), "bytes", "0 B"},
		{performanceReportFloat(1024), "bytes", "1.00 KiB"}, {performanceReportFloat(1.5 * (1 << 20)), "bytes", "1.50 MiB"},
		{performanceReportFloat(2 << 30), "bytes", "2.00 GiB"}, {performanceReportFloat(-1 << 20), "bytes", "-1.00 MiB"},
		{performanceReportFloat(2048), "bytes/s", "2.00 KiB/s"}, {performanceReportFloat(25), "%", "25"},
	} {
		if got := performanceReportHumanValue(tc.value, tc.unit); got != tc.want {
			t.Errorf("display %v %s: %q, want %q", tc.value, tc.unit, got, tc.want)
		}
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.FixedZone("CST", 8*60*60))
	for _, input := range []any{at, &at} {
		if got := performanceReportTime(input); got != "2026-01-02 03:04:05.123 UTC+08:00" {
			t.Errorf("friendly timezone formatting: %s", got)
		}
	}
	for _, input := range []any{nil, (*time.Time)(nil), time.Time{}} {
		if performanceReportTime(input) != "不可用" {
			t.Fatal("missing timestamp fabricated")
		}
	}
	for _, tc := range []struct {
		first, last *float64
		short       bool
	}{
		{nil, nil, false}, {performanceReportFloat(10), performanceReportFloat(10), true},
		{performanceReportFloat(10), performanceReportFloat(69.999), true},
		{performanceReportFloat(10), performanceReportFloat(70), false},
		{performanceReportFloat(10), performanceReportFloat(190), false},
	} {
		s := performanceReportSummary{FirstElapsedS: tc.first, LastElapsedS: tc.last}
		if got := s.shortSessionWarning() != ""; got != tc.short {
			t.Errorf("incorrect short-session boundary: %+v", tc)
		}
	}
}

func TestPerformanceReportProfilesRequireConsentAndCompletion(t *testing.T) {
	const name = "heap-start.pprof"
	for _, tc := range []struct {
		label                        string
		requested                    bool
		status                       string
		oversized, missing, included bool
	}{
		{"not-requested", false, "ok", false, false, false},
		{"no-status", true, "", false, false, false},
		{"failed", true, "采集失败", false, false, false},
		{"pending", true, "pending", false, false, false},
		{"case-sensitive", true, "OK", false, false, false},
		{"completed", true, "ok", false, false, true},
		{"oversized", true, "ok", true, false, false},
		{"missing", true, "ok", false, true, false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			dir := t.TempDir()
			for _, file := range []string{name, "heap-end.pprof", "goroutines-start.txt", "goroutines-end.txt"} {
				performanceReportTestWrite(t, filepath.Join(dir, file), []byte("coincidentally named file"))
			}
			if tc.oversized {
				if err := os.Truncate(filepath.Join(dir, name), performanceReportProfileLimit+1); err != nil {
					t.Fatal(err)
				}
			}
			if tc.missing {
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}
			meta := performanceReportTestMetadata()
			meta.ProfilesRequested = tc.requested
			status := PerformanceStatus{Directory: dir, ProfileStatus: map[string]string{name: tc.status, "heap-end.pprof": "failed", "goroutines-start.txt": "pending"}}
			out, err := exportPerformanceReport(context.Background(), meta, status, filepath.Join(dir, "samples.jsonl"), 0)
			if err != nil {
				t.Fatal(err)
			}
			files := performanceReportTestZIP(t, out)
			_, included := files[name]
			summary := performanceReportTestSummary(t, files)
			if included != tc.included || summary.Profiles[0].Included != tc.included {
				t.Fatalf("profile bypassed consent/status/size/existence: %+v", summary.Profiles[0])
			}
			for _, other := range []string{"heap-end.pprof", "goroutines-start.txt", "goroutines-end.txt"} {
				if _, ok := files[other]; ok {
					t.Fatalf("status of one file authorized unfinished sibling %s", other)
				}
			}
			if tc.oversized && !strings.Contains(summary.Profiles[0].Result, "16 MiB") {
				t.Fatal("profile size check was lost")
			}
		})
	}
}

func TestPerformanceReportPendingMarkersAndDisabledLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "samples.jsonl")
	first, second := performanceReportTestSample(0), performanceReportTestSample(1)
	first.Values["go_memory_limit_disabled"] = 1
	first.Values["go_gc_disabled"] = 1
	first.Values["system_memory_used_percent"] = 0
	first.Unavailable = map[string]string{"go_memory_limit_bytes": "未设置Go软内存上限", "go_gogc_percent": "GC已关闭"}
	second.Values["go_memory_limit_bytes"] = 512 << 20
	second.Values["go_gogc_percent"] = 100
	second.Values["system_memory_used_percent"] = 50
	raw := performanceReportTestJSONL(t, first, second)
	performanceReportTestWrite(t, path, raw)
	pending := []string{"1.100s: 卡顿待落盘", "1.200s: 导出开始 </script><img src=x onerror=alert(1)>"}
	status := PerformanceStatus{Directory: dir, Active: true, PendingMarkers: pending}
	out, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), status, path, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := performanceReportTestZIP(t, out)
	summary := performanceReportTestSummary(t, files)
	if !reflect.DeepEqual(summary.PendingMarkers, pending) || summary.Samples != 2 || len(summary.Events) != 1 {
		t.Fatal("pending markers were lost or presented as committed events")
	}
	if !bytes.Equal(raw, files["samples.jsonl"]) || bytes.Contains(files["samples.csv"], []byte("导出开始")) {
		t.Fatal("pending markers were appended to the committed timeline/CSV")
	}
	for _, name := range []string{"report.html", "report.md"} {
		text := string(files[name])
		if !strings.Contains(text, "待落盘标记") || !strings.Contains(text, "导出开始") || strings.Contains(text, "<img src=x") || !strings.Contains(text, "不含后续序列化") {
			t.Fatalf("missing/unsafe pending marker or incorrect collector_ms semantics in %s", name)
		}
	}
	for _, key := range []string{"go_memory_limit_disabled", "go_gc_disabled"} {
		m := performanceReportTestMetric(t, summary, key)
		if m.Count != 1 || m.Missing != 1 || m.Unit != "flag" {
			t.Fatalf("disabled state confused with 0: %+v", m)
		}
		performanceReportTestNumber(t, m.Mean, 1)
	}
	limit := performanceReportTestMetric(t, summary, "go_memory_limit_bytes")
	if limit.Count != 1 || limit.Missing != 1 {
		t.Fatalf("unlimited became zero or infinity: %+v", limit)
	}
	performanceReportTestNumber(t, limit.Mean, 512<<20)
	performanceReportTestNumber(t, performanceReportTestMetric(t, summary, "go_gogc_percent").Mean, 100)
	performanceReportTestNumber(t, performanceReportTestMetric(t, summary, "system_memory_used_percent").Mean, 25)
}

func TestPerformanceReportEmptySnapshot(t *testing.T) {
	dir := t.TempDir()
	// A zero-byte committed snapshot need not open any timeline, even when a
	// nonempty or invalid active tail exists (or no file has yet been created).
	path := filepath.Join(dir, "samples.jsonl")
	out, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), PerformanceStatus{}, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	files := performanceReportTestZIP(t, out)
	s := performanceReportTestSummary(t, files)
	if s.Samples != 0 || !s.Insufficient || s.FirstAt != nil || s.LastElapsedS != nil || len(files["samples.jsonl"]) != 0 {
		t.Fatalf("empty snapshot: %+v", s)
	}
	for _, m := range s.Metrics {
		if m.Mean != nil || m.P95 != nil || m.TrendPerMinute != nil {
			t.Fatalf("fabricated empty statistic: %+v", m)
		}
	}
	if !strings.Contains(string(files["report.html"]), "样本不足") {
		t.Fatal("empty snapshot not labeled insufficient")
	}
}

type performanceReportCancelContext struct {
	context.Context
	cancel context.CancelFunc
	calls  int
	after  int
}

func (c *performanceReportCancelContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		c.cancel()
	}
	return c.Context.Err()
}

func TestPerformanceReportCancelAndInvalidInputPreservePrevious(t *testing.T) {
	good := performanceReportTestJSONL(t, performanceReportTestSample(0))
	badUTF8 := append([]byte(`{"at":"`), 0xff)
	badUTF8 = append(badUTF8, []byte(`"}`)...)
	var cancellable []byte
	for i := 0; i < 20; i++ {
		cancellable = append(cancellable, performanceReportTestJSONL(t, performanceReportTestSample(float64(i)))...)
	}
	cases := []struct {
		name        string
		data        []byte
		size        int64
		cancelAfter int
	}{
		{"cancel-before", good, int64(len(good)), 1},
		{"cancel-during", cancellable, int64(len(cancellable)), 12},
		{"malformed", []byte("{not JSON}\n"), 11, 0},
		{"partial-prefix", good, int64(len(good) / 2), 0},
		{"short-file", good, int64(len(good) + 1), 0},
		{"invalid-utf8", badUTF8, int64(len(badUTF8)), 0},
		{"null-object", []byte("null\n"), 5, 0},
		{"null-timing", []byte("{\"at\":\"2026-01-02T03:04:05Z\",\"elapsed_s\":null,\"interval_s\":1}\n"), 0, 0},
		{"extra-json", append(bytes.TrimSpace(good), []byte(" {}\n")...), 0, 0},
		{"unknown-config", []byte("{\"at\":\"2026-01-02T03:04:05Z\",\"elapsed_s\":0,\"interval_s\":1,\"config\":{}}\n"), 0, 0},
		{"duplicate-time", bytes.Repeat(good, 2), int64(len(good) * 2), 0},
		{"line-limit", []byte(strings.Repeat(" ", performanceReportLineLimit+1)), performanceReportLineLimit + 1, 0},
		{"input-limit", good, PerformanceMaxBytes + 1, 0},
		{"negative-size", good, -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "samples.jsonl")
			performanceReportTestWrite(t, path, tc.data)
			target := filepath.Join(dir, "performance-report.zip")
			previous := []byte("previous published export must survive")
			performanceReportTestWrite(t, target, previous)
			ctx := context.Background()
			if tc.cancelAfter > 0 {
				base, cancel := context.WithCancel(ctx)
				defer cancel()
				ctx = &performanceReportCancelContext{Context: base, cancel: cancel, after: tc.cancelAfter}
			}
			size := tc.size
			if size == 0 {
				size = int64(len(tc.data))
			}
			out, err := exportPerformanceReport(ctx, performanceReportTestMetadata(), PerformanceStatus{}, path, size)
			if err == nil || out != "" {
				t.Fatalf("bad input published a report: %s %v", out, err)
			}
			if tc.cancelAfter > 0 && !errors.Is(err, context.Canceled) {
				t.Fatalf("did not return cancellation: %v", err)
			}
			after, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(after, previous) {
				t.Fatalf("previous report damaged: %v", err)
			}
			performanceReportTestNoTemps(t, dir)
		})
	}
}

func TestPerformanceReportSampleLimitAndRawPrefixReader(t *testing.T) {
	line := "{\"at\":\"2026-01-02T03:04:05Z\",\"elapsed_s\":0,\"interval_s\":1}\n"
	count := 0
	err := performanceReportScan(context.Background(), strings.NewReader(strings.Repeat(line, PerformanceMaxSamples+1)), func(performanceReportSample) error { count++; return nil })
	if err == nil || count != PerformanceMaxSamples || !strings.Contains(err.Error(), "21600") {
		t.Fatalf("sample bound: count=%d err=%v", count, err)
	}
	// io.LimitedReader must not consume even one byte from a live tail.
	reader := bytes.NewReader([]byte(line + "DO NOT READ"))
	limited := &io.LimitedReader{R: reader, N: int64(len(line))}
	if err := performanceReportScan(context.Background(), limited, func(performanceReportSample) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if reader.Len() != len("DO NOT READ") {
		t.Fatal("read past the committed prefix")
	}
}

func TestPerformanceReportChartPreservesPeaksAndMissing(t *testing.T) {
	chart := performanceReportChart{Key: "main_cpu_percent"}
	width := 1
	for i := 0; i < 700; i++ {
		values := map[string]float64{"main_cpu_percent": 0}
		if i == 313 {
			values["main_cpu_percent"] = 99
		}
		if i == 511 {
			delete(values, "main_cpu_percent")
		}
		chart.add(i/width, float64(i), values, i == 650)
		if len(chart.buckets) > performanceReportChartBuckets {
			chart.compact()
			width *= 2
		}
	}
	if len(chart.buckets) > performanceReportChartBuckets {
		t.Fatal("unbounded chart storage")
	}
	chart.finish()
	peak, zero, missing := false, false, false
	for _, point := range chart.Points {
		if point[0] == nil || point[1] == nil {
			missing = true
			continue
		}
		if point[0].(float64) == 313 && point[1].(float64) == 99 {
			peak = true
		}
		if point[1].(float64) == 0 {
			zero = true
		}
		if point[0].(float64) == 511 {
			t.Fatal("missing point fabricated as zero")
		}
	}
	if !peak || !zero || !missing || len(chart.Points) > performanceReportChartBuckets*9 {
		t.Fatalf("invalid downsampling: peak=%t zero=%t missing=%t points=%d", peak, zero, missing, len(chart.Points))
	}
}

type performanceReportFailWriter struct{ short bool }

func (w performanceReportFailWriter) Write(p []byte) (int, error) {
	if w.short {
		return max(0, len(p)-1), nil
	}
	return 0, io.ErrClosedPipe
}

func TestPerformanceReportWriteAndPublishFailure(t *testing.T) {
	for _, short := range []bool{true, false} {
		w := performanceReportWriter{context.Background(), performanceReportFailWriter{short: short}}
		_, err := w.Write([]byte("data"))
		want := io.ErrClosedPipe
		if short {
			want = io.ErrShortWrite
		}
		if !errors.Is(err, want) {
			t.Fatalf("write failure lost: %v", err)
		}
		r := newPerformanceReport(performanceReportTestMetadata(), PerformanceStatus{}, 0)
		if err := r.finish(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := r.csv(context.Background(), strings.NewReader(""), w); !errors.Is(err, want) {
			t.Fatalf("buffered CSV failure lost: %v", err)
		}
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "performance-report.zip")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	performanceReportTestWrite(t, filepath.Join(target, "sentinel"), []byte("do not remove target on rename failure"))
	out, err := exportPerformanceReport(context.Background(), performanceReportTestMetadata(), PerformanceStatus{}, filepath.Join(dir, "samples.jsonl"), 0)
	if err == nil || out != "" {
		t.Fatalf("publish should fail: %s %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(target, "sentinel")); err != nil {
		t.Fatal("rename fallback removed existing target")
	}
	performanceReportTestNoTemps(t, dir)
}

func TestPerformanceReportArithmeticAndIdentityBounds(t *testing.T) {
	for _, input := range []string{
		`{"at":"2026-01-02T03:04:05Z","elapsed_s":0,"interval_s":1,"values":{"x":1e309}}`,
		`{"at":"2026-01-02T03:04:05Z","elapsed_s":0,"interval_s":1,"processes":[{"pid":42,"name":"/home/private/program"}]}`,
	} {
		if err := performanceReportScan(context.Background(), strings.NewReader(input), func(performanceReportSample) error { return nil }); err == nil {
			t.Fatalf("accepted malformed/privacy-unsafe input: %s", input)
		}
	}
	r := newPerformanceReport(performanceReportTestMetadata(), PerformanceStatus{}, 0)
	for i := 0; i <= performanceReportMetricLimit; i++ {
		err := r.ensureMetric(fmt.Sprintf("unknown_%d", i))
		if err != nil {
			if len(r.metrics) != performanceReportMetricLimit {
				t.Fatal("unexpected metric budget")
			}
			return
		}
	}
	t.Fatal("metric cardinality is unbounded")
}
