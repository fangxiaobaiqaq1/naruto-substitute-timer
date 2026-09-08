package frame

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"sync/atomic"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

type analysisProbe struct {
	entered, returned time.Time
}

func (p *analysisProbe) Analyze(*image.RGBA) engine.Result {
	p.entered = time.Now()
	p.returned = time.Now()
	return engine.Result{Name: "probe", Fighting: true}
}

type timedAnalysisProbe struct {
	analysisProbe
	acquired time.Time
}

func (p *timedAnalysisProbe) AnalyzeAt(img *image.RGBA, acquired time.Time) engine.Result {
	p.acquired = acquired
	return p.Analyze(img)
}

func TestAnalysisTimestampsBracketEngineOnly(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 250))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{100, 120, 130, 255}), image.Point{}, draw.Src)
	acquired := time.Now().Add(-time.Second)
	for _, timed := range []bool{false, true} {
		probe := &analysisProbe{}
		var eng engine.Engine = probe
		if timed {
			eng = &timedAnalysisProbe{}
			probe = &eng.(*timedAnalysisProbe).analysisProbe
		}
		f := AnalyzeImage(img, eng, detect.ModeStretch, acquired.Add(-time.Millisecond), acquired, "probe")
		if f.AnalysisStarted.IsZero() || f.AnalysisStarted.Before(acquired) || f.AnalysisStarted.After(probe.entered) || f.AnalyzedAt.Before(probe.returned) {
			t.Fatalf("timed=%v: analysis timestamps do not bracket the engine: %+v, probe=%+v", timed, f, probe)
		}
		if timed && !eng.(*timedAnalysisProbe).acquired.Equal(acquired) {
			t.Fatal("diagnostic timing changed detector acquisition time")
		}
	}
}

func TestRejectedImagesHaveNoAnalysisTiming(t *testing.T) {
	for _, img := range []*image.RGBA{nil, image.NewRGBA(image.Rect(0, 0, 10, 10)), image.NewRGBA(image.Rect(0, 0, 400, 250))} {
		probe := &analysisProbe{}
		started := time.Now()
		f := AnalyzeImage(img, probe, detect.ModeStretch, started, started.Add(time.Millisecond), "probe")
		if !f.Hold || !f.AnalysisStarted.IsZero() || !f.AnalyzedAt.IsZero() || !probe.entered.IsZero() {
			t.Fatalf("rejected image became an analysis sample: %+v", f)
		}
		if img == nil && !f.CapturedAt.IsZero() {
			t.Fatal("nil image claimed successful acquisition")
		}
	}
}

func TestCaptureConfigurationFailureRetainsAttemptWithoutSuccess(t *testing.T) {
	cfg := config.Config{}
	cfg.Capture.PreferredMethods = []string{"unsupported-test-method"}
	p, closeFn := NewConfiguredSnapshotter(&analysisProbe{}, cfg)
	defer closeFn()
	f := p()
	if f.Err == nil || f.CaptureStarted.IsZero() || f.CaptureMethod != "unsupported-test-method" || !f.CapturedAt.IsZero() || !f.AnalysisStarted.IsZero() || !f.AnalyzedAt.IsZero() {
		t.Fatalf("capture failure lost its attempt or fabricated completed stages: %+v", f)
	}
}

func TestTimedOutCaptureRetainsOnlyItsAttemptMetadata(t *testing.T) {
	release, cleaned := make(chan struct{}), make(chan struct{})
	var attempt atomic.Pointer[captureAttempt]
	p, closeFn := boundedProvider(func() Frame {
		attempt.Store(&captureAttempt{started: time.Now(), method: "test-sdk"})
		<-release
		return Frame{Img: image.NewRGBA(image.Rect(0, 0, 1, 1)), CapturedAt: time.Now()}
	}, func() { close(cleaned) }, 10*time.Millisecond, func() captureAttempt {
		if a := attempt.Load(); a != nil {
			return *a
		}
		return captureAttempt{}
	})
	defer func() { closeFn(); close(release); <-cleaned }()
	f := p()
	if f.Err == nil || !f.Hold || f.CaptureStarted.IsZero() || f.CaptureMethod != "test-sdk" || f.Img != nil || !f.CapturedAt.IsZero() || !f.AnalysisStarted.IsZero() || !f.AnalyzedAt.IsZero() {
		t.Fatalf("timeout lost capture context or claimed a completed frame: %+v", f)
	}
	if rejected := p(); rejected.CaptureMethod != "" || rejected.CaptureStarted.Before(f.CaptureStarted) || !rejected.CapturedAt.IsZero() {
		t.Fatalf("busy request reused the previous capture as a new observation: %+v", rejected)
	}
}

func TestTimedOutCaptureNeverStartsConcurrentNativeCalls(t *testing.T) {
	gate := make(chan struct{})
	cleaned := make(chan struct{})
	var calls atomic.Int32
	p, closeFn := boundedProvider(func() Frame { calls.Add(1); <-gate; return Frame{Sequence: 1} }, func() { close(cleaned) }, 10*time.Millisecond)
	if f := p(); !f.Hold || f.Err == nil {
		t.Fatalf("expected timeout: %+v", f)
	}
	for i := 0; i < 20; i++ {
		if f := p(); !f.Hold {
			t.Fatal("busy native call must not accept another observation")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("spawned %d calls", calls.Load())
	}
	closeFn()
	closeFn()
	close(gate)
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("SDK not released after in-flight call ended")
	}
}

