package diagnostics

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in real collector/export smoke check, preserving its files for browser
// and pprof inspection. It does not start capture, Fyne, OCR or another process.
func TestPerformanceLiveExport(t *testing.T) {
	root := os.Getenv("NARUTO_PERFORMANCE_SMOKE_DIR")
	if root == "" {
		t.Skip("set NARUTO_PERFORMANCE_SMOKE_DIR to retain a real resource report")
	}
	notify := make(chan struct{}, 1)
	r, err := NewPerformance(PerformanceOptions{Root: root, Version: "performance-smoke-test", Profiles: true,
		OnSample: func() {
			select {
			case notify <- struct{}{}:
			default:
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for r.Snapshot().Samples < 3 {
		select {
		case <-notify:
		case <-r.Done():
			t.Fatalf("collector stopped early: %+v", r.Snapshot())
		case <-deadline.C:
			t.Fatal("collector did not produce three samples")
		}
	}
	if err := r.Mark("集成自检标记（非实战卡顿）"); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	status := r.Snapshot()
	if status.Latest == nil || status.Latest.Values["go_heap_alloc_bytes"] <= 0 {
		t.Fatal("runtime metrics missing")
	}
	for _, p := range status.Latest.Processes {
		if p.PID == os.Getpid() {
			if _, supplied := p.Values["cpu_seconds"]; supplied && !p.CounterAt.IsZero() {
				if _, ok := p.Values["cpu_percent"]; !ok {
					t.Fatalf("process baseline did not become a rate: %+v", p.Unavailable)
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path, err := r.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	files := make(map[string]bool)
	for _, f := range z.File {
		if filepath.IsAbs(f.Name) || strings.Contains(f.Name, "..") {
			t.Fatal("unsafe ZIP member", f.Name)
		}
		files[f.Name] = true
	}
	for _, name := range []string{"report.html", "report.md", "summary.json", "metadata.json", "samples.jsonl", "samples.csv", "heap-start.pprof", "heap-end.pprof", "goroutines-start.txt", "goroutines-end.txt"} {
		if !files[name] {
			t.Fatal("missing report member", name)
		}
	}
	t.Logf("PERFORMANCE_REPORT=%s", path)
}
