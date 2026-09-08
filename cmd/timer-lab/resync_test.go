package main

import (
	"errors"
	"image"
	"testing"
	"time"

	"narutotimer/internal/frame"
)

func TestReplaySourceChangesKeepCooldownAndResyncBaseline(t *testing.T) {
	oldImage := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	newImage := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for _, name := range []string{"resolution", "method", "both", "error then missing metadata", "unknown beads"} {
		t.Run(name, func(t *testing.T) {
			base := time.Unix(1700000000, 0)
			at := func(n int) time.Time { return base.Add(time.Duration(n) * 16 * time.Millisecond) }
			makeFrame := func(n, ready int, img *image.RGBA, method string) frame.Frame {
				f := replayFight(at(n), ready, ready)
				f.Img, f.CaptureMethod = img, method
				return f
			}
			tracker := newReplayTracker(replayTestConfig(), 15*time.Second)
			tracker.observe(makeFrame(0, 4, oldImage, "mumu-sdk"))
			tracker.observe(makeFrame(1, 3, oldImage, "mumu-sdk"))
			if events := tracker.observe(makeFrame(2, 3, oldImage, "mumu-sdk")); len(events) != 2 {
				t.Fatalf("setup: expected one cooldown per side, got %+v", events)
			}
			changedImage, changedMethod := newImage, "mumu-sdk"
			if name == "method" {
				changedImage = oldImage
			}
			if name == "method" || name == "both" {
				changedMethod = "printwindow"
			}
			changed := makeFrame(3, 2, changedImage, changedMethod)
			if name == "error then missing metadata" {
				changed.Err = errors.New("capture geometry is changing")
			}
			if name == "unknown beads" {
				changed.Beads[0].Unknown = true
				changed.Beads[4].Unknown = true
			}
			if events := tracker.observe(changed); len(events) != 0 {
				t.Fatalf("source change invented events: %+v", events)
			}
			if name == "error then missing metadata" {
				tracker.observe(frame.Frame{CapturedAt: at(4), Err: errors.New("capture unavailable")})
			}
			for _, n := range []int{5, 6} {
				if events := tracker.observe(makeFrame(n, 2, changedImage, changedMethod)); len(events) != 0 {
					t.Fatalf("new baseline invented events: %+v", events)
				}
			}
			if tracker.left.LastReady() != 2 || tracker.right.LastReady() != 2 || len(tracker.left.Remaining(at(6))) != 1 || len(tracker.right.Remaining(at(6))) != 1 {
				t.Fatal("source change lost an existing cooldown or failed to establish the new baseline")
			}
			if events := tracker.observe(makeFrame(7, 1, changedImage, changedMethod)); len(events) != 0 {
				t.Fatalf("post-change drop bypassed confirmation: %+v", events)
			}
			events := tracker.observe(makeFrame(8, 1, changedImage, changedMethod))
			if len(events) != 2 || events[0].side != "left" || events[1].side != "right" || !events[0].firstObserved.Equal(at(7)) || !events[1].firstObserved.Equal(at(7)) {
				t.Fatalf("post-change drop missing or delayed: %+v", events)
			}
			if len(tracker.left.Remaining(at(8))) != 2 || len(tracker.right.Remaining(at(8))) != 2 {
				t.Fatal("post-change drop failed to coexist with the previous cooldown")
			}
		})
	}
}
