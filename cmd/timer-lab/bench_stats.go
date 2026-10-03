package main

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"narutotimer/internal/capture"
)

// Portable statistics and report formatting for the bench subcommand. Nothing
// here touches capture or OCR, so it is unit-tested on every platform.

const (
	benchOK      = "ok"      // every measured frame succeeded
	benchPartial = "partial" // some frames failed
	benchFailed  = "failed"  // open failed or no frame succeeded
	benchHung    = "hung"    // a native call exceeded the watchdog; client abandoned

	// defaultFrameBudgetMS mirrors frame.NewSelectableSnapshotter when
	// capture.timeoutMs <= 0.
	defaultFrameBudgetMS = 1200

	// A method is "stable" when at most this share of measured attempts fail
	// (over-budget frames count as failures).
	stableErrorRate = 0.05
)

// latencyStats is in milliseconds. Count 0 means "not measured", never 0 ms.
type latencyStats struct {
	Count int     `json:"count"`
	Min   float64 `json:"minMs"`
	P50   float64 `json:"p50Ms"`
	P90   float64 `json:"p90Ms"`
	P99   float64 `json:"p99Ms"`
	Max   float64 `json:"maxMs"`
	Mean  float64 `json:"meanMs"`
}

func summarize(values []float64) latencyStats {
	if len(values) == 0 {
		return latencyStats{}
	}
	s := latencyStats{Count: len(values), Min: values[0], Max: values[0]}
	sum := 0.0
	for _, v := range values {
		s.Min, s.Max = min(s.Min, v), max(s.Max, v)
		sum += v
	}
	s.Mean = sum / float64(len(values))
	s.P50, s.P90, s.P99 = percentile(values, .5), percentile(values, .9), percentile(values, .99)
	return s
}

// fps converts a mean per-frame cost into frames per second.
func (s latencyStats) fps() float64 {
	if s.Count == 0 || s.Mean <= 0 {
		return 0
	}
	return 1000 / s.Mean
}

type methodResult struct {
	Method            string       `json:"method"`
	Source            string       `json:"source,omitempty"`
	Status            string       `json:"status"`
	Error             string       `json:"error,omitempty"`
	SampleErrors      []string     `json:"sampleErrors,omitempty"`
	OpenMS            float64      `json:"openMs"`
	Attempts          int          `json:"attempts"`
	Successful        int          `json:"successful"`
	Errors            int          `json:"errors"`
	Timeouts          int          `json:"timeouts"`
	Duplicates        int          `json:"duplicates"`
	Width             int          `json:"width"`
	Height            int          `json:"height"`
	ResolutionChanges int          `json:"resolutionChanges"`
	Capture           latencyStats `json:"capture"`
	Analysis          latencyStats `json:"analysis"`
	AnalysisSkipped   int          `json:"analysisSkipped"`
	Fighting          int          `json:"fightingFrames"`
	Total             latencyStats `json:"total"`
	WallSeconds       float64      `json:"wallSeconds"`
	CaptureFPS        float64      `json:"captureFps"`
	PipelineFPS       float64      `json:"pipelineFps"`
	UniqueFPS         float64      `json:"uniqueFps"`
}

func (r methodResult) errorRate() float64 {
	if r.Attempts == 0 {
		return 0
	}
	return float64(r.Errors) / float64(r.Attempts)
}

// timeoutRate is the share of measured attempts whose capture+analysis exceeded
// the app's per-frame budget; those frames are also counted in Errors.
func (r methodResult) timeoutRate() float64 {
	if r.Attempts == 0 {
		return 0
	}
	return float64(r.Timeouts) / float64(r.Attempts)
}

// duplicateRate is relative to successful frames: the first frame cannot be a
// duplicate, matching the live provider.
func (r methodResult) duplicateRate() float64 {
	if r.Successful == 0 {
		return 0
	}
	return float64(r.Duplicates) / float64(r.Successful)
}

// finalize derives status and rates from the raw counters.
func (r *methodResult) finalize() {
	if r.WallSeconds > 0 {
		r.PipelineFPS = float64(r.Successful) / r.WallSeconds
		r.UniqueFPS = float64(r.Successful-r.Duplicates) / r.WallSeconds
	}
	r.CaptureFPS = r.Capture.fps()
	switch {
	case r.Status == benchHung:
	case r.Successful == 0:
		r.Status = benchFailed
	case r.Errors > 0:
		r.Status = benchPartial
	default:
		r.Status = benchOK
	}
}

