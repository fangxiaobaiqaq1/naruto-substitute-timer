package frame

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"narutotimer/internal/capture"
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

func TestCaptureTimeoutKeepsRuntimeCallerBounded(t *testing.T) {
	if got := captureTimeout(1200); got != 1200*time.Millisecond {
		t.Fatalf("caller timeout = %s, want 1200ms", got)
	}
	if got := captureTimeout(0); got != 1200*time.Millisecond {
		t.Fatalf("default caller timeout = %s, want 1200ms", got)
	}
}

func TestCaptureMethodForConfiguredClientSeparatesMethodIdentityFromSourceLabel(t *testing.T) {
	leidianSource := "雷电 ADB · 127.0.0.1:5557 · 实例 2"
	mumuSource := "MuMu 实例 0 · E:\\Program Files\\Netease\\MuMu"
	for _, tc := range []struct {
		name, configuredMethod, source, want string
	}{
		{"Leidian ADB", capture.MethodLeidianADB, leidianSource, capture.MethodLeidianADB},
		{"MuMu SDK", capture.MethodMuMuSDK, mumuSource, capture.MethodMuMuSDK},
		{"unknown source unchanged", "unknown-method", "custom capture · target", "custom capture · target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := captureMethodForConfiguredClient(tc.configuredMethod, tc.source); got != tc.want {
				t.Fatalf("capture method = %q, want %q (configured=%q source=%q)", got, tc.want, tc.configuredMethod, tc.source)
			}
		})
	}
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

func TestSlowHelperDoesNotBlockCallerAndReapsBeforeRecovery(t *testing.T) {
	const observationTimeout = 20 * time.Millisecond
	started, canceled := make(chan struct{}), make(chan struct{})
	var calls, active, maxActive atomic.Int32
	p, closeFn := boundedProviderContextWithTimeout(func(ctx context.Context) Frame {
		call := calls.Add(1)
		if now := active.Add(1); now > maxActive.Load() {
			maxActive.Store(now)
		}
		defer active.Add(-1)
		if call == 1 {
			close(started)
			<-ctx.Done()
			close(canceled)
			return Frame{Hold: true, Err: ctx.Err(), HelperOutcome: "timeout", HelperReap: "killed_reaped"}
		}
		return Frame{Img: image.NewRGBA(image.Rect(0, 0, 2, 2)), HelperOutcome: "success", HelperReap: "completed"}
	}, nil, func() time.Duration { return observationTimeout }, func() time.Duration { return time.Second }, func() bool { return true })
	defer closeFn()
	at := time.Now()
	f := p()
	if elapsed := time.Since(at); elapsed > 100*time.Millisecond || !f.Hold || f.Err == nil || f.HelperOutcome != "timeout" || f.HelperReap != "pending" {
		t.Fatalf("caller waited for helper transaction: elapsed=%s frame=%+v", elapsed, f)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("helper did not start")
	}
	if busy := p(); busy.HelperOutcome != "busy" || busy.HelperOriginOutcome != "timeout" || busy.HelperTerminal != "pending" {
		t.Fatalf("busy frame lost origin transaction: %+v", busy)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("helper cancellation did not reach worker")
	}
	f = waitForDeliveredFrame(t, p)
	if f.Img == nil || f.Sequence != 1 || calls.Load() != 2 || maxActive.Load() != 1 {
		t.Fatalf("recovery overlapped or accepted stale frame: calls=%d active=%d frame=%+v", calls.Load(), maxActive.Load(), f)
	}
}

func TestRevisionChangeCancelsActiveHelperWithoutOverlapAndRejectsOldEvidence(t *testing.T) {
	started, canceled, abort := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var cancelOnce sync.Once
	var calls, active, maxActive atomic.Int32
	p, closeFn := boundedProviderContextWithTimeout(func(ctx context.Context) Frame {
		calls.Add(1)
		if now := active.Add(1); now > maxActive.Load() {
			maxActive.Store(now)
		}
		defer active.Add(-1)
		close(started)
		select {
		case <-ctx.Done():
		case <-abort:
		}
		close(canceled)
		return Frame{Img: image.NewRGBA(image.Rect(0, 0, 2, 2)), CapturedAt: time.Now(), HelperOutcome: "sdk_worker", HelperReap: "killed_reaped"}
	}, nil, func() time.Duration { return time.Second }, func() time.Duration { return time.Second }, func() bool { cancelOnce.Do(func() { close(abort) }); return true })
	defer closeFn()
	result := make(chan Frame, 1)
	go func() { result <- p() }()
	<-started
	// This models selection's non-blocking lifecycle cancellation. The old
	// revision's returned frame is then discarded without retaining pixels.
	if f := discardChangedRevision(Frame{SourceRevision: 7, HelperOutcome: "timeout", HelperReap: "pending"}, 7); f.SourceRevision != 7 || f.Img != nil || f.Err == nil || !f.Hold {
		t.Fatalf("old revision became UI evidence: %+v", f)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("revision cancellation did not reach helper")
	}
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("cancelled helper did not return")
	}
	if calls.Load() != 1 || maxActive.Load() != 1 {
		t.Fatalf("revision cancellation overlapped helper calls=%d active=%d", calls.Load(), maxActive.Load())
	}
}

