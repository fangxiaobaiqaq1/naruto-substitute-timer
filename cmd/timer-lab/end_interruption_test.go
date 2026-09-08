package main

import (
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"testing"
	"time"
)

func TestReplayUncertainAnimationBreaksExitVotes(t *testing.T) {
	cfg := config.Default()
	cfg.Tracking.EnterFightFrames = 1
	for _, gap := range []frame.Frame{{Hold: true}, {Scene: "vs", Hold: true}, {Scene: "result", Hold: true}} {
		tr := newReplayTracker(cfg, 15*time.Second)
		at := time.Unix(1700000000, 0)
		tr.observe(frame.Frame{Fighting: true, Scene: "fight", LayoutProfile: "duel", CapturedAt: at})
		for i := 1; i <= 20; i++ {
			f := frame.Frame{Scene: "result"}
			if i%2 == 0 {
				f = gap
			}
			f.CapturedAt = at.Add(time.Duration(i) * 20 * time.Millisecond)
			tr.observe(f)
		}
		if !tr.fighting {
			t.Fatalf("interrupted/duplicate result hits accumulated into exit: %+v", gap)
		}
	}
}

func TestReplayExitRejectsRepeatedTimestampButAcceptsStaticPage(t *testing.T) {
	cfg := config.Default()
	cfg.Tracking.EnterFightFrames = 1
	tr := newReplayTracker(cfg, 15*time.Second)
	at := time.Unix(1700000000, 0)
	tr.observe(frame.Frame{Fighting: true, Scene: "fight", CapturedAt: at})
	for range 20 {
		tr.observe(frame.Frame{Scene: "result", CapturedAt: at.Add(time.Millisecond)})
	}
	if !tr.fighting {
		t.Fatal("same captured frame reset match")
	}
	for i := 1; i <= 10; i++ {
		tr.observe(frame.Frame{Scene: "result", Duplicate: true, CapturedAt: at.Add(time.Duration(i+1) * 20 * time.Millisecond)})
	}
	if tr.fighting {
		t.Fatal("fresh captures of static result cannot exit")
	}
}
