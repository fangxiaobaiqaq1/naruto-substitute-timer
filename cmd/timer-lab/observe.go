package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
)

// observe measures the configured capture + detector + provider return path.
// It cannot measure event confirmation, UI delivery or drawing, so it never
// reports end-to-end latency.
// Default output is metadata only. Explicit evidence capture is bounded and
// encodes PNGs after measurement; raw-copy overhead is disclosed in the report.
func observe(args []string) error {
	f := flag.NewFlagSet("observe", flag.ContinueOnError)
	configPath := f.String("config", "config.json", "application configuration")
	duration := f.Duration("duration", 10*time.Second, "observation duration")
	interval := f.Duration("interval", 0, "sampling interval; default application fight interval")
	out := f.String("out", "", "new output directory, required")
	saveEvidence := f.Int("save-uncertain", 0, "explicit diagnostic: save first frame and uncertain frames after observing (0..8, at most 64 MiB)")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *duration <= 0 || *interval < 0 || *out == "" {
		return fmt.Errorf("positive duration, nonnegative interval and new out directory required")
	}
	if *saveEvidence < 0 || *saveEvidence > 8 {
		return fmt.Errorf("save-uncertain must be 0..8")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *interval == 0 {
		*interval = time.Duration(cfg.UI.PollIntervalMS) * time.Millisecond
	}
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		return fmt.Errorf("output directory must not exist: %s", *out)
	}
	eng, err := factory.NewLive(factory.FromApp(cfg))
	if err != nil {
		return err
	}
	defer eng.Close()
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	log, err := os.Create(filepath.Join(*out, "observations.jsonl"))
	if err != nil {
		return err
	}
	defer log.Close()
	enc := json.NewEncoder(log)
	provider, closeCapture := frame.NewConfiguredSnapshotter(eng, cfg)
	defer closeCapture()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var captureCosts, analysisCosts, totalCosts []float64
	counts := map[string]int{}
	attempts, errors, duplicates, skipped := 0, 0, 0, 0
	start := time.Now()
	evidence := diagnosticFrames{limit: *saveEvidence}
	for time.Since(start) < *duration && ctx.Err() == nil {
		beg := time.Now()
		fr := provider()
		elapsed := time.Since(beg)
		attempts++
		counts[fr.Scene]++
		entry := map[string]any{"sample": attempts, "offsetMs": time.Since(start).Seconds() * 1000, "source": fr.CaptureMethod, "scene": fr.Scene, "layoutProfile": fr.LayoutProfile, "hold": fr.Hold, "duplicate": fr.Duplicate, "left": knownCount(fr.Beads, 'L', fr.Slots(true, cfg.Layout.BeadsPerSide)), "right": knownCount(fr.Beads, 'R', fr.Slots(false, cfg.Layout.BeadsPerSide)), "providerCallMs": elapsed.Seconds() * 1000}
		for name, stamp := range map[string]time.Time{"captureStarted": fr.CaptureStarted, "capturedAt": fr.CapturedAt, "analysisStarted": fr.AnalysisStarted, "analyzedAt": fr.AnalyzedAt} {
			if !stamp.IsZero() {
				entry[name] = stamp
			}
		}
		entry["textStatus"] = fr.TextStatus
		entry["leftNinja"], entry["rightNinja"] = fr.LeftNinja, fr.RightNinja
		entry["playerSide"] = fr.PlayerSide
		if path := evidence.keep(fr); path != "" {
			entry["diagnosticImage"] = path
		}
		if fr.Err != nil || fr.Img == nil {
			errors++
			if fr.Err != nil {
				entry["error"] = fr.Err.Error()
			} else {
				entry["error"] = "capture returned no image"
			}
		} else if !validObservationFrame(fr) {
			skipped++
			entry["pipelineExcluded"] = "analysis missing or stage timestamps invalid"
		} else {
			totalCosts = append(totalCosts, elapsed.Seconds()*1000)
			entry["pipelineMs"] = elapsed.Seconds() * 1000
			if !fr.CaptureStarted.IsZero() && !fr.CapturedAt.Before(fr.CaptureStarted) {
				cost := fr.CapturedAt.Sub(fr.CaptureStarted).Seconds() * 1000
				captureCosts = append(captureCosts, cost)
				entry["captureMs"] = cost
			}
			if !fr.AnalyzedAt.IsZero() && !fr.AnalysisStarted.IsZero() {
				cost := fr.AnalyzedAt.Sub(fr.AnalysisStarted).Seconds() * 1000
				analysisCosts = append(analysisCosts, cost)
				entry["analyzeMs"] = cost
			}
		}
		if fr.Duplicate {
			duplicates++
		}
		if err := enc.Encode(entry); err != nil {
			return err
		}
		wait := max(time.Millisecond, *interval-time.Since(beg))
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	elapsed := time.Since(start).Seconds()
	for _, saved := range evidence.frames {
		file, err := os.OpenFile(filepath.Join(*out, saved.name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = png.Encode(file, saved.img)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	report := observationSummary(attempts, errors, duplicates, elapsed, counts, captureCosts, analysisCosts, totalCosts)
	report["skippedAnalysis"] = skipped
	if *saveEvidence > 0 {
		report["diagnosticImages"] = len(evidence.frames)
		report["diagnosticNote"] = "Explicit bounded raw copies; PNG encoding occurs after observation. Not a clean performance benchmark."
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "summary.json"), b, 0644); err != nil {
		return err
	}
	fmt.Println(string(b))
	if len(totalCosts) == 0 {
		return fmt.Errorf("no usable frames captured; diagnostics saved to %s", *out)
	}
	return nil
}

type diagnosticFrame struct {
	name string
	img  *image.RGBA
}

func validObservationFrame(f frame.Frame) bool {
	return f.Err == nil && f.Img != nil && !f.CapturedAt.IsZero() &&
		!f.AnalysisStarted.IsZero() && !f.AnalyzedAt.IsZero() &&
		!f.AnalysisStarted.Before(f.CapturedAt) && !f.AnalyzedAt.Before(f.AnalysisStarted) &&
		(f.CaptureStarted.IsZero() || !f.CapturedAt.Before(f.CaptureStarted))
}

type diagnosticFrames struct {
	limit, bytes int
	frames       []diagnosticFrame
}

func (d *diagnosticFrames) keep(f frame.Frame) string {
	if d.limit <= 0 || len(d.frames) >= min(d.limit, 8) || f.Img == nil || f.Err != nil {
		return ""
	}
	if len(d.frames) > 0 && !f.Hold && knownCount(f.Beads, 'L', f.Slots(true, 4)) != nil && knownCount(f.Beads, 'R', f.Slots(false, 4)) != nil {
		return ""
	}
	b := f.Img.Bounds()
	const budget = 64 << 20
	if b.Dx() <= 0 || b.Dy() <= 0 || b.Dx() > budget/4/b.Dy() {
		return ""
	}
	n := b.Dx() * b.Dy() * 4
	if n > budget-d.bytes {
		return ""
	}
	img := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(img, img.Bounds(), f.Img, b.Min, draw.Src)
	name := fmt.Sprintf("diagnostic-%02d.png", len(d.frames)+1)
	d.frames = append(d.frames, diagnosticFrame{name, img})
	d.bytes += n
	return name
}

func observationSummary(attempts, errors, duplicates int, elapsed float64, counts map[string]int, capture, analysis, total []float64) map[string]any {
	successful := len(total)
	status := "ok"
	if errors > 0 || successful < attempts {
		status = "partial"
	}
	if successful == 0 {
		status = "no_frames"
		capture, analysis, total = nil, nil, nil
	}
	// Null is intentionally different from a measured zero. A dead capture API
	// must never look like a 0ms, high-FPS success in a performance report.
	measure := func(values []float64, q float64) any {
		if len(values) == 0 {
			return nil
		}
		return percentile(values, q)
	}
	rate := func(n int) float64 {
		if elapsed <= 0 {
			return 0
		}
		return float64(n) / elapsed
	}
	return map[string]any{
		"status": status, "samples": attempts, "successful": successful, "errors": errors, "duplicates": duplicates, "scenes": counts, "seconds": elapsed,
		"samplesPerSecond": rate(successful), "attemptsPerSecond": rate(attempts), "latencyAvailable": false, "endToEndAvailable": false, "pipelineLatencyAvailable": successful > 0,
		"captureP95Ms": measure(capture, .95), "analysisP95Ms": measure(analysis, .95), "pipelineP50Ms": measure(total, .5), "pipelineP95Ms": measure(total, .95), "pipelineP99Ms": measure(total, .99),
		"endToEndP50Ms": nil, "endToEndP95Ms": nil, "endToEndP99Ms": nil,
		"sourceTimestampKnown": false, "note": "Pipeline is provider call duration for actual images with completed analysis. It excludes source render delay, event confirmation, UI delivery and overlay drawing; it is not end-to-end latency. Skipped analysis and capture failures are excluded. No accuracy claim.",
	}
}
