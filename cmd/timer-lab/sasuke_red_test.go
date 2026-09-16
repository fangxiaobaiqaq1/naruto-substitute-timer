package main

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

func TestSasukeRedScreenshotAndSyntheticDropThroughPipeline(t *testing.T) {
	src := purpleEvidence(t, "../../inbox/regressions/duel-sasuke-red-20260916/game.png")
	cfg := config.Default()
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cfg.Tracking.EnterFightFrames = 1
	cfg.Tracking.MinimumConfirmFrames = 2
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
	base := time.Unix(1700000000, 0)
	analyze := func(img *image.RGBA, ms int) frame.Frame {
		at := base.Add(time.Duration(ms) * time.Millisecond)
		return frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, at, at, "synthetic-red-mirror")
	}
	f := analyze(src, 0)
	if f.Err != nil || !f.Fighting || f.Hold || f.LayoutProfile != "duel" || f.LeftNinja != ninja.SasukeXiayin || f.RightNinja != ninja.SasukeXiayin {
		t.Fatalf("initial frame: %+v", f)
	}
	check := func(f frame.Frame, left, right int) {
		t.Helper()
		l, r := knownCount(f.Beads, 'L', f.LeftSlots), knownCount(f.Beads, 'R', f.RightSlots)
		if l == nil || r == nil || *l != left || *r != right {
			t.Fatalf("want %d/%d, got %+v", left, right, f.Beads)
		}
	}
	check(f, 2, 3)
	if events := tracker.observe(f); len(events) != 0 {
		t.Fatal("still image invented substitute")
	}

	// Synthetic name occlusion and a painted dark R3, not captured substitutes.
	covered := image.NewRGBA(src.Bounds())
	draw.Draw(covered, covered.Bounds(), src, src.Bounds().Min, draw.Src)
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	area, _ := layout.ContentArea(src)
	for _, p := range layout.PositionsIn("duel", area) {
		if p.Idx == 0 {
			draw.Draw(covered, ninja.NameRegion(image.Pt(p.X, p.Y), float64(area.W)/960, p.Side == "left"), image.Black, image.Point{}, draw.Src)
		}
	}
	gapped := analyze(covered, 20)
	if gapped.LeftNinja != "" || gapped.RightNinja != "" {
		t.Fatal("occluded name reused old identity")
	}
	check(gapped, 2, 3)
	if events := tracker.observe(gapped); len(events) != 0 {
		t.Fatal("name gap invented substitute")
	}
	for _, b := range f.Beads {
		if b.Label == "R3" {
			draw.Draw(covered, image.Rect(b.X-6, b.Y-7, b.X+7, b.Y+8), image.NewUniform(color.RGBA{40, 10, 15, 255}), image.Point{}, draw.Src)
		}
	}
	var events []replayEvent
	for _, ms := range []int{40, 60, 80, 100} {
		fr := analyze(covered, ms)
		fr.Duplicate = ms == 60 || ms == 100
		check(fr, 2, 2)
		got := tracker.observe(fr)
		if ms != 80 && len(got) != 0 {
			t.Fatalf("early/repeated confirmation at %d: %+v", ms, got)
		}
		events = append(events, got...)
	}
	if len(events) != 1 || events[0].side != "right" || !events[0].firstObserved.Equal(base.Add(40*time.Millisecond)) {
		t.Fatalf("wrong red substitute: %+v", events)
	}
	// Expired hints must not leave the shifted geometry or red classifier alive.
	expired := analyze(covered, 1200)
	if expired.RightNinja != "" || knownCount(expired.Beads, 'R', 4) != nil {
		t.Fatalf("expired hint admitted red row: %+v", expired)
	}
}
