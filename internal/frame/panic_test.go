package frame

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/safego"
)

// fanOutPanicEngine panics on one of its own helper goroutines, like an engine
// bug on an unusual frame in the rgb/scene/avatar fan-outs.
type fanOutPanicEngine struct{ calls int }

func (e *fanOutPanicEngine) Analyze(img *image.RGBA) engine.Result {
	return e.AnalyzeAt(img, time.Now())
}

func (e *fanOutPanicEngine) AnalyzeAt(*image.RGBA, time.Time) engine.Result {
	e.calls++
	var g safego.Group
	g.Go(func() {
		var rows []int
		_ = rows[e.calls+2] // index out of range on the helper goroutine
	})
	g.Wait()
	return engine.Result{Name: "unreachable", Fighting: true}
}

// callerPanicEngine panics on the analysing goroutine itself.
type callerPanicEngine struct{}

func (callerPanicEngine) Analyze(*image.RGBA) engine.Result { panic("makeslice: len out of range") }

func fightImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 400, 250))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{100, 120, 130, 255}), image.Point{}, draw.Src)
	return img
}

// GE-03: an engine panic, also on an engine-internal goroutine, becomes an
// error for that frame instead of ending the process; the stack goes to the
// crash log.
func TestAnalyzeImageTurnsEnginePanicIntoErrorFrame(t *testing.T) {
	var log bytes.Buffer
	safego.SetLog(&log)
	t.Cleanup(func() { safego.SetLog(nil) })
	for _, tc := range []struct {
		name string
		eng  engine.Engine
		want string
	}{
		{"helper goroutine", &fanOutPanicEngine{}, "index out of range"},
		{"analysing goroutine", callerPanicEngine{}, "makeslice"},
	} {
		img := fightImage()
		acquired := time.Now().Add(-time.Second)
		f := AnalyzeImage(img, tc.eng, detect.ModeStretch, acquired, acquired, "probe")
		if f.Err == nil || !f.Hold || !strings.Contains(f.Err.Error(), tc.want) || !strings.Contains(f.Err.Error(), "识别出错") {
			t.Fatalf("%s: frame err=%v hold=%v", tc.name, f.Err, f.Hold)
		}
		if f.Fighting || len(f.Beads) != 0 || f.Img != img || !f.CapturedAt.Equal(acquired) || f.CaptureMethod != "probe" {
			t.Fatalf("%s: error frame must keep the capture and carry no observation: %+v", tc.name, f)
		}
		var p *safego.PanicError
		if !errors.As(f.Err, &p) {
			t.Fatalf("%s: error does not carry the panic: %T", tc.name, f.Err)
		}
	}
	if !strings.Contains(log.String(), "index out of range") || !strings.Contains(log.String(), "panic_test.go") {
		t.Fatalf("crash log lacks the helper's stack:\n%s", log.String())
	}
}

// The worker-level guard covers the capture client and frame plumbing: the
// provider reports the panic for one request and keeps serving.
func TestBoundedProviderSurvivesCapturePanic(t *testing.T) {
	safego.SetLog(nil)
	calls := 0
	inner := func() Frame {
		calls++
		if calls == 1 {
			var m map[string]int
			m["capture"]++ // assignment to entry in nil map
		}
		return Frame{Img: fightImage(), CapturedAt: time.Now(), CaptureMethod: "probe"}
	}
	provider, closeFn := boundedProvider(inner, nil, time.Second)
	defer closeFn()
	first := provider()
	if first.Err == nil || !first.Hold || !strings.Contains(first.Err.Error(), "nil map") || !strings.Contains(first.Err.Error(), "采集出错") {
		t.Fatalf("first frame: err=%v hold=%v", first.Err, first.Hold)
	}
	second := provider()
	if second.Err != nil || second.Img == nil || second.Sequence != 1 {
		t.Fatalf("provider did not recover: err=%v sequence=%d", second.Err, second.Sequence)
	}
}
