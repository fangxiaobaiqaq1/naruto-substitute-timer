package rgb

import (
	"image"
	"image/color"
	"image/draw"
	"narutotimer/internal/app"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"testing"
	"time"
)

// Controlled synthetic gold wash over a REAL HUD, not a recorded game effect.
func TestReviewGoldWashDoesNotProduceSubstitute(t *testing.T) {
	cfg := config.Default()
	src := loadRGBA(t, "../../../inbox/regressions/round-start-wash-20260908/frame-0179.png")
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	area, _ := layout.ContentArea(img)
	pos := layout.PositionsIn("duel", area)
	first, last := pos[0], pos[3]
	draw.Draw(img, image.Rect(first.X-16, first.Y-16, last.X+17, last.Y+17), image.NewUniform(color.RGBA{255, 200, 0, 255}), image.Point{}, draw.Src)
	if goldGlintBody(img, last, 4, 5) {
		t.Fatal("setup must be a flat wash, not a gold body")
	}
	e := NewConfigured(layout, cfg.Vision)
	e.Prefer("duel")
	var clock app.SideClock
	clock.SetObservationGap(150 * time.Millisecond)
	at := time.Unix(1700000000, 0)
	for i, frame := range []*image.RGBA{src, img, img, src, src} {
		r := e.AnalyzeAt(frame, at.Add(time.Duration(i)*20*time.Millisecond))
		ready, known := 0, 0
		for _, b := range r.Beads {
			if b.Label[0] == 'L' && !b.Unknown {
				known++
				if b.Lit {
					ready++
				}
			}
		}
		t.Logf("frame%d known=%d ready=%d", i, known, ready)
		if known == 4 {
			clock.Observe(ready, true, at.Add(time.Duration(i)*20*time.Millisecond), 15*time.Second, 2)
		} else {
			clock.InvalidateObservation(at.Add(time.Duration(i) * 20 * time.Millisecond))
		}
	}
	if clock.EventCount() != 0 {
		t.Fatalf("flat gold overlay manufactured recovery/drop and %d false event(s)", clock.EventCount())
	}
	// Explicit synthetic positive control: a real, fully observed 3->2 after
	// the wash must still confirm at the first drop frame, without a grace ban.
	dropped := image.NewRGBA(src.Bounds())
	draw.Draw(dropped, dropped.Bounds(), src, src.Bounds().Min, draw.Src)
	p := pos[2]
	draw.Draw(dropped, image.Rect(p.X-4, p.Y-4, p.X+5, p.Y+5), image.NewUniform(color.RGBA{28, 54, 98, 255}), image.Point{}, draw.Src)
	for i := 5; i < 7; i++ {
		r := e.AnalyzeAt(dropped, at.Add(time.Duration(i)*20*time.Millisecond))
		ready, known := 0, 0
		for _, b := range r.Beads {
			if b.Label[0] == 'L' && !b.Unknown {
				known++
				if b.Lit {
					ready++
				}
			}
		}
		if known != 4 || ready != 2 {
			t.Fatalf("genuine drop not observed: %+v", r.Beads)
		}
		clock.Observe(ready, true, at.Add(time.Duration(i)*20*time.Millisecond), 15*time.Second, 2)
	}
	firstEvent, _ := clock.LastEvent()
	if clock.EventCount() != 1 || !firstEvent.Equal(at.Add(100*time.Millisecond)) {
		t.Fatalf("genuine drop delayed/lost: %d at %v", clock.EventCount(), firstEvent)
	}
}

func TestGoldBodyRejectsFlatAndHollowButKeepsBoundedCore(t *testing.T) {
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left"}, X: 100, Y: 100}
	area := detect.ContentArea{W: 1600, H: 900}
	for _, kind := range []string{"flat", "hollow", "bounded"} {
		img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
		draw.Draw(img, image.Rect(80, 80, 121, 121), image.NewUniform(color.RGBA{255, 220, 0, 255}), image.Point{}, draw.Src)
		if kind == "hollow" {
			draw.Draw(img, image.Rect(96, 92, 105, 109), image.NewUniform(color.RGBA{255, 210, 70, 255}), image.Point{}, draw.Src)
		}
		if kind == "bounded" {
			draw.Draw(img, img.Bounds(), image.Black, image.Point{}, draw.Src)
			draw.Draw(img, image.Rect(97, 96, 104, 105), image.NewUniform(color.RGBA{255, 220, 0, 255}), image.Point{}, draw.Src)
		}
		b := sampleCalibrated(img, []detect.BeadPosition{p}, area, config.Default().Vision)[0]
		if kind == "bounded" {
			if b.Unknown || !b.Gold {
				t.Fatalf("visible core rejected: %+v", b)
			}
		} else if !b.Unknown {
			t.Fatalf("%s overlay admitted: %+v", kind, b)
		}
	}
}
