package factory

// Realistic per-frame cost of the recognition engine (docs/性能优化-windows.md).
//
//	taskset -c 0-3 env GOMAXPROCS=4 go test ./internal/engine/factory \
//	  -run '^$' -bench '^BenchmarkFrames$' -benchtime 96x -count 6 -benchmem
//	NARUTO_FOOTPRINT=1 go test ./internal/engine/factory -run '^TestFrameFootprint$' -count 1 -v
//
// Every sub-benchmark is one capture geometry: one engine, frames 16 ms apart
// (so the 100 ms cold scans and 500 ms avatar full scans are included), cycling
// through perturbVariants copies of the frame so pixel memos never hit.
// NARUTO_PERF_CORPUS (default /ssd/work/perf-corpus) supplies the real frames
// and calibrated synthetic frames; missing files skip only their own case.

import (
	"bufio"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine"
)

const (
	perfFrameStep = 16 * time.Millisecond
	perfVariants  = 4 // K perturbed copies per frame, used round-robin
)

type perfScenario struct {
	name        string
	path        string // PNG; empty = synthetic duel frame
	calibration string // gocheck --calibration form, optional
}

func perfCorpus() string {
	if dir := os.Getenv("NARUTO_PERF_CORPUS"); dir != "" {
		return dir
	}
	return "/ssd/work/perf-corpus"
}

func perfScenarios() []perfScenario {
	corpus := perfCorpus()
	var out []perfScenario
	real, _ := filepath.Glob(filepath.Join(corpus, "real", "*.png"))
	sort.Strings(real)
	for _, f := range real {
		name := strings.TrimSuffix(filepath.Base(f), ".png")
		name = strings.NewReplacer(" ", "", "(", "", ")", "", "图片_", "img").Replace(name)
		out = append(out, perfScenario{name: "real-" + name, path: f})
	}
	out = append(out, perfScenario{name: "synthetic-duel-1920x1080"})
	for _, c := range []string{
		"calib/control-tablet-2560x1600-camp",
		"calib/special-tablet-2388x1668-camp-left-xiayin-red",
		"geometry/1920x1080-duel-special",
	} {
		dir := filepath.Join(corpus, "synthetic", c)
		calibration, _ := os.ReadFile(filepath.Join(dir, "calibration.txt"))
		out = append(out, perfScenario{name: strings.ReplaceAll(c, "/", "-"), path: filepath.Join(dir, "0.png"), calibration: strings.TrimSpace(string(calibration))})
	}
	return out
}

func perfLoad(tb testing.TB, sc perfScenario, cfg config.Config) *image.RGBA {
	tb.Helper()
	if sc.path == "" {
		return syntheticDuelFrame(tb, cfg, 1920, 1080, 4)
	}
	f, err := os.Open(sc.path)
	if os.IsNotExist(err) {
		tb.Skipf("perf corpus frame unavailable: %s", sc.path)
	}
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	src, err := png.Decode(bufio.NewReader(f))
	if err != nil {
		tb.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img
}

// perfCalibration parses gocheck's "PROFILE:LB/RB/LA/RA" form.
func perfCalibration(tb testing.TB, arg string) *config.RegionCalibration {
	if arg == "" {
		return nil
	}
	profile, boxes, ok := strings.Cut(arg, ":")
	parts := strings.Split(boxes, "/")
	if !ok || len(parts) != 4 {
		tb.Fatalf("bad calibration %q", arg)
	}
	c := &config.RegionCalibration{Profile: profile}
	for i, dst := range []**config.NormalizedRect{&c.LeftBeads, &c.RightBeads, &c.LeftAvatar, &c.RightAvatar} {
		if parts[i] == "-" {
			continue
		}
		fields := strings.Split(parts[i], ",")
		if len(fields) != 4 {
			tb.Fatalf("bad calibration box %q", parts[i])
		}
		var v [4]float64
		for k, field := range fields {
			var err error
			if v[k], err = strconv.ParseFloat(field, 64); err != nil {
				tb.Fatal(err)
			}
		}
		*dst = &config.NormalizedRect{X: v[0], Y: v[1], Width: v[2], Height: v[3]}
	}
	return c
}

// perturbVariants returns k copies of img; copy i flips the low green bit on
// a 1/36 lattice with its own phase, so consecutive variants differ inside
// every 6x6 window (all ROIs) while staying visually identical.
func perturbVariants(img *image.RGBA, k int) []*image.RGBA {
	out := make([]*image.RGBA, k)
	b := img.Bounds()
	for i := range out {
		v := image.NewRGBA(b)
		copy(v.Pix, img.Pix)
		px, py := i%6, (i*5+1)%6
		for y := b.Min.Y + py; y < b.Max.Y; y += 6 {
			row := v.Pix[(y-b.Min.Y)*v.Stride:]
			for x := px; x < b.Dx(); x += 6 {
				row[x*4+1] ^= 1
			}
		}
		out[i] = v
	}
	return out
}

func perfConfig(tb testing.TB, sc perfScenario) config.Config {
	cfg := benchConfig(tb)
	cfg.Layout.Calibration = perfCalibration(tb, sc.calibration)
	return cfg
}

func perfNew(tb testing.TB, cfg config.Config) engine.TimedEngine {
	tb.Helper()
	eng, err := New(FromApp(cfg))
	if err != nil {
		tb.Fatal(err)
	}
	return eng.(engine.TimedEngine)
}

func perfEngine(tb testing.TB, sc perfScenario) (engine.TimedEngine, []*image.RGBA) {
	tb.Helper()
	cfg := perfConfig(tb, sc)
	frames := perturbVariants(perfLoad(tb, sc, cfg), perfVariants)
	return perfNew(tb, cfg), frames
}

var perfBase = time.Unix(1700000000, 0)

// BenchmarkFrames reports ns/op, B/op and allocs/op per analysed frame.
func BenchmarkFrames(b *testing.B) {
	for _, sc := range perfScenarios() {
		b.Run(sc.name, func(b *testing.B) {
			timed, frames := perfEngine(b, sc)
			at := perfBase
			// Warm-up: first full scans, template preparation and caches.
			for i := range perfVariants {
				timed.AnalyzeAt(frames[i], at)
				at = at.Add(perfFrameStep)
			}
			runtime.GC()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				timed.AnalyzeAt(frames[i%len(frames)], at)
				at = at.Add(perfFrameStep)
			}
		})
	}
}

