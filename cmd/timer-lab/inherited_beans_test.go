package main

import (
	"narutotimer/internal/frame"
	"testing"
	"time"
)

func TestReplaySwapInheritsBeansRejectingOneBrightReturnFrame(t *testing.T) {
	for _, after := range [][]int{{4, 3, 3}, {3, 2, 2}, {2, 2}} {
		cfg := replayTestConfig()
		cfg.Tracking.EnterFightFrames = 1
		tr := newReplayTracker(cfg, 15*time.Second)
		base := time.Unix(1700000000, 0)
		tr.observe(replayFight(base, 4, 4))
		tr.observe(replayFight(base.Add(20*time.Millisecond), 3, 4))
		tr.observe(replayFight(base.Add(40*time.Millisecond), 3, 4))
		tr.observe(frame.Frame{Hold: true, Scene: "vs", CapturedAt: base.Add(time.Second)})
		for i, n := range after {
			tr.observe(replayFight(base.Add(3*time.Second+time.Duration(i)*20*time.Millisecond), n, 4))
		}
		want := uint64(2)
		if after[0] != 3 {
			want = 1
		}
		if tr.left.EventCount() != want || tr.right.EventCount() != 0 {
			t.Errorf("return %v: left=%d right=%d want left=%d", after, tr.left.EventCount(), tr.right.EventCount(), want)
		}
	}
}
