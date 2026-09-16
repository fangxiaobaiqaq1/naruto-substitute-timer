package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

// Optional private regression. These are the 1061 decoded 30fps frames of the
// user's compressed desktop recording, cropped to (59,136)-(881,599), not SDK
// originals. Neither the video, usernames nor full screenshots are distributed.
func TestDuelVideoSeptember16(t *testing.T) {
	dir := os.Getenv("TIMER_DUEL_VIDEO_FRAMES")
	if dir == "" {
		dir = "../../tmp/video-20260916-1242/game"
	}
	if _, err := os.Stat(filepath.Join(dir, "frame-000000.png")); os.IsNotExist(err) {
		t.Skip("private video frames not distributed")
	}
	cfg := config.Default()
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
	base := time.Unix(1700000000, 0)
	var events []replayEvent
	unknown := 0
	for i := 0; i < 1061; i++ {
		img, err := loadRGBA(filepath.Join(dir, fmt.Sprintf("frame-%06d.png", i)))
		if err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Second / 30)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, at, at, "compressed-video")
		if f.Err != nil {
			t.Fatal(f.Err)
		}
		left, right := knownCount(f.Beads, 'L', f.LeftSlots), knownCount(f.Beads, 'R', f.RightSlots)
		if left == nil || right == nil {
			unknown++
		}
		// Manually checked stable windows, excluding recharge transitions.
		wantL, wantR := -1, -1
		switch {
		case i <= 80:
			wantL, wantR = 2, 2
		case i >= 530 && i < 710:
			wantL, wantR = 2, 4
		case i >= 710 && i <= 780:
			wantL, wantR = 1, 4
		case i >= 800:
			wantL, wantR = 2, 4
		}
		if (wantL >= 0 && left != nil && *left != wantL) || (wantR >= 0 && right != nil && *right != wantR) {
			t.Fatalf("frame %d wrong visible count %v/%v, want %d/%d", i, left, right, wantL, wantR)
		}
		// This entire interval was lost to the warm portrait glint before the
		// fix; it must stay observable across the second real left substitute.
		if i >= 540 && i <= 720 && left == nil {
			t.Fatalf("portrait glint lost left baseline at %d", i)
		}
		events = append(events, tracker.observe(f)...)
	}
	if unknown > 50 {
		t.Fatalf("unknown sides regressed: %d/1061 frames", unknown)
	}
	if len(events) != 3 {
		t.Fatalf("expected exactly three visible substitutes, got %+v", events)
	}
	for i, want := range []struct {
		side  string
		frame int
	}{{"right", 172}, {"left", 261}, {"left", 710}} {
		if events[i].side != want.side || !events[i].firstObserved.Equal(base.Add(time.Duration(want.frame)*time.Second/30)) {
			t.Fatalf("event %d: %+v, expected %+v", i, events[i], want)
		}
	}
	t.Logf("1061 frames, %d with an unknown side, 3 correctly timed substitutes", unknown)
}