func (r methodResult) stable() bool {
	return (r.Status == benchOK || r.Status == benchPartial) && r.Successful > 0 && r.Total.Count > 0 && r.errorRate() <= stableErrorRate
}

// rankStats is the cost of one unique frame: capture percentiles plus analysis
// percentiles over the frames that were analyzed. Unlike Total it does not get
// cheaper as the duplicate rate rises, so a method returning stale buffers
// cannot win by skipping analysis.
func (r methodResult) rankStats() latencyStats {
	if r.Capture.Count == 0 {
		return r.Total
	}
	return latencyStats{Count: r.Capture.Count, P50: r.Capture.P50 + r.Analysis.P50, P90: r.Capture.P90 + r.Analysis.P90, Mean: r.Capture.Mean + r.Analysis.Mean}
}

// recommend picks the stable method with the lowest capture p90 + analysis p90
// (see rankStats); p50 sums then mean sums break ties.
func recommend(results []methodResult) (methodResult, bool) {
	var best methodResult
	found := false
	for _, r := range results {
		if !r.stable() {
			continue
		}
		if !found || less(r.rankStats(), best.rankStats()) {
			best, found = r, true
		}
	}
	return best, found
}

func less(a, b latencyStats) bool {
	if a.P90 != b.P90 {
		return a.P90 < b.P90
	}
	if a.P50 != b.P50 {
		return a.P50 < b.P50
	}
	return a.Mean < b.Mean
}

func recommendationLine(results []methodResult) string {
	best, ok := recommend(results)
	if !ok {
		return fmt.Sprintf("推荐：无（没有错误率（含超时）≤%.0f%% 的可用采集方式）", stableErrorRate*100)
	}
	return fmt.Sprintf("推荐：%s（采集 p90+识别 p90 %.1fms，实测 %.1f FPS，不重复 %.1f FPS，错误率 %.1f%%，超时率 %.1f%%，重复率 %.1f%%，%dx%d）",
		best.Method, best.rankStats().P90, best.PipelineFPS, best.UniqueFPS, best.errorRate()*100, best.timeoutRate()*100, best.duplicateRate()*100, best.Width, best.Height)
}

type ocrResult struct {
	Provider  string       `json:"executionProvider"`
	Backend   string       `json:"backend"`
	Profile   string       `json:"profile"`
	StartupMS float64      `json:"firstCallMs"`
	Read      latencyStats `json:"read"`
	Errors    int          `json:"errors"`
	Error     string       `json:"error,omitempty"`
}

var statusNames = map[string]string{benchOK: "正常", benchPartial: "部分失败", benchFailed: "失败", benchHung: "卡死"}

func statusName(s string) string {
	if name, ok := statusNames[s]; ok {
		return name
	}
	return s
}

// benchSettings are the run-wide parameters shown in the report.
type benchSettings struct {
	Analyzed   bool    `json:"analyze"`
	IntervalMS float64 `json:"intervalMs"` // 0 = back to back
	BudgetMS   float64 `json:"frameBudgetMs"`
}

