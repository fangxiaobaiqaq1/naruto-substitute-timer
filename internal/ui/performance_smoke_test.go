package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/diagnostics"
)

// Opt-in visual artifact from the headless Fyne driver, using labeled synthetic
// readings. It validates layout, not native Windows rendering or real GPU data.
func TestPerformanceVisualSmoke(t *testing.T) {
	root := os.Getenv("NARUTO_PERFORMANCE_UI_SMOKE_DIR")
	if root == "" {
		t.Skip("set NARUTO_PERFORMANCE_UI_SMOKE_DIR for Fyne layout images")
	}
	s, r, posts := performanceTestSession(t)
	s.openPerformance()
	s.performanceView.profiles.SetChecked(false)
	if !s.startPerformance(false) {
		t.Fatal("start rejected")
	}
	pumpPerformance(t, posts, func() bool { return performancePhaseIs(s, performanceRunning) })
	loop := 38.0
	r.sample(diagnostics.PerformanceSample{At: time.Now(), ElapsedS: 65, IntervalS: 1, CollectMS: 1.2,
		Values: map[string]float64{"system_cpu_percent": 87, "main_cpu_percent": 4.5, "tree_cpu_percent": 6.2, "tree_cpu_cores": .496,
			"system_memory_used_percent": 72, "system_gpu_busiest_engine_percent": 64, "emulator_cpu_percent": 58, "emulator_working_set_bytes": 6 << 30,
			"main_working_set_bytes": 84 << 20, "tree_working_set_bytes": 126 << 20, "tree_private_bytes": 112 << 20, "go_heap_alloc_bytes": 30 << 20,
			"go_goroutines": 12, "tree_threads": 18, "tree_handles": 160},
		Top: []diagnostics.PerformanceTopProcess{
			{PID: 1, Name: "timer-app.exe", Role: "self", CPUPercent: performanceTestFloat(6.2), WorkingSetBytes: 126 << 20},
			{PID: 2, Name: "MuMuVMMHeadless.exe", Role: "emulator", CPUPercent: performanceTestFloat(52), WorkingSetBytes: 5 << 30, GPUPercent: performanceTestFloat(61)},
			{PID: 3, Name: "MuMuNxMain.exe", Role: "emulator", CPUPercent: performanceTestFloat(6), WorkingSetBytes: 1 << 30},
			{PID: 4, Name: "chrome.exe", CPUPercent: performanceTestFloat(11), WorkingSetBytes: 2 << 30},
			{PID: 5, Name: "obs64.exe", CPUPercent: performanceTestFloat(9), WorkingSetBytes: 700 << 20, GPUPercent: performanceTestFloat(12)}},
		Application: diagnostics.PerformanceApplication{Backend: "MuMu 实例 0 · SDK", Scene: "fight", Width: 1920, Height: 1080, PollMS: 16, Attempts: 3900, Analyzed: 2600, LoopMaxMS: &loop},
		Markers:     []string{"合成面板自检，非实战数据"}})
	pumpPerformance(t, posts, func() bool { return s.performanceView.tiles[0].Text == "87%" })
	// GLFW automatically re-lays out labels after their text grows; the
	// headless canvas needs the same explicit relayout used by preview tests.
	s.settings.Content().Refresh()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name  string
		image image.Image
	}{
		{"performance-panel.png", s.settings.Canvas().Capture()},
		{"performance-entry.png", s.win.Canvas().Capture()},
	} {
		f, err := os.Create(filepath.Join(root, item.name))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, item.image)
		closeErr := f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	t.Logf("PERFORMANCE_UI_IMAGES=%s", root)
}
