//go:build ocrhost

// ocrcheck shows what the Windows OCR path reads on recorded frames, on a
// Linux host: the production engine (factory.New) wrapped by the live text
// engine (hudtext.New, as factory.NewLive does on Windows) with the Windows
// helper's row recognizer run in-process (internal/ocr local_host.go). It is
// the reference for the Android port (android/tools/ocrhost). Run from the
// repo root with an ONNX Runtime 1.27.0 shared library for this host:
//
//	go run -tags ocrhost ./android/tools/ocrcheck --lib /path/libonnxruntime.so.1.27.0 \
//	    [--names 白方小,…] [--interval 250ms] [--diagnose] <png or dir>...
//
// Frames are fed in name order at --interval apart (capture time). Each OCR
// job is allowed to finish before the next frame, so the output does not
// depend on this machine's speed. --diagnose also prints, for every frame with
// a duel/camp HUD layout (fighting or not), hudtext.Diagnose: the three views'
// raw text and the parsed titles.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/hudtext"
	"narutotimer/internal/identity"
	"narutotimer/internal/ocr"
)

// tracked counts requests so the driver can wait for the background job.
type tracked struct {
	ocr.Recognizer
	inflight atomic.Int32
	started  atomic.Int64
	mu       sync.Mutex
	elapsed  []time.Duration
}

func (t *tracked) PrefersColorRows() bool { return true }

func (t *tracked) ReadRegions(ctx context.Context, img image.Image, regions []image.Rectangle) ([]ocr.Line, error) {
	t.inflight.Add(1)
	t.started.Add(1)
	defer t.inflight.Add(-1)
	start := time.Now()
	lines, err := t.Recognizer.(ocr.RegionRecognizer).ReadRegions(ctx, img, regions)
	t.mu.Lock()
	t.elapsed = append(t.elapsed, time.Since(start)/time.Duration(max(1, len(regions))))
	t.mu.Unlock()
	return lines, err
}

func load(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img, nil
}

func expand(args []string) []string {
	var files []string
	for _, a := range args {
		if st, err := os.Stat(a); err == nil && st.IsDir() {
			ents, _ := os.ReadDir(a)
			var names []string
			for _, e := range ents {
				if strings.HasSuffix(e.Name(), ".png") {
					names = append(names, e.Name())
				}
			}
			sort.Strings(names)
			for _, n := range names {
				files = append(files, filepath.Join(a, n))
			}
		} else {
			files = append(files, a)
		}
	}
	return files
}

func main() {
	lib := flag.String("lib", "", "ONNX Runtime shared library (required)")
	model := flag.String("model", "internal/ocr/neuraldata/recognizer.onnx", "recognizer model")
	names := flag.String("names", "", "comma-separated 我的名字")
	interval := flag.Duration("interval", 250*time.Millisecond, "capture interval between frames")
	diagnose := flag.Bool("diagnose", false, "print hudtext.Diagnose for every fighting frame")
	flag.Parse()
	if *lib == "" || flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	data, err := os.ReadFile(*model)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	host, err := ocr.NewHost(*lib, data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	reader := &tracked{Recognizer: host}
	var mine []string
	for _, n := range strings.Split(*names, ",") {
		if n = strings.TrimSpace(n); n != "" {
			mine = append(mine, n)
		}
	}
	cfg := config.Default()
	cfg.UI.PlayerNames = mine
	cfg.UI.AutoTextRecognition = true
	identity.SetMineNames(mine)
	inner, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	live := hudtext.New(inner, cfg, reader, identity.MineNames)
	defer live.Close()
	base := time.Unix(1700000000, 0)
	for i, f := range expand(flag.Args()) {
		img, err := load(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "skip", f, err)
			continue
		}
		at := base.Add(time.Duration(i) * *interval)
		before := reader.started.Load()
		r := live.AnalyzeAt(img, at)
		// Let the background job (if any) finish before the next frame.
		time.Sleep(20 * time.Millisecond)
		for reader.inflight.Load() > 0 {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(10 * time.Millisecond)
		job := ""
		if reader.started.Load() != before {
			job = " +ocr"
		}
		fmt.Printf("%s\tscene=%s fight=%v unc=%v profile=%s side=%s mine=%s opp=%s text=%s L=%s R=%s Lc=%s Rc=%s%s\n",
			filepath.Base(f), r.Scene, r.Fighting, r.Uncertain, r.LayoutProfile, r.PlayerSide, r.PlayerName, r.OppName,
			r.TextStatus, r.LeftNinja, r.RightNinja, r.LeftNinjaCandidate, r.RightNinjaCandidate, job)
		if *diagnose && (r.LayoutProfile == "duel" || r.LayoutProfile == "camp") {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			report, err := hudtext.Diagnose(ctx, host, img, cfg, r.LayoutProfile)
			cancel()
			if err != nil {
				fmt.Printf("\tdiagnose: %v\n", err)
				continue
			}
			for side, views := range report.Variants {
				fmt.Printf("\t%c views=%q title=%+v\n", "LR"[side], views, report.Titles[side])
			}
		}
	}
	reader.mu.Lock()
	var total time.Duration
	for _, d := range reader.elapsed {
		total += d
	}
	if n := len(reader.elapsed); n > 0 {
		fmt.Fprintf(os.Stderr, "ocr jobs %d, mean %.1f ms per row\n", n, float64(total.Microseconds())/1000/float64(n))
	}
	reader.mu.Unlock()
}