// TestFrameFootprint prints heap and RSS after building the engine and after
// 200 frames. Run it alone in a fresh process (NARUTO_FOOTPRINT=1). Only one
// frame buffer is alive (perturbed in place, like a reused capture slot), so
// the numbers are engine + one frame; NARUTO_FOOTPRINT_RING=3 keeps three
// frame buffers like the live capture ring and NARUTO_FOOTPRINT_FRAMES sets
// the frame count. "retained" is HeapInuse after two GCs (pool victims
// flushed); VmHWM is the peak RSS; cpu is the process user+sys time of the
// frame loop (GOGC/GOMEMLIMIT studies).
func TestFrameFootprint(t *testing.T) {
	if os.Getenv("NARUTO_FOOTPRINT") == "" {
		t.Skip("set NARUTO_FOOTPRINT=1 (fresh process) to measure the footprint")
	}
	names := strings.Split(os.Getenv("NARUTO_FOOTPRINT_CASES"), ",")
	if names[0] == "" {
		names = []string{"real-mumu-1920x1080-1"}
	}
	byName := map[string]perfScenario{}
	for _, sc := range perfScenarios() {
		byName[sc.name] = sc
	}
	report := func(stage string) {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		inuse := ms.HeapInuse
		runtime.GC()
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		rss, hwm := procStatus("VmRSS"), procStatus("VmHWM")
		t.Logf("%-44s HeapInuse=%6.1f MiB retained=%6.1f MiB HeapSys=%6.1f MiB VmRSS=%6.1f MiB VmHWM=%6.1f MiB", stage,
			float64(inuse)/(1<<20), float64(after.HeapInuse)/(1<<20), float64(after.HeapSys)/(1<<20), rss/1024, hwm/1024)
	}
	report("start")
	for _, name := range names {
		sc, ok := byName[name]
		if !ok {
			t.Fatalf("unknown case %q", name)
		}
		cfg := perfConfig(t, sc)
		timed := perfNew(t, cfg)
		report(name + "/engine")
		ring := []*image.RGBA{perfLoad(t, sc, cfg)}
		if n, _ := strconv.Atoi(os.Getenv("NARUTO_FOOTPRINT_RING")); n > 1 {
			for range n - 1 {
				c := image.NewRGBA(ring[0].Rect)
				copy(c.Pix, ring[0].Pix)
				ring = append(ring, c)
			}
		}
		report(name + "/engine+frames")
		frames := 200
		if n, _ := strconv.Atoi(os.Getenv("NARUTO_FOOTPRINT_FRAMES")); n > 0 {
			frames = n
		}
		at := perfBase
		cpu := processCPU()
		for i := range frames {
			img := ring[i%len(ring)]
			perturbInPlace(img, (i/len(ring))%perfVariants)
			timed.AnalyzeAt(img, at)
			at = at.Add(perfFrameStep)
		}
		cpu = processCPU() - cpu
		report(fmt.Sprintf("%s/%d-frames", name, frames))
		t.Logf("%-44s cpu=%v (%.2f ms/frame) GOGC=%q GOMEMLIMIT=%q", name, cpu.Round(time.Millisecond), float64(cpu.Microseconds())/1000/float64(frames), os.Getenv("GOGC"), os.Getenv("GOMEMLIMIT"))
		runtime.KeepAlive(timed)
		runtime.KeepAlive(ring)
	}
}

// perturbInPlace flips the low green bit on lattice k (see perturbVariants):
// successive calls with different k always change every 6x6 window.
func perturbInPlace(img *image.RGBA, k int) {
	b := img.Bounds()
	px, py := k%6, (k*5+1)%6
	for y := b.Min.Y + py; y < b.Max.Y; y += 6 {
		row := img.Pix[(y-b.Min.Y)*img.Stride:]
		for x := px; x < b.Dx(); x += 6 {
			row[x*4+1] ^= 1
		}
	}
}

// procStatus returns a /proc/self/status value in kB (0 when unavailable).
func procStatus(key string) float64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, key+":"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				v, _ := strconv.ParseFloat(fields[0], 64)
				return v
			}
		}
	}
	return 0
}
