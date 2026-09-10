package rgb

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"testing"
	"time"

	"narutotimer/internal/app"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

var warmTestColor = color.RGBA{240, 65, 18, 255}

func copyWarmFrame(src *image.RGBA) *image.RGBA {
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img
}

func whiteWarmSweep(img *image.RGBA, x, y int) {
	draw.Draw(img, image.Rect(x-5, y-1, x+6, y+1), image.White, image.Point{}, draw.Src)
}

// This is a controlled synthetic animation, not a recording of a game event.
// Each fresh frame goes through the real RGB sampler before feeding SideClock.
func TestWarmSweepSequenceRejectsFalseEventsAndKeepsRealDrop(t *testing.T) {
	area := detect.ContentArea{W: 960, H: 540}
	base := image.NewRGBA(image.Rect(0, 0, area.W, area.H))
	var positions []detect.BeadPosition
	for i := range 6 {
		p := detect.BeadPosition{Bead: detect.Bead{Side: "right", Idx: i}, X: 200 - 15*i, Y: 100}
		positions = append(positions, p)
		draw.Draw(base, image.Rect(p.X-3, p.Y-5, p.X+4, p.Y+6), image.NewUniform(warmTestColor), image.Point{}, draw.Src)
	}
	readouts := [2]ninja.Readout{{}, {Name: ninja.Madara, Slots: 6, Palette: ninja.Warm}}
	var clock app.SideClock
	clock.SetObservationGap(150 * time.Millisecond)
	start := time.Unix(1700000000, 0)
	step := 0
	feed := func(img *image.RGBA, wantReady, wantKnown int) time.Time {
		t.Helper()
		at := start.Add(time.Duration(step) * 20 * time.Millisecond)
		step++
		beads := sampleCalibratedSpecial(img, positions, area, config.Default().Vision, readouts)
		_, ready := countReady(beads)
		known := knownCount(beads)
		if ready != wantReady || known != wantKnown {
			t.Fatalf("frame %d: ready=%d known=%d, want %d/%d: %+v", step, ready, known, wantReady, wantKnown, beads)
		}
		if known == 6 {
			clock.Observe(ready, true, at, 15*time.Second, 2)
		} else {
			clock.InvalidateObservation(at)
		}
		return at
	}
	feed(base, 6, 6)
	for range 3 {
		for _, p := range positions {
			sweep := copyWarmFrame(base)
			whiteWarmSweep(sweep, p.X, p.Y)
			feed(sweep, 6, 6)
			feed(sweep, 6, 6)
			feed(base, 6, 6)
		}
	}
	last := positions[5]
	covered := copyWarmFrame(base)
	draw.Draw(covered, image.Rect(last.X-6, last.Y-8, last.X+7, last.Y+9), image.White, image.Point{}, draw.Src)
	feed(covered, 5, 5)
	feed(covered, 5, 5)
	feed(base, 6, 6)
	if clock.EventCount() != 0 {
		t.Fatalf("idle sweep/occlusion invented %d substitutes", clock.EventCount())
	}
	drop := copyWarmFrame(base)
	draw.Draw(drop, image.Rect(last.X-3, last.Y-5, last.X+4, last.Y+6), image.NewUniform(color.RGBA{28, 54, 98, 255}), image.Point{}, draw.Src)
	firstDrop := feed(drop, 5, 6)
	feed(drop, 5, 6)
	confirmedAt, _ := clock.LastEvent()
	if clock.EventCount() != 1 || !confirmedAt.Equal(firstDrop) {
		t.Fatalf("real 6->5->5 must create one event at first drop: count=%d at=%v want=%v", clock.EventCount(), confirmedAt, firstDrop)
	}
}

