package main

import (
	"fmt"
	"image"
	"testing"
	"time"

	"narutotimer/internal/frame"
)

func TestObserveFailureIsNotZeroLatencySuccess(t *testing.T) {
	report := observationSummary(480, 480, 0, 8, map[string]int{"": 480}, nil, nil, nil)
	if report["status"] != "no_frames" || report["successful"] != 0 || report["samplesPerSecond"] != float64(0) || report["latencyAvailable"] != false {
		t.Fatalf("failed captures counted as frames: %v", report)
	}
	for _, key := range []string{"captureP95Ms", "analysisP95Ms", "pipelineP50Ms", "pipelineP95Ms", "pipelineP99Ms", "endToEndP50Ms", "endToEndP95Ms", "endToEndP99Ms"} {
		if report[key] != nil {
			t.Errorf("%s is a fabricated measurement: %v", key, report[key])
		}
	}
	if report["attemptsPerSecond"] != float64(60) {
		t.Fatal("attempt rate should remain diagnostic")
	}
	if report["pipelineLatencyAvailable"] != false || report["endToEndAvailable"] != false {
		t.Fatalf("failure exposed measured latency: %v", report)
	}
}

func TestObservePartialReportsOnlyActualFrames(t *testing.T) {
	report := observationSummary(10, 4, 2, 2, map[string]int{"fight": 6}, []float64{2, 3, 4, 5, 6, 7}, []float64{3, 4, 5, 6, 7, 8}, []float64{6, 7, 8, 9, 10, 11})
	if report["status"] != "partial" || report["successful"] != 6 || report["samplesPerSecond"] != float64(3) || report["attemptsPerSecond"] != float64(5) || report["pipelineP95Ms"] != float64(11) {
		t.Fatalf("wrong measured rates: %v", report)
	}
	if report["latencyAvailable"] != false || report["endToEndAvailable"] != false || report["pipelineLatencyAvailable"] != true {
		t.Fatalf("provider timing was presented as end-to-end latency: %v", report)
	}
}

func TestObserveOnlyCountsCompletedOrderedFrameStages(t *testing.T) {
	start := time.Now()
	valid := frame.Frame{Img: image.NewRGBA(image.Rect(0, 0, 2, 2)), CaptureStarted: start, CapturedAt: start.Add(time.Millisecond), AnalysisStarted: start.Add(10 * time.Millisecond), AnalyzedAt: start.Add(12 * time.Millisecond)}
	if !validObservationFrame(valid) {
		t.Fatal("completed capture and analysis should provide pipeline timing")
	}
	for _, tc := range []struct {
		name   string
		modify func(*frame.Frame)
	}{
		{"no image", func(f *frame.Frame) { f.Img = nil }},
		{"capture failed", func(f *frame.Frame) { f.Err = fmt.Errorf("capture failed") }},
		{"no capture", func(f *frame.Frame) { f.CapturedAt = time.Time{} }},
		{"analysis skipped", func(f *frame.Frame) { f.AnalysisStarted, f.AnalyzedAt = time.Time{}, time.Time{} }},
		{"missing analysis entry", func(f *frame.Frame) { f.AnalysisStarted = time.Time{} }},
		{"inverted capture", func(f *frame.Frame) { f.CaptureStarted = f.CapturedAt.Add(time.Second) }},
		{"analysis before capture", func(f *frame.Frame) { f.AnalysisStarted = start }},
		{"inverted analysis", func(f *frame.Frame) { f.AnalyzedAt = f.AnalysisStarted.Add(-time.Millisecond) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := valid
			tc.modify(&f)
			if validObservationFrame(f) {
				t.Fatalf("invalid frame was counted as timing evidence: %+v", f)
			}
		})
	}
}

func TestObserveNoCompletedFramesSuppressesStagePercentiles(t *testing.T) {
	report := observationSummary(3, 0, 0, 1, nil, []float64{1, 2, 3}, nil, nil)
	if report["status"] != "no_frames" || report["captureP95Ms"] != nil || report["pipelineLatencyAvailable"] != false {
		t.Fatalf("skipped frames became a latency report: %v", report)
	}
}