func TestLateFramesDoNotAdvanceDeliveryOrDeduplication(t *testing.T) {
	firstGate, changedGate := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	a, b := image.NewRGBA(image.Rect(0, 0, 2, 2)), image.NewRGBA(image.Rect(0, 0, 2, 2))
	b.Pix[0] = 255
	p, closeFn := boundedProvider(func() Frame {
		switch calls.Add(1) {
		case 1:
			<-firstGate
		case 4:
			<-changedGate
			return Frame{Img: b}
		case 6:
			return Frame{Img: b}
		}
		return Frame{Img: a}
	}, nil, 20*time.Millisecond)
	defer closeFn()
	if f := p(); !f.Hold || f.Err == nil || f.Sequence != 0 {
		t.Fatalf("first call must time out without delivery: %+v", f)
	}
	close(firstGate)
	f := waitForDeliveredFrame(t, p)
	if f.Duplicate || f.Sequence != 1 {
		t.Fatalf("discarded initial frame became evidence: %+v", f)
	}
	if f := p(); !f.Duplicate || f.Sequence != 2 {
		t.Fatalf("identical delivered pixels must be marked duplicate: %+v", f)
	}
	if f := p(); !f.Hold || f.Err == nil || f.Sequence != 0 {
		t.Fatalf("changed late frame must time out: %+v", f)
	}
	close(changedGate)
	f = waitForDeliveredFrame(t, p)
	if !f.Duplicate || f.Sequence != 3 {
		t.Fatalf("late changed frame replaced the delivered baseline: %+v", f)
	}
	if f := p(); f.Duplicate || f.Sequence != 4 {
		t.Fatalf("newly delivered changed pixels must remain new evidence: %+v", f)
	}
}

func waitForDeliveredFrame(t *testing.T, p Provider) Frame {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f := p()
		if f.Err == nil && f.Img != nil {
			return f
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("capture did not recover after the native call completed")
	return Frame{}
}

func TestCloseReleasesCallerBeforeNativeCallAndCleansUpOnce(t *testing.T) {
	started, release, cleaned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls, cleanupCalls atomic.Int32
	p, closeFn := boundedProvider(func() Frame {
		calls.Add(1)
		close(started)
		<-release
		return Frame{Img: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	}, func() { cleanupCalls.Add(1); close(cleaned) }, time.Second)
	result := make(chan Frame, 1)
	go func() { result <- p() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("native call did not start")
	}
	if f := p(); !f.Hold || f.Err == nil {
		t.Fatal("concurrent caller must not queue a second native capture")
	}
	closeFn()
	closeFn()
	select {
	case f := <-result:
		if !f.Hold || f.Err == nil || f.Img != nil {
			t.Fatalf("closed provider delivered an observation: %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wake the waiting caller")
	}
	select {
	case <-cleaned:
		t.Fatal("cleanup ran while native capture was still using the SDK")
	default:
	}
	for range 10 {
		if f := p(); !f.Hold || f.Err == nil {
			t.Fatalf("closed provider accepted a request: %+v", f)
		}
	}
	close(release)
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("SDK not released after native completion")
	}
	if calls.Load() != 1 || cleanupCalls.Load() != 1 {
		t.Fatalf("native calls=%d, cleanup calls=%d", calls.Load(), cleanupCalls.Load())
	}
}

func TestDeduplicationUsesVisibleGeometryAndCaptureSource(t *testing.T) {
	base := image.NewRGBA(image.Rect(0, 0, 2, 2))
	padded := &image.RGBA{Pix: make([]byte, 20), Stride: 12, Rect: base.Rect}
	padded.Pix[8] = 100 // Row padding, not a visible pixel.
	reshaped := image.NewRGBA(image.Rect(0, 0, 4, 1))
	frames := []Frame{
		{Img: base, CaptureMethod: "sdk"},
		{Img: padded, CaptureMethod: "sdk"},
		{Img: reshaped, CaptureMethod: "sdk"},
		{Img: reshaped, CaptureMethod: "window"},
		{Img: base, CaptureMethod: "window", Err: fmt.Errorf("discarded")},
		{Img: reshaped, CaptureMethod: "window"},
	}
	i := 0
	p, closeFn := boundedProvider(func() Frame { f := frames[i]; i++; return f }, nil, time.Second)
	defer closeFn()
	wantDuplicate := []bool{false, true, false, false, false, true}
	wantSequence := []uint64{1, 2, 3, 4, 0, 5}
	for index := range frames {
		f := p()
		if f.Duplicate != wantDuplicate[index] || f.Sequence != wantSequence[index] {
			t.Fatalf("frame %d: duplicate=%v sequence=%d, want %v/%d", index, f.Duplicate, f.Sequence, wantDuplicate[index], wantSequence[index])
		}
	}
}
