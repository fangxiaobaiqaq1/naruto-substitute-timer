package ui

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
)

// User evidence: the timer pill covers the round label, but all eight beans and
// two independent battle controls remain visible. The supplied frame has 1/4.
// The 2 -> 1 sequence below is a controlled pixel edit, NOT a recorded match.
// Crucially every frame runs factory -> AnalyzeImage -> captureOnce -> SideClock;
// no Fighting/Hold flags or bean observations are injected to bypass the gate.
func TestOverlayCoveredRoundStillDeliversBeanDropToClock(t *testing.T) {
	f, err := os.Open("../../inbox/regressions/wide-overlay-beans-20261009.png")
	if os.IsNotExist(err) {
		t.Skip("local user screenshot not distributed")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	copyFrame := func() *image.RGBA {
		img := image.NewRGBA(src.Bounds())
		draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
		return img
	}
	before, drop, confirm := copyFrame(), copyFrame(), copyFrame()
	// Copy the first blue bean onto the second dark slot; retain actual outlines.
	draw.Draw(before, image.Rect(140, 44, 153, 63), src, image.Pt(127, 44), draw.Src)
	// Simulate a newly rendered frame, rather than supplying duplicate pixels.
	confirm.SetRGBA(500, 300, color.RGBA{80, 70, 60, 255})

	cfg := config.Default()
	cfg.UI.PlayerSide = "right" // The opponent and expected event are on the left.
	cfg.UI.AutoTextRecognition = false
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	mode, _ := detect.ParseMode(cfg.Layout.ContentMode)
	at := time.Unix(1700000000, 0)
	s := testSession(nil)
	s.cfg = cfg
	s.side = "right"
	for i, img := range []*image.RGBA{before, drop, confirm} {
		captured := at.Add(time.Duration(i) * 100 * time.Millisecond)
		fr := frame.AnalyzeImage(img, eng, mode, captured, captured, "overlay-regression")
		if fr.Hold || !fr.Fighting || fr.LayoutProfile != "duel" {
			t.Fatalf("frame %d blocked before timer: fight=%v hold=%v profile=%s status=%s", i, fr.Fighting, fr.Hold, fr.LayoutProfile, fr.Status)
		}
		left, right := countReady(fr.Beads)
		wantLeft := 1
		if i == 0 {
			wantLeft = 2
		}
		if left != wantLeft || right != 4 {
			t.Fatalf("frame %d actual pixel counts=%d/%d want=%d/4", i, left, right, wantLeft)
		}
		s.provider = func() frame.Frame { return fr }
		s.captureOnce()
		wantEvents := uint64(0)
		if i == 2 {
			wantEvents = 1
		}
		if s.left.EventCount() != wantEvents || s.right.EventCount() != 0 {
			t.Fatalf("frame %d events left=%d right=%d want=%d/0", i, s.left.EventCount(), s.right.EventCount(), wantEvents)
		}
	}
	firstDrop := at.Add(100 * time.Millisecond)
	if event, _ := s.left.LastEvent(); !event.Equal(firstDrop) {
		t.Fatalf("event timestamp=%s want acquisition time %s", event, firstDrop)
	}
	remaining := s.oppRemaining(at.Add(200 * time.Millisecond))
	if len(remaining) != 1 || remaining[0] < 14.89 || remaining[0] > 14.91 {
		t.Fatalf("opponent clock did not start at first drop: %v", remaining)
	}
}
