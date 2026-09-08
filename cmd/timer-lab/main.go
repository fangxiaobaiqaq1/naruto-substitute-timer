// timer-lab records rendered pixels and stage timings for repeatable diagnosis.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/win"
)

type sample struct {
	Index     int       `json:"index"`
	Image     string    `json:"image,omitempty"`
	Started   time.Time `json:"captureStarted"`
	Captured  time.Time `json:"capturedAt"`
	CaptureMS float64   `json:"captureMs"`
	OffsetMS  float64   `json:"offsetMs"`
	Duplicate bool      `json:"duplicate"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Error     string    `json:"error,omitempty"`
}

func main() {
	var err error
	if len(os.Args) < 2 {
		err = fmt.Errorf("usage: timer-lab capture [flags] | replay [flags] | observe [flags] | ocr -image <screenshot> [flags]")
	} else {
		switch os.Args[1] {
		case "capture":
			err = record(os.Args[2:])
		case "replay":
			err = replay(os.Args[2:])
		case "ocr":
			err = ocrInspect(os.Args[2:])
		case "observe":
			err = observe(os.Args[2:])
		default:
			err = fmt.Errorf("unknown command %q", os.Args[1])
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func record(args []string) error {
	f := flag.NewFlagSet("capture", flag.ContinueOnError)
	backend := f.String("backend", "printwindow", "printwindow or mumu")
	root := f.String("install-dir", "", "MuMu installation root (mumu backend)")
	dll := f.String("dll", "", "explicit MuMu screenshot DLL")
	instance := f.Int("instance", 0, "MuMu instance index")
	pkg := f.String("package", "", "optional running Android package display")
	duration := f.Duration("duration", 10*time.Second, "record duration")
	interval := f.Duration("interval", 33*time.Millisecond, "start-to-start target interval")
	timeout := f.Duration("timeout", 5*time.Second, "exit probe if native capture stops responding")
	out := f.String("out", "", "new output directory (required; never overwrites a recording)")
	recordImages := f.Bool("record", false, "save every frame; PNG writing is measured separately and may reduce sampling FPS")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *out == "" || *duration <= 0 || *interval < time.Millisecond || *timeout < time.Second {
		return fmt.Errorf("out required; duration positive; interval >=1ms; timeout >=1s")
	}
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		return fmt.Errorf("output directory must not exist: %s", *out)
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var progress atomic.Int64
	progress.Store(time.Now().UnixNano())
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if time.Since(time.Unix(0, progress.Load())) > *timeout {
					fmt.Fprintln(os.Stderr, "capture probe timed out; process exits without spawning more native calls")
					os.Exit(2)
				}
			}
		}
	}()
	var capture func() (*image.RGBA, error)
	switch *backend {
	case "mumu":
		c, err := mumu.Open(mumu.Options{InstallDir: *root, DLLPath: *dll, Instance: *instance, Package: *pkg})
		if err != nil {
			return err
		}
		defer c.Close()
		capture = c.Capture
	case "printwindow":
		wins := win.FindMuMu()
		if len(wins) == 0 {
			return fmt.Errorf("no MuMu window")
		}
		w := wins[0]
		capture = func() (*image.RGBA, error) { return win.CaptureClient(w.HWND) }
	default:
		return fmt.Errorf("unsupported backend %q", *backend)
	}
	log, err := os.Create(filepath.Join(*out, "frames.jsonl"))
	if err != nil {
		return err
	}
	defer log.Close()
	enc := json.NewEncoder(log)
	start := time.Now()
	var prev [32]byte
	var previousImage string
	var costs []float64
	attempts, errors, changed := 0, 0, 0
	var storage time.Duration
	for time.Since(start) < *duration {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		beg := time.Now()
		progress.Store(beg.UnixNano())
		img, err := capture()
		end := time.Now()
		if err == nil && (img == nil || img.Bounds().Dx() < 400 || img.Bounds().Dy() < 250) {
			err = fmt.Errorf("capture is not a usable game frame (minimum 400x250)")
		}
		progress.Store(end.UnixNano())
		attempts++
		s := sample{Index: attempts, Started: beg, Captured: end, CaptureMS: float64(end.Sub(beg)) / float64(time.Millisecond), OffsetMS: float64(end.Sub(start)) / float64(time.Millisecond)}
		if err != nil {
			s.Error = err.Error()
			errors++
		} else {
			s.Width, s.Height = img.Bounds().Dx(), img.Bounds().Dy()
			hash := sha256.Sum256(img.Pix)
			s.Duplicate = len(costs) > 0 && hash == prev
			prev = hash
			if !s.Duplicate {
				changed++
			}
			costs = append(costs, s.CaptureMS)
			if len(costs) == 1 || (*recordImages && !s.Duplicate) {
				s.Image = fmt.Sprintf("frame-%06d.png", s.Index)
				saveStart := time.Now()
				err = savePNG(filepath.Join(*out, s.Image), img)
				storage += time.Since(saveStart)
				if err != nil {
					return err
				}
				previousImage = s.Image
			} else if *recordImages {
				s.Image = previousImage
			}
		}
		if err := enc.Encode(s); err != nil {
			return err
		}
		progress.Store(time.Now().UnixNano())
		wait := *interval - time.Since(beg)
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
	elapsed := time.Since(start).Seconds()
	report := map[string]any{"backend": *backend, "attempts": attempts, "successful": len(costs), "errors": errors, "seconds": elapsed, "samplesPerSecond": float64(len(costs)) / elapsed, "changedImagesPerSecond": float64(changed) / elapsed, "captureP50Ms": percentile(costs, .5), "captureP95Ms": percentile(costs, .95), "captureP99Ms": percentile(costs, .99), "pngWriteMs": float64(storage) / float64(time.Millisecond), "sourceTimestampKnown": false, "note": "Capture duration is API+copy time, not game-to-display latency. Changed image rate is not rendered FPS. PNG recording can slow the probe."}
	b, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "summary.json"), b, 0644); err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func savePNG(path string, img image.Image) error {
	f, e := os.Create(path)
	if e != nil {
		return e
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	e = enc.Encode(f, img)
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	a := append([]float64(nil), values...)
	sort.Float64s(a)
	i := int(float64(len(a)-1)*p + .5)
	return a[i]
}