func TestDiscardChangedRevisionPreservesHelperCorrelationWithoutEvidence(t *testing.T) {
	original := Frame{SourceRevision: 4, Img: image.NewRGBA(image.Rect(0, 0, 2, 2)), CapturedAt: time.Now(), AnalysisStarted: time.Now(), AnalyzedAt: time.Now(), HelperOutcome: "timeout", HelperReap: "pending", HelperOriginOutcome: "timeout", HelperTerminal: "pending"}
	got := discardChangedRevision(original, 4)
	if got.SourceRevision != 4 || got.Err == nil || !got.Hold || got.Img != nil || !got.CapturedAt.IsZero() || !got.AnalysisStarted.IsZero() || !got.AnalyzedAt.IsZero() {
		t.Fatalf("revision discard retained stale evidence: %+v", got)
	}
	if got.HelperOutcome != "timeout" || got.HelperReap != "pending" || got.HelperOriginOutcome != "timeout" || got.HelperTerminal != "pending" {
		t.Fatalf("revision discard lost helper correlation: %+v", got)
	}
}

func TestTerminalReapIsDeliveredAsHeldDiagnosticBeforeNextRequest(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p, closeFn := boundedProviderContextWithTimeout(func(ctx context.Context) Frame {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(canceled)
			return Frame{Img: image.NewRGBA(image.Rect(0, 0, 2, 2)), CapturedAt: time.Now(), HelperOutcome: "timeout", HelperReap: "killed_reaped"}
		}
		return Frame{Img: image.NewRGBA(image.Rect(0, 0, 2, 2)), HelperOutcome: "success", HelperReap: "completed"}
	}, nil, func() time.Duration { return 20 * time.Millisecond }, func() time.Duration { return time.Second }, func() bool { return true })
	defer closeFn()
	if f := p(); f.HelperReap != "pending" || f.Err == nil {
		t.Fatalf("initial timeout = %+v", f)
	}
	<-started
	<-canceled
	deadline := time.Now().Add(time.Second)
	for {
		f := p()
		if f.HelperReap == "killed_reaped" {
			if !f.Hold || f.Err == nil || f.Img != nil || !f.CapturedAt.IsZero() || f.HelperTerminal != "killed_reaped" {
				t.Fatalf("terminal reap became valid evidence: %+v", f)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal reap was not emitted: %+v", f)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCloseCancelsHelperWorkerWithoutWaitingForTransactionDeadline(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	p, closeFn := boundedProviderContextWithTimeout(func(ctx context.Context) Frame {
		close(started)
		<-ctx.Done()
		close(canceled)
		return Frame{Hold: true, Err: ctx.Err(), HelperOutcome: "sdk_worker", HelperReap: "killed_reaped"}
	}, nil, func() time.Duration { return time.Second }, func() time.Duration { return time.Second }, func() bool { return true })
	result := make(chan Frame, 1)
	go func() { result <- p() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("helper did not start")
	}
	closeFn()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel helper worker")
	}
	select {
	case f := <-result:
		if f.Err == nil || f.Img != nil {
			t.Fatalf("closed worker produced a frame: %+v", f)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not release caller")
	}
}

func TestLegacyTimeoutDoesNotWaitForNoOpHelperHook(t *testing.T) {
	gate := make(chan struct{})
	p, closeFn := boundedProviderContext(func(context.Context) Frame {
		<-gate
		return Frame{Img: image.NewRGBA(image.Rect(0, 0, 2, 2))}
	}, nil, 20*time.Millisecond, func() bool { return false })
	defer func() { close(gate); closeFn() }()
	started := time.Now()
	f := p()
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond || f.Err == nil || !f.Hold {
		t.Fatalf("legacy timeout waited for in-process call: elapsed=%s frame=%+v", elapsed, f)
	}
}

func TestTerminatedCaptureHelperRecoversWithoutOverlapOrLateAcceptance(t *testing.T) {
	stalled, terminated := make(chan struct{}), make(chan struct{})
	var calls, active, maxActive atomic.Int32
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	p, closeFn := boundedProviderContext(func(ctx context.Context) Frame {
		call := calls.Add(1)
		if now := active.Add(1); now > maxActive.Load() {
			maxActive.Store(now)
		}
		defer active.Add(-1)
		if call == 1 {
			close(stalled)
			<-ctx.Done() // Models helper.Run waiting until CommandContext kills/reaps it.
			close(terminated)
			return Frame{Img: image.NewRGBA(image.Rect(0, 0, 2, 2))} // late/stale result
		}
		return Frame{Img: img}
	}, nil, 20*time.Millisecond, nil)
	defer closeFn()
	if f := p(); !f.Hold || f.Err == nil || f.Img != nil {
		t.Fatalf("stalled helper was accepted: %+v", f)
	}
	select {
	case <-stalled:
	case <-time.After(time.Second):
		t.Fatal("stalled helper did not start")
	}
	select {
	case <-terminated:
	case <-time.After(time.Second):
		t.Fatal("timed out helper was not terminated/reaped")
	}
	f := waitForDeliveredFrame(t, p)
	if f.Img == nil || f.Duplicate || f.Sequence != 1 {
		t.Fatalf("next helper frame was not fresh evidence: %+v", f)
	}
	if calls.Load() != 2 || maxActive.Load() != 1 {
		t.Fatalf("helper calls=%d maximum overlap=%d", calls.Load(), maxActive.Load())
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
