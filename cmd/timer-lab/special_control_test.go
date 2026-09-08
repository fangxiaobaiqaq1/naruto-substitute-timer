package main

import (
	"image"
	"image/color"
	"image/draw"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"testing"
	"time"
)

func TestSpecialAttackArtDoesNotPauseReadableHUD(t *testing.T) {
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	src, err := loadRGBA("../../inbox/regressions/camp-both-naruto-right-one-20260907.png")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700000000, 0)
	tracker := newReplayTracker(cfg, 15*time.Second)
	for i := 0; i < 3; i++ {
		img := image.NewRGBA(src.Bounds())
		draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
		if i > 0 {
			// Synthetic primary-marker occlusion: keep both original HUD rows
			// and Naruto's special attack art. No actual gameplay claim.
			draw.Draw(img, image.Rect(708, 12, 892, 101), image.NewUniform(color.RGBA{60, 65, 70, 255}), image.Point{}, draw.Src)
			draw.Draw(img, image.Rect(720, 780, 880, 895), image.NewUniform(color.RGBA{60, 65, 70, 255}), image.Point{}, draw.Src)
			// The old attack control is deliberately unavailable. Substitute
			// evidence is independent of its center artwork or pressed state.
			draw.Draw(img, image.Rect(1350, 670, 1590, 890), image.Black, image.Point{}, draw.Src)
		} else {
			// Synthetic 2->1 drop supplies an event to verify, not just a label.
			draw.Draw(img, image.Rect(1354, 89, 1379, 116), src, image.Pt(1379, 89), draw.Src)
		}
		img.SetRGBA(800, 500, color.RGBA{uint8(30 * i), 50, 70, 255})
		now := at.Add(time.Duration(i) * 20 * time.Millisecond)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "synthetic-special-control")
		if !f.Fighting || f.Hold || f.Scene != "fight" {
			t.Fatalf("frame %d: readable HUD paused fight=%v hold=%v scene=%s name=%s/%s beads=%+v", i, f.Fighting, f.Hold, f.Scene, f.LeftNinja, f.RightNinja, f.Beads)
		}
		tracker.observe(f)
	}
	if tracker.right.EventCount() != 1 || tracker.left.EventCount() != 0 {
		t.Fatalf("substitute control did not preserve actual bean-event path: right=%d left=%d", tracker.right.EventCount(), tracker.left.EventCount())
	}
	first, _ := tracker.right.LastEvent()
	if !first.Equal(at.Add(20 * time.Millisecond)) {
		t.Fatalf("event timestamp delayed: %v", first)
	}
}