// renderBench prints the human table. Latency columns are milliseconds.
func renderBench(w io.Writer, results []methodResult, set benchSettings, ocrs []ocrResult) {
	analyzed := set.Analyzed
	header := []string{"方式", "状态", "分辨率", "成功/尝试", "错误率", "超时率", "重复率", "阶段", "min", "p50", "p90", "p99", "max", "均值", "FPS"}
	var rows [][]string
	for _, r := range results {
		res := "-"
		if r.Width > 0 {
			res = fmt.Sprintf("%dx%d", r.Width, r.Height)
			if r.ResolutionChanges > 0 {
				res += "*"
			}
		}
		lead := []string{r.Method, statusName(r.Status), res, fmt.Sprintf("%d/%d", r.Successful, r.Attempts), pct(r.errorRate()), pct(r.timeoutRate()), pct(r.duplicateRate())}
		blank := make([]string, len(lead))
		rows = append(rows, append(lead, statsCells("采集", r.Capture, r.CaptureFPS)...))
		if analyzed && r.Successful > 0 {
			rows = append(rows, append(blank, statsCells("分析", r.Analysis, r.Analysis.fps())...))
			rows = append(rows, append(blank, statsCells("合计", r.Total, r.PipelineFPS)...))
		}
	}
	writeTable(w, header, rows)
	for _, r := range results {
		if r.Error != "" {
			fmt.Fprintf(w, "  %s：%s\n", r.Method, r.Error)
		}
		for _, e := range r.SampleErrors {
			fmt.Fprintf(w, "  %s 帧错误：%s\n", r.Method, e)
		}
	}
	pacing := "连续采集不等待（重复率、FPS 高于程序实际）"
	if set.IntervalMS > 0 {
		pacing = fmt.Sprintf("每 %.0fms 开始一次采集（同程序对局轮询）", set.IntervalMS)
	}
	note := fmt.Sprintf("说明：耗时单位 ms，%s；采集+识别超过 %.0fms（capture.timeoutMs）的帧同程序丢弃，计入超时与错误、不计入耗时统计；采集 FPS=1000/均值；重复帧与上一帧像素指纹相同；分辨率带 * 表示测量期间变化过。", pacing, set.BudgetMS)
	if analyzed {
		note += "合计行为采集+识别（与程序一样串行；MuMu SDK/雷电重复帧跳过识别，printwindow 每帧都做空帧判断+识别），FPS 为实测。推荐按采集 p90+识别 p90 排序，不因重复帧变快。"
	}
	fmt.Fprintln(w, note)
	if len(ocrs) > 0 {
		fmt.Fprintln(w)
		header := []string{"OCR", "后端", "布局", "首次(含启动)", "min", "p50", "p90", "p99", "max", "均值", "次/秒", "错误"}
		var rows [][]string
		for _, o := range ocrs {
			first := "-"
			if o.StartupMS > 0 {
				first = ms(o.StartupMS)
			}
			row := []string{o.Provider, o.Backend, o.Profile, first}
			row = append(row, statsCells("", o.Read, o.Read.fps())[1:]...)
			rows = append(rows, append(row, fmt.Sprint(o.Errors)))
		}
		writeTable(w, header, rows)
		for _, o := range ocrs {
			if o.Error != "" {
				fmt.Fprintf(w, "  OCR %s：%s\n", o.Provider, o.Error)
			}
		}
		fmt.Fprintln(w, "说明：OCR 为两侧名字 ROI 的一次识别；DirectML 不可用时助手静默回退 CPU，两行数字相近即说明未启用 GPU；后端为“系统 OCR”（非 cgo 构建）时执行提供程序不生效。")
	}
	fmt.Fprintln(w, recommendationLine(results))
}

func statsCells(stage string, s latencyStats, fps float64) []string {
	if s.Count == 0 {
		return []string{stage, "-", "-", "-", "-", "-", "-", "-"}
	}
	return []string{stage, ms(s.Min), ms(s.P50), ms(s.P90), ms(s.P99), ms(s.Max), ms(s.Mean), fmt.Sprintf("%.1f", fps)}
}

func ms(v float64) string  { return fmt.Sprintf("%.1f", v) }
func pct(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }

// writeTable left-aligns columns by terminal display width so CJK headers line up.
func writeTable(w io.Writer, header []string, rows [][]string) {
	widths := make([]int, len(header))
	for _, row := range append([][]string{header}, rows...) {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], displayWidth(cell))
			}
		}
	}
	for _, row := range append([][]string{header}, rows...) {
		var b strings.Builder
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			b.WriteString(cell)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-displayWidth(cell)+2))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

// displayWidth counts East Asian wide runes as two columns.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r),
			r >= 0x3000 && r <= 0x303f, r >= 0xff00 && r <= 0xff60, r >= 0xffe0 && r <= 0xffe6:
			n += 2
		default:
			n++
		}
	}
	return n
}

// benchMethods returns the requested methods, or every method valid for the
// configured provider. Unknown names are rejected before any client opens.
func benchMethods(list, provider string) ([]string, error) {
	leidian := []string{capture.MethodLeidianADBRaw, capture.MethodLeidianADB, capture.MethodLeidianWindow}
	mumu := []string{capture.MethodMuMuSDK, capture.MethodPrintWindow}
	if strings.TrimSpace(list) == "" {
		switch provider {
		case capture.MethodMuMuSDK, capture.MethodPrintWindow:
			return mumu, nil
		case capture.MethodLeidianADB, capture.MethodLeidianADBRaw, capture.MethodLeidianWindow:
			return leidian, nil
		default:
			return append(mumu, leidian...), nil
		}
	}
	known := map[string]bool{}
	for _, m := range append(mumu, leidian...) {
		known[m] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range strings.Split(list, ",") {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		if !known[m] {
			return nil, fmt.Errorf("unknown capture method %q (want %s)", m, strings.Join(append(mumu, leidian...), ", "))
		}
		seen[m] = true
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("methods: empty list")
	}
	return out, nil
}
