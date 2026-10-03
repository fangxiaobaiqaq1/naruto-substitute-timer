package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"os"
	"os/signal"
	"strings"
	"time"

	"narutotimer/internal/capture"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/hudtext"
	"narutotimer/internal/ocr"
	"narutotimer/internal/win"
)

// bench measures each capture method through the production capture registry,
// paced like the app's fight poll (or back to back with -interval 0), then
// (optionally) the live engine: SDK/雷电 rows skip duplicate frames as the
// app's dedupedAnalysis does, the printwindow row analyzes every frame as
// frame.snapshot does. A frame whose capture+analysis exceeds
// capture.timeoutMs is counted as a timeout, as boundedProvider drops it.
// Frames are never written to disk.
func bench(args []string) error {
	f := flag.NewFlagSet("bench", flag.ContinueOnError)
	configPath := f.String("config", "config.json", "application configuration")
	methodList := f.String("methods", "", "comma-separated capture methods; default every method valid for capture.provider")
	frames := f.Int("frames", 100, "measured frames per method")
	warmup := f.Int("warmup", 5, "unmeasured frames per method before measuring")
	analyze := f.Bool("analyze", true, "run the live engine on non-duplicate frames and time it separately")
	withOCR := f.Bool("ocr", false, "also time name-ROI OCR with the cpu and directml execution providers")
	ocrRuns := f.Int("ocr-runs", 10, "measured OCR reads per execution provider")
	jsonOut := f.String("json", "", "optional NEW JSON result file")
	interval := f.Int("interval", -1, "ms between capture starts; default max(80, ui.pollIntervalMs) like the app's fight poll, 0 = back to back")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *frames < 1 || *warmup < 0 || *ocrRuns < 1 || *interval < -1 {
		return fmt.Errorf("frames >=1, warmup >=0, ocr-runs >=1 and interval >=0 required")
	}
	if *jsonOut != "" {
		if _, err := os.Stat(*jsonOut); !os.IsNotExist(err) {
			return fmt.Errorf("output file must not exist: %s", *jsonOut)
		}
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	methods, err := benchMethods(*methodList, cfg.Capture.Provider)
	if err != nil {
		return err
	}
	mode, err := detect.ParseMode(cfg.Layout.ContentMode)
	if err != nil {
		return err
	}
	if *interval < 0 {
		*interval = max(80, cfg.UI.PollIntervalMS)
	}
	set := benchSettings{Analyzed: *analyze, IntervalMS: float64(*interval), BudgetMS: float64(frameBudget(cfg).Milliseconds())}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// After the first Ctrl+C, restore default handling so a second one kills
	// the process even if a native call is stuck.
	go func() {
		<-ctx.Done()
		stop()
	}()
	var results []methodResult
	var ocrFrame *image.RGBA
	ocrProfile := ""
	for _, method := range methods {
		if ctx.Err() != nil {
			break
		}
		fmt.Fprintf(os.Stderr, "测量 %s …\n", method)
		r, last, profile := benchMethod(ctx, cfg, mode, method, *frames, *warmup, *analyze, time.Duration(*interval)*time.Millisecond)
		results = append(results, r)
		if last != nil && (ocrFrame == nil || (ocrProfile == "" && profile != "")) {
			ocrFrame, ocrProfile = last, profile
		}
	}
	var ocrs []ocrResult
	if *withOCR && ctx.Err() == nil {
		ocrs = benchOCR(ctx, cfg, ocrFrame, ocrProfile, *ocrRuns)
	}
	renderBench(os.Stdout, results, set, ocrs)
	if *jsonOut == "" {
		return nil
	}
	best, _ := recommend(results)
	data, err := json.MarshalIndent(map[string]any{
		"generatedAt": time.Now(), "config": *configPath, "provider": cfg.Capture.Provider, "frames": *frames, "warmup": *warmup, "analyze": *analyze,
		"intervalMs": set.IntervalMS, "frameBudgetMs": set.BudgetMS,
		"methods": results, "ocr": ocrs, "recommended": best.Method, "recommendation": recommendationLine(results),
		"note": "Capture is API+copy time, not game-to-display latency; capture starts are paced every intervalMs (0 = back to back). " +
			"Duplicates share the live provider's pixel fingerprint; SDK/Leidian rows skip analysis on duplicates like dedupedAnalysis, the printwindow row runs ClassifyScreen+Analyze on every frame like frame.snapshot. " +
			"A frame whose capture+analysis exceeds frameBudgetMs is counted in timeouts and errors and left out of the latency stats, as the app drops it. " +
			"Total is capture plus analysis per frame. The recommendation is the stable method (error rate incl. timeouts <= 5%) with the lowest capture p90 + analysis p90, which does not reward duplicate frames.",
	}, "", "  ")
	if err != nil {
		return err
	}
	out, err := os.OpenFile(*jsonOut, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = out.Write(append(data, '\n'))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// frameBudget is the app's per-frame capture+analysis budget (boundedProvider).
func frameBudget(cfg config.Config) time.Duration {
	if cfg.Capture.TimeoutMS <= 0 {
		return defaultFrameBudgetMS * time.Millisecond
	}
	return time.Duration(cfg.Capture.TimeoutMS) * time.Millisecond
}

// benchWatchdog only detects hung native calls; the frame budget is separate.
// benchWatchdog bounds one native call. A call that exceeds it is abandoned
// (never closed concurrently) and the method is reported as hung.
func benchWatchdog(cfg config.Config) time.Duration {
	return max(5*time.Second, 4*time.Duration(cfg.Capture.TimeoutMS)*time.Millisecond)
}

type captureResult struct {
	img               *image.RGBA
	started, captured time.Time
	err               error
}

func openBenchClient(cfg config.Config, method string) (capture.Client, error) {
	if method == capture.MethodPrintWindow {
		// Same per-frame path as frame.snapshot: cached window lookup + PrintWindow.
		return capture.StaticClient{SourceName: capture.MethodPrintWindow, CaptureFunc: func() (*image.RGBA, error) {
			wins := win.FindMuMu()
			if len(wins) == 0 {
				win.InvalidateMuMu()
				return nil, fmt.Errorf("未找到模拟器窗口")
			}
			img, err := win.CaptureClient(pickBenchWindow(wins).HWND)
			if err != nil && !win.IsBusyFrame(err) {
				win.InvalidateMuMu()
			}
			return img, err
		}}, nil
	}
	c := cfg.Capture
	// A single-entry chain keeps the 雷电 client from silently falling back to
	// another method, so each row measures exactly one path.
	c.PreferredMethods = []string{method}
	return capture.OpenConfigured(context.Background(), method, c)
}

// pickBenchWindow mirrors frame.pickGameWindow.
func pickBenchWindow(wins []win.Window) win.Window {
	for _, w := range wins {
		name := strings.ToLower(w.ProcessName)
		if strings.Contains(name, "device") || strings.Contains(name, "player") {
			return w
		}
	}
	return wins[0]
}

func benchMethod(ctx context.Context, cfg config.Config, mode detect.ContentMode, method string, frames, warmup int, analyze bool, interval time.Duration) (r methodResult, last *image.RGBA, profile string) {
	r.Method = method
	watchdog := benchWatchdog(cfg)
	budget := frameBudget(cfg)
	type opened struct {
		client capture.Client
		err    error
	}
	// A late open is closed once it returns; nothing else uses that client.
	closeLate := func(ch <-chan opened) {
		go func() {
			if o := <-ch; o.client != nil {
				o.client.Close()
			}
		}()
	}
	openStarted := time.Now()
	openDone := make(chan opened, 1)
	go func() {
		c, err := openBenchClient(cfg, method)
		openDone <- opened{c, err}
	}()
	var client capture.Client
	select {
	case o := <-openDone:
		if o.err == nil && o.client == nil {
			o.err = fmt.Errorf("provider returned an empty client")
		}
		if o.err != nil {
			r.Error = "打开失败：" + o.err.Error()
			r.finalize()
			return r, nil, ""
		}
		client = o.client
	case <-time.After(3 * watchdog):
		closeLate(openDone)
		r.Status, r.Error = benchHung, fmt.Sprintf("打开超过 %s 未返回，已放弃", 3*watchdog)
		r.finalize()
		return r, nil, ""
	case <-ctx.Done():
		closeLate(openDone)
		r.Error = "已中断：打开未完成"
		r.finalize()
		return r, nil, ""
	}
	r.OpenMS = time.Since(openStarted).Seconds() * 1000
	r.Source = client.Source()
	hung := false
	defer func() {
		if !hung {
			client.Close()
		}
	}()
	var eng *hudtext.Engine
	if analyze {
		var err error
		if eng, err = factory.NewLive(factory.FromApp(cfg)); err != nil {
			r.Error = "引擎创建失败：" + err.Error()
			analyze = false
		} else {
			defer eng.Close()
		}
	}
	// grab returns ok=false when the call was abandoned: interrupted=true for
	// Ctrl+C, otherwise the watchdog fired. The client stays open either way.
	grab := func() (res captureResult, ok, interrupted bool) {
		done := make(chan captureResult, 1)
		go func() {
			started := time.Now()
			img, err := client.Capture()
			done <- captureResult{img, started, time.Now(), err}
		}()
		select {
		case res := <-done:
			if res.err == nil && res.img == nil {
				res.err = fmt.Errorf("capture returned no image")
			}
			if res.err == nil && (res.img.Bounds().Dx() < 400 || res.img.Bounds().Dy() < 250) {
				res.err = fmt.Errorf("画面过小 %dx%d（至少 400x250）", res.img.Bounds().Dx(), res.img.Bounds().Dy())
			}
			return res, true, false
		case <-time.After(watchdog):
			return captureResult{}, false, false
		case <-ctx.Done():
			return captureResult{}, false, true
		}
	}
	// The printwindow row runs the same per-frame work as frame.snapshot:
	// ClassifyScreen, then Analyze unless the frame is blank, on every frame.
	var captureCosts, analysisCosts, totalCosts []float64
	var previous uint64
	havePrevious := false
	consecutive := 0
	var wallStart time.Time
	var nextStart time.Time
	for i := 0; i < warmup+frames && ctx.Err() == nil; i++ {
		if !nextStart.IsZero() && interval > 0 {
			if wait := time.Until(nextStart); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					continue
				}
			}
		}
		captureStarted := time.Now()
		if interval > 0 {
			nextStart = captureStarted.Add(interval)
		}
		measured := i >= warmup
		if i == warmup {
			wallStart = captureStarted
		}
		res, ok, interrupted := grab()
		if !ok {
			if interrupted {
				r.Error = "已中断：采集未完成"
				break
			}
			hung = true
			r.Status = benchHung
			r.Error = fmt.Sprintf("单帧采集超过 %s 未返回，客户端已放弃（不关闭）", watchdog)
			if measured {
				r.Attempts++
				r.Errors++
			}
			break
		}
		if res.err != nil {
			consecutive++
			if measured {
				r.Attempts++
				r.Errors++
				if len(r.SampleErrors) < 3 {
					r.SampleErrors = append(r.SampleErrors, res.err.Error())
				}
			}
			if consecutive >= 10 {
				r.Error = "连续 10 帧失败，停止该方式"
				if !measured && len(r.SampleErrors) == 0 {
					r.SampleErrors = append(r.SampleErrors, res.err.Error())
				}
				break
			}
			continue
		}
		consecutive = 0
		hash := frame.Fingerprint(res.img, r.Source)
		duplicate := havePrevious && hash == previous
		previous, havePrevious = hash, true
		var analysisMS float64
		var frameProfile string
		var fighting bool
		analyzeFrame := analyze && (!duplicate || method == capture.MethodPrintWindow)
		if analyzeFrame {
			fr := frame.AnalyzeImage(res.img, eng, mode, res.started, res.captured, r.Source)
			if !fr.AnalysisStarted.IsZero() && !fr.AnalyzedAt.Before(fr.AnalysisStarted) {
				analysisMS = fr.AnalyzedAt.Sub(fr.AnalysisStarted).Seconds() * 1000
			} else if measured {
				r.AnalysisSkipped++
			}
			if fr.Fighting && fr.LayoutProfile != "" {
				frameProfile, fighting = fr.LayoutProfile, true
			}
		} else if analyze && measured {
			r.AnalysisSkipped++
		}
		if !measured {
			if frameProfile != "" {
				profile = frameProfile
			}
			continue
		}
		r.Attempts++
		captureMS := res.captured.Sub(res.started).Seconds() * 1000
		totalMS := captureMS + analysisMS
		if totalMS > float64(budget.Microseconds())/1000 {
			r.Errors++
			r.Timeouts++
			if len(r.SampleErrors) < 3 {
				r.SampleErrors = append(r.SampleErrors, fmt.Sprintf("帧预算 %.0fms 超出：%.1fms", float64(budget.Microseconds())/1000, totalMS))
			}
			continue
		}
		last = res.img
		if frameProfile != "" {
			profile = frameProfile
		}
		if fighting {
			r.Fighting++
		}
		w, h := res.img.Bounds().Dx(), res.img.Bounds().Dy()
		if r.Successful > 0 && (w != r.Width || h != r.Height) {
			r.ResolutionChanges++
		}
		r.Width, r.Height = w, h
		r.Successful++
		if duplicate {
			r.Duplicates++
		}
		captureCosts = append(captureCosts, captureMS)
		if analysisMS > 0 {
			analysisCosts = append(analysisCosts, analysisMS)
		}
		totalCosts = append(totalCosts, totalMS)
	}
	if !wallStart.IsZero() {
		r.WallSeconds = time.Since(wallStart).Seconds()
	}
	r.Capture, r.Analysis, r.Total = summarize(captureCosts), summarize(analysisCosts), summarize(totalCosts)
	r.finalize()
	return r, last, profile
}

// benchOCR launches the local OCR helper once per execution provider
// (NARUTO_OCR_EXECUTION_PROVIDER=cpu, then directml) and times hudtext.Diagnose,
// the same two name-ROI sheet the live engine reads.
func benchOCR(ctx context.Context, cfg config.Config, img *image.RGBA, profile string, runs int) []ocrResult {
	note := ""
	if img == nil {
		return []ocrResult{{Provider: "-", Backend: ocr.BackendName(), Error: "没有成功采集的画面可供 OCR"}}
	}
	if profile == "" {
		// Not a battle frame: geometry still resolves, so timing stays valid.
		profile = cfg.Layout.PreferredProfile
		if profile != "camp" && profile != "duel" {
			profile = "duel"
		}
		note = "（非对局画面，仅计时）"
	}
	var out []ocrResult
	for _, provider := range []string{"cpu", "directml"} {
		if ctx.Err() != nil {
			break
		}
		fmt.Fprintf(os.Stderr, "测量 OCR %s …\n", provider)
		o := ocrResult{Provider: provider, Backend: ocr.BackendName(), Profile: profile + note}
		reader := ocr.NewLocal(provider == "directml")
		call := func(limit time.Duration) (float64, error) {
			c, cancel := context.WithTimeout(ctx, limit)
			defer cancel()
			started := time.Now()
			_, err := hudtext.Diagnose(c, reader, img, cfg, profile)
			return time.Since(started).Seconds() * 1000, err
		}
		first, err := call(30 * time.Second)
		if err != nil {
			o.Errors++
			o.Error = "首次识别失败：" + err.Error()
		} else {
			o.StartupMS = first
			var costs []float64
			for i := 0; i < runs && ctx.Err() == nil; i++ {
				cost, err := call(5 * time.Second)
				if err != nil {
					o.Errors++
					o.Error = err.Error()
					continue
				}
				costs = append(costs, cost)
			}
			o.Read = summarize(costs)
		}
		reader.Close()
		out = append(out, o)
	}
	return out
}
