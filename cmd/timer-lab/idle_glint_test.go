package main

import (
	"fmt"
	"testing"
	"time"

	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

// Actual idle captures, not a simulated substitute. The scene and both bean
// rows must remain observable; NONE of these gold glints is an event.
func TestRecordedIdleGlintsKeepSceneAndDoNotStartTimer(t *testing.T) {
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
	for i := 0; i < 30; i++ {
		img, err := loadRGBA(fmt.Sprintf("../../inbox/regressions/camp-idle-glint-20260907/diagnostic-%02d.png", i%6+1))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Unix(1700000000, 0).Add(time.Duration(i) * 20 * time.Millisecond)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "recorded-idle-glint")
		l, r := knownCount(f.Beads, 'L', 4), knownCount(f.Beads, 'R', 4)
		if f.Err != nil || !f.Fighting || f.Hold || f.Scene != "fight" || f.LayoutProfile != "camp" || l == nil || r == nil || *l != 4 || *r != 2 {
			t.Fatalf("frame%d: %+v", i, f)
		}
		if got := tracker.observe(f); len(got) != 0 {
			t.Fatalf("idle glint started timer: %+v", got)
		}
	}
	if tracker.left.EventCount() != 0 || tracker.right.EventCount() != 0 {
		t.Fatal("idle animation created event")
	}
}
