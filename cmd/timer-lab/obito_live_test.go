package main

import (
	"image"
	"image/draw"
	"testing"
	"time"

	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

// The user supplied a window screenshot and the matching overlay showing 4/2.
// Remove only the visible MuMu toolbar and window border, without rescaling or
// composing a HUD. The SDK supplies game content, not these window decorations.
// This establishes the training-frame result, not the missing duel sequence.
func TestUserObitoRightTwoTrainingScreenshot(t *testing.T) {
	window, err := loadRGBA("../../inbox/regressions/camp-obito-right-two-20260908/window.png")
	if err != nil {
		t.Fatal(err)
	}
	region := image.Rect(1, 40, 951, 574)
	if window.Bounds() != image.Rect(0, 0, 952, 575) {
		t.Fatalf("fixture dimensions changed: %v", window.Bounds())
	}
	img := image.NewRGBA(image.Rectangle{Max: region.Size()})
	draw.Draw(img, img.Bounds(), window, region.Min, draw.Src)
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700000000, 0)
	tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
	for i := 0; i < 3; i++ {
		now := at.Add(time.Duration(i) * 33 * time.Millisecond)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "user-window-game-crop")
		f.Duplicate = i > 0
		left, right := knownCount(f.Beads, 'L', f.LeftSlots), knownCount(f.Beads, 'R', f.RightSlots)
		if f.Err != nil || !f.Fighting || f.Hold || f.Scene != "fight" || f.LayoutProfile != "camp" || f.RightNinja != ninja.Obito || f.LeftSlots != 4 || f.RightSlots != 4 || left == nil || right == nil || *left != 4 || *right != 2 {
			t.Fatalf("real training frame: %+v", f)
		}
		for _, bead := range f.Beads {
			if bead.Label == "R1" || bead.Label == "R2" {
				if !bead.Lit || bead.Dark || bead.Unknown {
					t.Fatalf("purple light bean: %+v", bead)
				}
			}
			if bead.Label == "R3" || bead.Label == "R4" {
				if bead.Lit || !bead.Dark || bead.Unknown {
					t.Fatalf("purple dark bean: %+v", bead)
				}
			}
		}
		if events := tracker.observe(f); len(events) != 0 {
			t.Fatalf("a starting two-bean state or repeated still image fabricated an event: %+v", events)
		}
	}
	if tracker.right.EventCount() != 0 {
		t.Fatal("starting bean count was interpreted as prior substitute events")
	}
}
