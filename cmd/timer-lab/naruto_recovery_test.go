package main

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"narutotimer/internal/engine"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

// This sequence is SYNTHETIC: a recorded 4/1 HUD supplies the real right-hand
// name/bean pixels; one lit bean is copied to R2, and the name is briefly hidden.
// It checks identity recovery -> calibrated beans -> event clock, NOT actual
// gameplay accuracy, natural substitution effects or display latency.
func TestRightNarutoDropAfterOneObscuredNameFrame(t *testing.T) {
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	source, err := loadRGBA("../../inbox/regressions/camp-both-naruto-right-one-20260907.png")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700000000, 0)
	tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
	var events []replayEvent
	for i := 0; i < 4; i++ {
		img := image.NewRGBA(source.Bounds())
		draw.Draw(img, img.Bounds(), source, source.Bounds().Min, draw.Src)
		if i == 0 {
			// R1 center=1391, R2=1366; preserve the calibrated coordinates.
			draw.Draw(img, image.Rect(1354, 89, 1379, 116), source, image.Pt(1379, 89), draw.Src)
		}
		if i == 1 {
			draw.Draw(img, image.Rect(1020, 20, 1400, 67), image.Black, image.Point{}, draw.Src)
		}
		img.SetRGBA(800, 500, color.RGBA{uint8(i * 30), 50, 70, 255})
		now := at.Add(time.Duration(i) * 16 * time.Millisecond)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "synthetic-right-naruto-recovery")
		if i != 1 {
			want := 1
			if i == 0 {
				want = 2
			}
			right := knownCount(f.Beads, 'R', 4)
			if f.RightNinja != ninja.Naruto || right == nil || *right != want || !f.Fighting || f.Hold {
				t.Fatalf("frame %d: name=%q right=%v fight=%v hold=%v beads=%+v", i, f.RightNinja, right, f.Fighting, f.Hold, f.Beads)
			}
		} else if f.RightNinja != "" || knownCount(f.Beads, 'R', 4) != nil {
			t.Fatal("hidden name supplied stale identity/beans")
		}
		events = append(events, tracker.observe(f)...)
	}
	if len(events) != 1 || tracker.right.EventCount() != 1 || tracker.left.EventCount() != 0 {
		t.Fatalf("right event missing/duplicated: %+v", events)
	}
	if !events[0].firstObserved.Equal(at.Add(32 * time.Millisecond)) {
		t.Fatalf("event time is not first CURRENT drop observation: %+v", events)
	}
}

func TestSixSlotNameGapDoesNotRemapToFourAndLoseDrop(t *testing.T) {
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
	at := time.Unix(1700000000, 0)
	for i := 0; i < 4; i++ {
		file, x, y := "hashirama-three", 81, 59
		if i == 0 {
			file, x, y = "hashirama-four", 83, 52
		}
		img := specialComposite(t, cfg, file, x, y, 960)
		if i == 1 {
			layout := engine.NewConfiguredLayout(factory.FromApp(cfg).Mode, cfg.Layout)
			area, _ := layout.ContentArea(img)
			first := layout.PositionsIn("duel", area)[0]
			region := ninja.NameRegion(image.Pt(first.X, first.Y), 1, true)
			draw.Draw(img, region, image.Black, image.Point{}, draw.Src)
		}
		img.SetRGBA(800, 500, color.RGBA{uint8(i * 30), 50, 70, 255})
		now := at.Add(time.Duration(i) * 16 * time.Millisecond)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "synthetic-six-slot-name-gap")
		if f.LeftSlots != 6 {
			t.Fatalf("frame %d: lost name remapped slots to %d", i, f.LeftSlots)
		}
		if i == 1 && (f.LeftNinja != "" || knownCount(f.Beads, 'L', 6) != nil) {
			t.Fatal("missing name supplied old bean votes")
		}
		tracker.observe(f)
	}
	if tracker.left.EventCount() != 1 {
		t.Fatalf("name gap erased event baseline: %d", tracker.left.EventCount())
	}
}
