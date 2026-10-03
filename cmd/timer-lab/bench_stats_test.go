package main

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestSummarizeStats(t *testing.T) {
	var values []float64
	for i := 100; i >= 1; i-- {
		values = append(values, float64(i))
	}
	s := summarize(values)
	if s.Count != 100 || s.Min != 1 || s.Max != 100 || s.Mean != 50.5 {
		t.Fatalf("summary = %+v", s)
	}
	// Nearest-rank on the sorted copy, same rule as capture/observe reports.
	if s.P50 != 51 || s.P90 != 90 || s.P99 != 99 {
		t.Fatalf("percentiles = %v %v %v", s.P50, s.P90, s.P99)
	}
	if values[0] != 100 {
		t.Fatal("summarize must not reorder its input")
	}
	if math.Abs(s.fps()-1000/50.5) > 1e-9 {
		t.Fatalf("fps = %v", s.fps())
	}
	if empty := summarize(nil); empty.Count != 0 || empty.fps() != 0 {
		t.Fatalf("empty = %+v", empty)
	}
	if one := summarize([]float64{7}); one.Min != 7 || one.P50 != 7 || one.P99 != 7 || one.Max != 7 {
		t.Fatalf("single = %+v", one)
	}
}

func TestFinalizeStatusAndRates(t *testing.T) {
	r := methodResult{Attempts: 10, Successful: 10, Duplicates: 4, WallSeconds: 2, Capture: summarize([]float64{10, 10})}
	r.finalize()
	if r.Status != benchOK || r.PipelineFPS != 5 || r.UniqueFPS != 3 || r.CaptureFPS != 100 || r.duplicateRate() != .4 {
		t.Fatalf("ok result = %+v", r)
	}
	r = methodResult{Attempts: 10, Successful: 9, Errors: 1}
	r.finalize()
	if r.Status != benchPartial || r.errorRate() != .1 {
		t.Fatalf("partial = %+v", r)
	}
	r = methodResult{Error: "打开失败"}
	r.finalize()
	if r.Status != benchFailed || r.errorRate() != 0 || r.duplicateRate() != 0 {
		t.Fatalf("failed = %+v", r)
	}
	r = methodResult{Status: benchHung, Attempts: 3, Successful: 2, Errors: 1}
	r.finalize()
	if r.Status != benchHung || r.stable() {
		t.Fatalf("hung must stay hung and unstable: %+v", r)
	}
}

func TestRecommendFastestStable(t *testing.T) {
	mk := func(name string, attempts, errors int, total ...float64) methodResult {
		r := methodResult{Method: name, Attempts: attempts, Successful: attempts - errors, Errors: errors, Total: summarize(total), Width: 1280, Height: 720}
		r.finalize()
		return r
	}
	results := []methodResult{
		mk("leidian-adb", 100, 0, 80, 90, 100),
		mk("leidian-window", 100, 30, 5, 5, 5), // fastest but unstable
		mk("leidian-adb-raw", 100, 2, 20, 25, 30),
		{Method: "mumu-sdk", Status: benchFailed},
	}
	best, ok := recommend(results)
	if !ok || best.Method != "leidian-adb-raw" {
		t.Fatalf("recommend = %q %v", best.Method, ok)
	}
	if line := recommendationLine(results); !strings.Contains(line, "leidian-adb-raw") || !strings.HasPrefix(line, "推荐：") {
		t.Fatalf("line = %q", line)
	}
	if _, ok := recommend(results[1:2]); ok {
		t.Fatal("unstable-only set must have no recommendation")
	}
	if line := recommendationLine(nil); !strings.Contains(line, "无") {
		t.Fatalf("empty line = %q", line)
	}
	// p90 tie falls back to p50.
	a, b := mk("a", 10, 0, 10, 20, 50), mk("b", 10, 0, 40, 40, 50)
	if best, _ := recommend([]methodResult{b, a}); best.Method != "a" {
		t.Fatalf("tie-break = %q", best.Method)
	}
}

func TestBenchMethods(t *testing.T) {
	cases := map[string]string{
		"mumu-sdk":        "mumu-sdk,printwindow-fullcontent",
		"leidian-adb":     "leidian-adb-raw,leidian-adb,leidian-window",
		"leidian-window":  "leidian-adb-raw,leidian-adb,leidian-window",
		"":                "mumu-sdk,printwindow-fullcontent,leidian-adb-raw,leidian-adb,leidian-window",
		"auto":            "mumu-sdk,printwindow-fullcontent,leidian-adb-raw,leidian-adb,leidian-window",
		"leidian-adb-raw": "leidian-adb-raw,leidian-adb,leidian-window",
	}
	for provider, want := range cases {
		got, err := benchMethods("", provider)
		if err != nil || strings.Join(got, ",") != want {
			t.Fatalf("%q: %v %v", provider, got, err)
		}
	}
	got, err := benchMethods(" leidian-adb , leidian-adb,mumu-sdk", "")
	if err != nil || strings.Join(got, ",") != "leidian-adb,mumu-sdk" {
		t.Fatalf("explicit = %v %v", got, err)
	}
	if _, err := benchMethods("leidian-png", ""); err == nil {
		t.Fatal("unknown method accepted")
	}
	if _, err := benchMethods(" , ", ""); err == nil {
		t.Fatal("empty list accepted")
	}
}

func TestDisplayWidthAndTableAlignment(t *testing.T) {
	if displayWidth("abc") != 3 || displayWidth("方式") != 4 || displayWidth("成功/尝试") != 9 || displayWidth("（）") != 4 {
		t.Fatal("display width")
	}
	var buf bytes.Buffer
	writeTable(&buf, []string{"方式", "p50"}, [][]string{{"leidian-adb", "1.0"}, {"雷电", "22.5"}})
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	col := -1
	for i, line := range lines {
		idx := strings.Index(line, []string{"p50", "1.0", "22.5"}[i])
		if w := displayWidth(line[:idx]); col >= 0 && w != col {
			t.Fatalf("misaligned column in %q", lines)
		} else {
			col = w
		}
	}
}

func TestRenderBenchReport(t *testing.T) {
	ok := methodResult{Method: "leidian-adb-raw", Attempts: 3, Successful: 3, Duplicates: 1, Width: 1280, Height: 720, ResolutionChanges: 1, WallSeconds: 1,
		Capture: summarize([]float64{10, 12, 14}), Analysis: summarize([]float64{5, 6}), Total: summarize([]float64{15, 18, 14})}
	ok.finalize()
	failed := methodResult{Method: "mumu-sdk", Error: "打开失败：no MuMu"}
	failed.finalize()
	var buf bytes.Buffer
	renderBench(&buf, []methodResult{ok, failed}, benchSettings{Analyzed: true, IntervalMS: 80, BudgetMS: 1200}, []ocrResult{{Provider: "cpu", Backend: "x", Profile: "duel", StartupMS: 900, Read: summarize([]float64{20, 22})}, {Provider: "directml", Error: "boom", Errors: 1}})
	out := buf.String()
	for _, want := range []string{"方式", "1280x720*", "正常", "失败", "采集", "分析", "合计", "33.3%", "mumu-sdk：打开失败：no MuMu", "OCR directml：boom", "推荐：leidian-adb-raw", "900.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
	buf.Reset()
	renderBench(&buf, []methodResult{ok}, benchSettings{BudgetMS: 1200}, nil)
	if strings.Contains(buf.String(), "分析") || strings.Contains(buf.String(), "OCR") {
		t.Fatalf("analysis/OCR rows printed when disabled:\n%s", buf.String())
	}
}
