package rgb

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/app"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

func TestRecordedRoundStartWashCannotFillEmptyBlueBead(t *testing.T) {
	for _, frame := range []int{179, 181, 182, 184} {
		src := loadRGBA(t, fmt.Sprintf("../../../inbox/regressions/round-start-wash-20260908/frame-%04d.png", frame))
		for _, width := range []int{800, 950, 999, 1280, 1600, 1920, 2560} {
			img := image.NewRGBA(image.Rect(0, 0, width, width*src.Bounds().Dy()/src.Bounds().Dx()))
			xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
			cfg := config.Default()
			e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
			e.Prefer("duel")
			got := e.Analyze(img)
			for _, b := range got.Beads {
				if b.Label == "L4" && b.Lit && !b.Unknown {
					t.Errorf("frame%d width%d: empty bead became available: %+v", frame, width, b)
				}
			}
			if frame == 179 || frame == 184 {
				l, r := countReady(got.Beads)
				if l != 3 || r != 1 || knownCount(got.Beads) != 8 {
					t.Errorf("clean frame%d width%d lost 3/1: %+v", frame, width, got.Beads)
				}
			}
		}
	}
}

// Isolate calibrated bead evidence from the scene gate: the 30fps encoded
// recording does not reproduce every SDK capture/scene decision. These are
// successive real frames, not manufactured bright/dark pixels or game input.
func TestRecordedBlueWashCannotInventRecoveryAndSubstitute(t *testing.T) {
	cfg := config.Default()
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	e.Prefer("duel")
	var c app.SideClock
	c.SetObservationGap(150 * time.Millisecond)
	at := time.Unix(1700000000, 0)
	for i := 179; i <= 185; i++ {
		img := loadRGBA(t, fmt.Sprintf("../../../inbox/regressions/round-start-wash-20260908/frame-%04d.png", i))
		now := at.Add(time.Duration(i-179) * time.Second / 30)
		got := e.AnalyzeAt(img, now)
		ready, known := 0, 0
		for _, b := range got.Beads {
			if b.Label[0] == 'L' && !b.Unknown {
				known++
				if b.Lit || b.Gold {
					ready++
				}
			}
		}
		if known != 4 {
			c.InvalidateObservation(now)
		} else {
			c.Observe(ready, true, now, 15*time.Second, 2)
		}
		t.Logf("frame%d ready=%d known=%d baseline=%d events=%d", i, ready, known, c.LastReady(), c.EventCount())
	}
	if c.EventCount() != 0 {
		t.Fatal("opening white wash created phantom 3->4->3 substitute")
	}
	// Explicit synthetic positive control AFTER the natural wash: a real dark
	// core replacing the third available bean must still start at first capture.
	img := loadRGBA(t, "../../../inbox/regressions/round-start-wash-20260908/frame-0185.png")
	for _, b := range e.Analyze(img).Beads {
		if b.Label == "L3" {
			draw.Draw(img, image.Rect(b.X-3, b.Y-4, b.X+4, b.Y+5), image.NewUniform(color.RGBA{20, 45, 80, 255}), image.Point{}, draw.Src)
		}
	}
	first := at.Add(220 * time.Millisecond)
	for j := range 2 {
		ready, known := 0, 0
		for _, b := range e.Analyze(img).Beads {
			if b.Label[0] == 'L' && !b.Unknown {
				known++
				if b.Lit || b.Gold {
					ready++
				}
			}
		}
		if known != 4 || ready != 2 {
			t.Fatalf("synthetic visible 3->2 was obscured: known=%d ready=%d", known, ready)
		}
		c.Observe(ready, true, first.Add(time.Duration(j)*20*time.Millisecond), 15*time.Second, 2)
	}
	if got, _ := c.LastEvent(); c.EventCount() != 1 || !got.Equal(first) {
		t.Fatal("real visible post-wash drop delayed or suppressed")
	}
}

func TestBlueBodyRejectsFlatWashWithoutIncreasingColorThreshold(t *testing.T) {
	cfg := config.Default().Vision
	img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left", Idx: 0}, X: 100, Y: 100}
	area := detect.ComputeContentArea(1600, 900, detect.ModeStretch)
	blue := image.NewUniform(color.RGBA{70, 190, 235, 255})
	draw.Draw(img, image.Rect(70, 85, 131, 116), blue, image.Point{}, draw.Src)
	if b := sampleCalibrated(img, []detect.BeadPosition{p}, area, cfg)[0]; !b.Unknown || b.Lit {
		t.Fatalf("flat blue wash voted: %+v", b)
	}
	draw.Draw(img, img.Bounds(), image.Black, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(96, 95, 105, 106), blue, image.Point{}, draw.Src)
	if b := sampleCalibrated(img, []detect.BeadPosition{p}, area, cfg)[0]; b.Unknown || !b.Lit {
		t.Fatalf("same-color bounded core rejected: %+v", b)
	}
}
