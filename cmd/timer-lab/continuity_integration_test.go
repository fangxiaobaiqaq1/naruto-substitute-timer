package main

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
)

func TestMissingRoundMarkerStillConfirmsFromCurrentHUD(t *testing.T) {
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	src, err := loadRGBA("../../inbox/fight/duel_20260821.png")
	if err != nil {
		t.Fatal(err)
	}
	missing := image.NewRGBA(src.Bounds())
	draw.Draw(missing, missing.Bounds(), src, src.Bounds().Min, draw.Src)
	draw.Draw(missing, image.Rect(730, 10, 880, 72), image.NewUniform(color.RGBA{70, 70, 70, 255}), image.Point{}, draw.Src)
	// Controlled mutation of the second left core: 2→1; the real fixture
	// supplies the rest of the HUD. This is not a labeled live-game event.
	draw.Draw(missing, image.Rect(192, 97, 205, 110), image.NewUniform(color.RGBA{28, 54, 98, 255}), image.Point{}, draw.Src)
	fresh := image.NewRGBA(missing.Bounds())
	draw.Draw(fresh, fresh.Bounds(), missing, image.Point{}, draw.Src)
	fresh.SetRGBA(800, 500, color.RGBA{50, 60, 70, 255})
	at := time.Unix(1700000000, 0)
	tracker := newReplayTracker(cfg, 15*time.Second)
	for i, img := range []*image.RGBA{src, missing, missing, fresh} {
		now := at.Add(time.Duration(i) * 20 * time.Millisecond)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "test")
		f.Duplicate = i == 2
		if !f.Fighting || f.Hold || f.Scene != "fight" || f.LayoutProfile != "duel" {
			t.Fatalf("marker miss interrupted fresh HUD at %d: %+v", i, f)
		}
		events := tracker.observe(f)
		if i < 3 && len(events) > 0 {
			t.Fatalf("premature event %d: %+v", i, events)
		}
		if i == 3 && (len(events) != 1 || events[0].side != "left" || !events[0].firstObserved.Equal(at.Add(20*time.Millisecond))) {
			t.Fatalf("event lost behind marker: %+v", events)
		}
	}
	// The same control with unreadable beads cannot keep producing events.
	draw.Draw(fresh, image.Rect(140, 90, 300, 120), image.NewUniform(color.RGBA{255, 255, 255, 255}), image.Point{}, draw.Src)
	now := at.Add(80 * time.Millisecond)
	f := frame.AnalyzeImage(fresh, eng, factory.FromApp(cfg).Mode, now, now, "test")
	if f.Fighting || !f.Hold || f.Scene != "fight" {
		t.Fatalf("occlusion must hold context without observation: %+v", f)
	}
	if events := tracker.observe(f); len(events) > 0 {
		t.Fatalf("occlusion event: %+v", events)
	}
	now = at.Add(time.Second)
	f = frame.AnalyzeImage(fresh, eng, factory.FromApp(cfg).Mode, now, now, "test")
	if f.Scene != "fight" || f.Fighting || !f.Hold {
		t.Fatalf("remembered context must not authorize occluded samples: %+v", f)
	}
	if events := tracker.observe(f); len(events) > 0 || tracker.left.EventCount() != 1 {
		t.Fatalf("occlusion altered confirmed events: %+v count=%d", events, tracker.left.EventCount())
	}
}
