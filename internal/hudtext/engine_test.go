package hudtext

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"sync"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"narutotimer/internal/ocr"
)

type fakeEngine struct {
	mu     sync.Mutex
	result engine.Result
}

func (f *fakeEngine) Analyze(*image.RGBA) engine.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.result
}
func (f *fakeEngine) set(result engine.Result) { f.mu.Lock(); f.result = result; f.mu.Unlock() }

type reply struct {
	lines []ocr.Line
	err   error
}
type fakeReader struct {
	called  chan struct{}
	replies chan reply
	closed  chan struct{}
	once    sync.Once
}

func (f *fakeReader) Read(ctx context.Context, _ image.Image) ([]ocr.Line, error) {
	select {
	case f.called <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-f.replies:
		return r.lines, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.closed:
		return nil, errors.New("closed")
	}
}
func (f *fakeReader) Close() error { f.once.Do(func() { close(f.closed) }); return nil }

func testTextEngine(t *testing.T) (*Engine, *fakeEngine, *fakeReader, *image.RGBA, []ocr.Line) {
	t.Helper()
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.AutoTextRecognition = true
	cfg.UI.PlayerNames = []string{"白方小"}
	inner := &fakeEngine{result: engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel"}}
	reader := &fakeReader{called: make(chan struct{}, 10), replies: make(chan reply, 10), closed: make(chan struct{})}
	e := New(inner, cfg, reader, nil)
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Error(err)
		}
	})
	img := realFrame(t, "../../inbox/regressions/duel-second-round-20260906.png")
	regions, _ := nameRegions(img, cfg.Layout, "duel")
	var strips [2]strip
	for i, roi := range regions {
		strips[i].img = img.SubImage(roi).(*image.RGBA)
	}
	sheet := buildSheet(strips)
	var lines []ocr.Line
	for _, row := range sheet.rows {
		text := "纲手[少女](对面账号)"
		if row.side == 1 {
			text = "山中井野(白方小)"
		}
		lines = append(lines, ocr.Line{Text: text, Words: []ocr.Word{{Text: text, X: 20, Y: float64(row.rect.Min.Y), Width: 200, Height: float64(row.rect.Dy())}}})
	}
	return e, inner, reader, img, lines
}

func awaitCall(t *testing.T, r *fakeReader) {
	t.Helper()
	select {
	case <-r.called:
	case <-time.After(3 * time.Second):
		t.Fatal("no background OCR request")
	}
}
func awaitResult(t *testing.T, e *Engine) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for len(e.results) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no background OCR response")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBackgroundOCRNeedsTwoResultsAndCurrentPixels(t *testing.T) {
	e, _, r, img, lines := testTextEngine(t)
	at := time.Unix(1700000000, 0)
	e.AnalyzeAt(img, at)
	awaitCall(t, r)
	r.replies <- reply{lines: lines}
	awaitResult(t, e)
	if got := e.AnalyzeAt(img, at.Add(time.Millisecond)); got.PlayerSide != "" || got.RightNinja != "" {
		t.Fatal("one frame prematurely confirmed OCR")
	}
	e.AnalyzeAt(img, at.Add(500*time.Millisecond))
	awaitCall(t, r)
	r.replies <- reply{lines: lines}
	awaitResult(t, e)
	got := e.AnalyzeAt(img, at.Add(501*time.Millisecond))
	if got.PlayerSide != "right" || got.PlayerName != "白方小" || got.RightNinja != "山中井野" || got.LeftNinja != "纲手[少女]" {
		t.Fatalf("consensus missing: %+v", got)
	}
	if got.LeftSlots != 0 || got.RightSlots != 0 {
		t.Fatal("OCR must not manufacture bean topology")
	}
	regions, _ := nameRegions(img, e.cfg.Layout, "duel")
	draw.Draw(img, regions[1], image.NewUniform(color.RGBA{10, 10, 10, 255}), image.Point{}, draw.Src)
	if got := e.AnalyzeAt(img, at.Add(502*time.Millisecond)); got.PlayerSide != "" || got.RightNinja != "" {
		t.Fatalf("old text survived changed pixels: %+v", got)
	}
}

