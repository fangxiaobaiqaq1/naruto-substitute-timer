package main

import (
	"testing"
	"time"

	"narutotimer/internal/frame"
)

func TestReplayNewRoundDifferenceIsNotObservedSubstitute(t *testing.T) {
	cfg := replayTestConfig()
	cfg.Tracking.EnterFightFrames = 1
	tr := newReplayTracker(cfg, 15*time.Second)
	at := time.Unix(1700000000, 0)
	tr.observe(replayFight(at, 4, 4))
	tr.observe(replayFight(at.Add(20*time.Millisecond), 4, 3))
	tr.observe(replayFight(at.Add(40*time.Millisecond), 4, 3))
	tr.observe(frame.Frame{Scene: "vs", Hold: true, CapturedAt: at.Add(time.Second)})
	for i := range 3 {
		if events := tr.observe(replayFight(at.Add(3*time.Second+time.Duration(i)*20*time.Millisecond), 4, 2)); len(events) != 0 {
			t.Fatalf("round difference emitted %+v", events)
		}
	}
	if tr.right.EventCount() != 1 || tr.right.LastReady() != 2 {
		t.Fatal("lost prior event/current count")
	}
}