func TestWarmWhiteCoreRequiresFilledBoundedBodyAndSixSlotIdentity(t *testing.T) {
	area := detect.ContentArea{W: 960, H: 540}
	p := detect.BeadPosition{Bead: detect.Bead{Side: "right"}, X: 100, Y: 100}
	readouts := [2]ninja.Readout{{}, {Name: ninja.Madara, Slots: 6, Palette: ninja.Warm}}
	for _, kind := range []string{"white cover", "warm wash", "finite wash", "empty rim", "upper body only", "lower body only", "filled"} {
		t.Run(kind, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, area.W, area.H))
			switch kind {
			case "white cover":
				draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
			case "warm wash", "finite wash":
				draw.Draw(img, image.Rect(80, 80, 121, 121), image.NewUniform(warmTestColor), image.Point{}, draw.Src)
				if kind == "finite wash" {
					draw.Draw(img, image.Rect(80, 107, 121, 121), image.Black, image.Point{}, draw.Src)
				}
			case "empty rim":
				for y := -8; y <= 8; y++ {
					for x := -6; x <= 6; x++ {
						if detect.InBeadDiamond(0, 0, 12, 16, x, y) && !detect.InBeadDiamond(0, 0, 8, 12, x, y) {
							img.SetRGBA(p.X+x, p.Y+y, warmTestColor)
						}
					}
				}
			case "upper body only":
				draw.Draw(img, image.Rect(97, 95, 104, 99), image.NewUniform(warmTestColor), image.Point{}, draw.Src)
			case "lower body only":
				draw.Draw(img, image.Rect(97, 102, 104, 106), image.NewUniform(warmTestColor), image.Point{}, draw.Src)
			case "filled":
				draw.Draw(img, image.Rect(97, 95, 104, 106), image.NewUniform(warmTestColor), image.Point{}, draw.Src)
			}
			whiteWarmSweep(img, p.X, p.Y)
			bead := sampleCalibratedSpecial(img, []detect.BeadPosition{p}, area, config.Default().Vision, readouts)[0]
			if kind == "filled" {
				if !bead.Lit || bead.Unknown {
					t.Fatalf("filled current body lost: %+v", bead)
				}
				for _, untrusted := range []ninja.Readout{{}, {Name: ninja.Madara, Slots: 4, Palette: ninja.Warm}, {Name: ninja.Madara, Slots: 6, Palette: ninja.Warm, Unverified: true}} {
					bead = sampleCalibratedSpecial(img, []detect.BeadPosition{p}, area, config.Default().Vision, [2]ninja.Readout{{}, untrusted})[0]
					if !bead.Unknown {
						t.Fatalf("unverified/wrong topology supplied white votes: %+v -> %+v", untrusted, bead)
					}
				}
			} else if !bead.Unknown {
				t.Fatalf("%s supplied a bean vote: %+v", kind, bead)
			}
		})
	}
}

// User images are local evidence. The synthetic tests above remain runnable
// without distributing game screenshots; these tests additionally use the
// actual orange body and blue empty cores when the native images are present.
func TestNativeMadaraWarmBodySurvivesSyntheticSweep(t *testing.T) {
	fullPath := "../../../inbox/regressions/review-20260909/6.png"
	emptyPath := "../../../inbox/regressions/review-20260909/7.png"
	for _, path := range []string{fullPath, emptyPath} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Skip("local native Madara screenshots are not distributed")
		}
	}
	cfg := config.Default()
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	e.Prefer("camp")
	full := loadRGBA(t, fullPath)
	baseline := e.Analyze(full)
	_, count := countReady(baseline.Beads)
	if baseline.RightNinja != ninja.Madara || baseline.RightSlots != 6 || count != 6 {
		t.Fatalf("unexpected native fixture: %+v", baseline)
	}
	for _, p := range baseline.Beads {
		if p.Label[0] != 'R' {
			continue
		}
		img := copyWarmFrame(full)
		whiteWarmSweep(img, p.X, p.Y)
		got := e.Analyze(img)
		left, right := countReady(got.Beads)
		if left != 4 || right != 6 || knownCount(got.Beads) != 10 {
			t.Errorf("sweep on native %s: %+v", p.Label, got.Beads)
		}
	}
	empty := loadRGBA(t, emptyPath)
	for _, p := range baseline.Beads {
		if p.Label[0] == 'R' && p.Label[1] >= '3' {
			whiteWarmSweep(empty, p.X, p.Y)
		}
	}
	got := e.Analyze(empty)
	for _, bead := range got.Beads {
		if bead.Label[0] == 'R' && bead.Label[1] >= '3' && bead.Lit {
			t.Fatalf("native empty blue slot gained a white-glint vote: %+v", bead)
		}
	}
}
