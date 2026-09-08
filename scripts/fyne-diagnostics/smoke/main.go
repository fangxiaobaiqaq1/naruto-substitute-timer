// A native rendering integration check with synthetic frames. Its report is
// test evidence, never a benchmark of game capture or recognition latency.
package main

import (
	"flag"
	"fmt"
	"image"
	"os"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/widget"
	"narutotimer/internal/diagnostics"
	"narutotimer/internal/frame"
)

func main() {
	out := flag.String("out", "debug/native-draw-smoke", "synthetic test output directory")
	flag.Parse()
	a := app.NewWithID("narutotimer.diagnostics.smoke")
	w := a.NewWindow("计时器绘制链路自检")
	hook, ok := w.(interface {
		SetFrameDrawCallback(func(time.Time, time.Time))
	})
	if !ok {
		fmt.Fprintln(os.Stderr, "native draw callback missing; build this check with the Fyne overlay")
		os.Exit(1)
	}
	r, err := diagnostics.New(diagnostics.Options{Root: *out, RecordFrames: true,
		Config: map[string]string{"purpose": "synthetic native draw smoke check, not game latency"}})
	if err != nil {
		panic(err)
	}
	label := widget.NewLabel("正在检查原生绘制回调…")
	w.SetContent(label)
	w.Resize(fyne.NewSize(320, 100))
	var current, lastDrawn uint64 // accessed only from the Fyne main thread
	drawn := make(chan uint64, 3)
	done := make(chan struct{})
	var once sync.Once
	hook.SetFrameDrawCallback(func(started, completed time.Time) {
		if current == 0 {
			return
		}
		r.MarkUI([]uint64{current}, "drawing", started)
		r.MarkUI([]uint64{current}, "drawn", completed)
		if lastDrawn != current {
			lastDrawn = current
			drawn <- current
		}
		if r.Snapshot().CompleteFrames >= 3 {
			once.Do(func() { close(done) })
		}
	})
	go func() {
		for i := 1; i <= 3; i++ {
			time.Sleep(120 * time.Millisecond)
			at := time.Now()
			f := frame.Frame{Img: image.NewRGBA(image.Rect(0, 0, 400, 250)), Sequence: uint64(i),
				CaptureStarted: at, CapturedAt: at, AnalysisStarted: at, AnalyzedAt: at,
				CaptureMethod: "synthetic-smoke", Scene: "fight", Fighting: true}
			id := r.Observe(f, time.Now())
			r.MarkUI([]uint64{id}, "queued", time.Now())
			fyne.Do(func() {
				current = id
				label.SetText(fmt.Sprintf("原生绘制检查 %d/3", id))
				r.MarkUI([]uint64{id}, "applied", time.Now())
			})
			// Startup/font loading may legitimately coalesce several UI posts.
			// Wait for each actual draw before submitting the next test frame.
			select {
			case <-drawn:
			case <-time.After(10 * time.Second):
				return
			}
		}
	}()
	go func() {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
		}
		if err := r.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		fyne.Do(a.Quit)
	}()
	w.ShowAndRun()
	_ = r.Close()
	v := r.Snapshot()
	fmt.Printf("synthetic native draw smoke: frames=%d recorded=%d output=%s\n", v.CompleteFrames, v.RecordedFrames, v.Directory)
	if v.CompleteFrames != 3 || v.RecordedFrames != 3 || v.LastError != "" {
		os.Exit(1)
	}
}