func TestBlockedOCRDoesNotBlockOrAccumulateCaptureFrames(t *testing.T) {
	e, _, r, img, _ := testTextEngine(t)
	at := time.Unix(1700000000, 0)
	e.AnalyzeAt(img, at)
	awaitCall(t, r)
	finished := make(chan struct{})
	go func() {
		for i := range 30 {
			e.AnalyzeAt(img, at.Add(time.Duration(i+1)*20*time.Millisecond))
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("capture waited on blocked OCR")
	}
	if len(e.jobs) != 0 || len(r.called) != 0 {
		t.Fatal("backlog of screenshot jobs")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.closed:
	default:
		t.Fatal("helper not closed")
	}
}

func TestLateTextCannotCrossScenesOrDisable(t *testing.T) {
	for _, mode := range []string{"lobby", "disabled", "late", "geometry", "seek"} {
		t.Run(mode, func(t *testing.T) {
			e, inner, r, img, lines := testTextEngine(t)
			at := time.Unix(1700000000, 0)
			e.AnalyzeAt(img, at)
			awaitCall(t, r)
			next := at.Add(time.Millisecond)
			switch mode {
			case "lobby":
				inner.set(engine.Result{Scene: "lobby"})
				e.AnalyzeAt(img, next)
				inner.set(engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel"})
			case "disabled":
				e.SetEnabled(false)
			case "late":
				next = at.Add(3 * time.Second)
			case "geometry":
				img = image.NewRGBA(image.Rect(0, 0, 960, 540))
			case "seek":
				next = at.Add(-time.Second)
			}
			r.replies <- reply{lines: lines}
			awaitResult(t, e)
			if got := e.AnalyzeAt(img, next); got.PlayerSide != "" || got.RightNinja != "" {
				t.Fatalf("stale %s result accepted: %+v", mode, got)
			}
		})
	}
}

func TestExactAccountChangeAndTemplatePriority(t *testing.T) {
	e, inner, r, img, lines := testTextEngine(t)
	at := time.Unix(1700000000, 0)
	for i := range 2 {
		e.AnalyzeAt(img, at.Add(time.Duration(i)*500*time.Millisecond))
		awaitCall(t, r)
		r.replies <- reply{lines: lines}
		awaitResult(t, e)
		e.AnalyzeAt(img, at.Add(time.Duration(i)*500*time.Millisecond+time.Millisecond))
	}
	e.mu.Lock()
	e.names = func() []string { return []string{"白方小额外"} }
	e.mu.Unlock()
	if got := e.AnalyzeAt(img, at.Add(time.Second)); got.PlayerSide != "" {
		t.Fatal("partial/old account matched after settings change")
	}
	inner.set(engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel", PlayerSide: "left", PlayerName: "模板账号", RightNinja: "模板忍者"})
	if got := e.AnalyzeAt(img, at.Add(time.Second+time.Millisecond)); got.PlayerSide != "left" || got.PlayerName != "模板账号" || got.RightNinja != "模板忍者" {
		t.Fatal("OCR overrode template evidence")
	}
}

func TestOCRFailureBacksOffWithoutDisablingBeads(t *testing.T) {
	e, _, r, img, _ := testTextEngine(t)
	at := time.Unix(1700000000, 0)
	e.AnalyzeAt(img, at)
	awaitCall(t, r)
	r.replies <- reply{err: errors.New("no language")}
	awaitResult(t, e)
	got := e.AnalyzeAt(img, at.Add(time.Second))
	if !got.Fighting || got.TextStatus != "unavailable" || got.TextError == "" {
		t.Fatalf("bad fallback: %+v", got)
	}
	for i := range 20 {
		e.AnalyzeAt(img, at.Add(time.Second+time.Duration(i)*100*time.Millisecond))
	}
	if len(r.called) != 0 {
		t.Fatal("failed backend restarted every capture frame")
	}
}

func TestOCRFailureBackoffSurvivesSceneFlapping(t *testing.T) {
	e, inner, r, img, _ := testTextEngine(t)
	at := time.Unix(1700000000, 0)
	e.AnalyzeAt(img, at)
	awaitCall(t, r)
	r.replies <- reply{err: errors.New("no language")}
	awaitResult(t, e)
	e.AnalyzeAt(img, at.Add(time.Second))
	inner.set(engine.Result{Scene: "lobby"})
	e.AnalyzeAt(img, at.Add(1100*time.Millisecond))
	inner.set(engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel"})
	got := e.AnalyzeAt(img, at.Add(1200*time.Millisecond))
	if got.TextStatus != "unavailable" || got.TextError == "" {
		t.Fatalf("scene transition hid OCR backoff: %+v", got)
	}
	e.mu.Lock()
	busy := e.busy
	e.mu.Unlock()
	if busy || len(r.called) != 0 || len(e.jobs) != 0 {
		t.Fatal("scene transition restarted failed OCR before retry delay")
	}
	e.AnalyzeAt(img, at.Add(6*time.Second))
	awaitCall(t, r)
}

func TestOldSceneBackendFailureStillBacksOff(t *testing.T) {
	for _, consumeInLobby := range []bool{false, true} {
		t.Run(fmt.Sprint(consumeInLobby), func(t *testing.T) {
			e, inner, r, img, _ := testTextEngine(t)
			at := time.Unix(1700000000, 0)
			e.AnalyzeAt(img, at)
			awaitCall(t, r)
			inner.set(engine.Result{Scene: "lobby"})
			e.AnalyzeAt(img, at.Add(time.Millisecond))
			r.replies <- reply{err: errors.New("no language")}
			awaitResult(t, e)
			if consumeInLobby {
				e.AnalyzeAt(img, at.Add(2*time.Millisecond))
			}
			inner.set(engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel"})
			got := e.AnalyzeAt(img, at.Add(500*time.Millisecond))
			if got.TextStatus != "unavailable" || got.TextError == "" {
				t.Fatalf("old scene backend failure lost: %+v", got)
			}
			e.mu.Lock()
			busy := e.busy
			e.mu.Unlock()
			if busy {
				t.Fatal("restarted failed helper on scene change")
			}
		})
	}
}

func TestOCRStatusDoesNotOutliveItsEvidence(t *testing.T) {
	e, _, r, img, lines := testTextEngine(t)
	at := time.Unix(1700000000, 0)
	for i := range 2 {
		now := at.Add(time.Duration(i) * 500 * time.Millisecond)
		e.AnalyzeAt(img, now)
		awaitCall(t, r)
		r.replies <- reply{lines: lines}
		awaitResult(t, e)
		e.AnalyzeAt(img, now.Add(time.Millisecond))
	}
	regions, _ := nameRegions(img, e.cfg.Layout, "duel")
	for _, roi := range regions {
		draw.Draw(img, roi, image.NewUniform(color.RGBA{10, 10, 10, 255}), image.Point{}, draw.Src)
	}
	got := e.AnalyzeAt(img, at.Add(time.Second))
	if got.TextStatus == "ready" || got.TextStatus == "reading" || got.PlayerSide != "" {
		t.Fatalf("blank name strips still report verified/busy OCR: %+v", got)
	}
}

func TestOCRRetryDoesNotDisplayPreviousFailure(t *testing.T) {
	e, _, r, img, _ := testTextEngine(t)
	at := time.Unix(1700000000, 0)
	e.AnalyzeAt(img, at)
	awaitCall(t, r)
	r.replies <- reply{err: errors.New("temporary failure")}
	awaitResult(t, e)
	e.AnalyzeAt(img, at.Add(time.Second))
	got := e.AnalyzeAt(img, at.Add(6*time.Second))
	if got.TextStatus != "reading" || got.TextError != "" {
		t.Fatalf("retry is hidden behind the old failure: %+v", got)
	}
}
