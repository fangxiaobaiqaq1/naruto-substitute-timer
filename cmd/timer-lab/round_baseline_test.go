package main

import (
	"fmt"
	"testing"
	"time"

	"narutotimer/internal/frame"
)

func TestReplayOpeningBaselineDoesNotBlockOtherSide(t *testing.T) {
	for _, visible := range []int{0, 1} {
		t.Run(fmt.Sprint(visible), func(t *testing.T) {
			tr := newReplayTracker(replayTestConfig(), 15*time.Second)
			base := time.Unix(1700000000, 0)
			opening := replayFight(base, 4, 4)
			opening.RoundOpening = true
			tr.observe(opening)
			var events []replayEvent
			for i, ready := range []int{4, 3, 3, 3, 3} {
				counts := []int{ready, ready}
				if i < 3 {
					counts[1-visible] = -1
				}
				f := replayFight(base.Add(420*time.Millisecond+time.Duration(i)*20*time.Millisecond), counts[0], counts[1])
				events = append(events, tr.observe(f)...)
			}
			if len(events) != 1 || events[0].side != []string{"left", "right"}[visible] || !events[0].firstObserved.Equal(base.Add(440*time.Millisecond)) {
				t.Fatalf("visible side was blocked or unknown side inferred a use: %+v", events)
			}
			if tr.roundBaseline != ([2]bool{}) {
				t.Fatal("baseline did not clear independently")
			}
		})
	}
}

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

func TestReplayOpeningMarkerSurvivesObscuredBeans(t *testing.T) {
	tr := newReplayTracker(replayTestConfig(), 15*time.Second)
	at := time.Unix(1_700_000_000, 0)
	tr.observe(replayFight(at, 4, 4))
	tr.observe(replayFight(at.Add(20*time.Millisecond), 3, 4))
	tr.observe(replayFight(at.Add(40*time.Millisecond), 3, 4))
	opening := replayFight(at.Add(time.Second), -1, 4)
	opening.Hold, opening.RoundOpening = true, true
	tr.observe(opening)
	opening = replayFight(at.Add(1020*time.Millisecond), 3, 4)
	opening.RoundOpening = true
	tr.observe(opening)
	if tr.left.EventCount() != 1 || tr.left.Active() || tr.left.LastReady() != 3 {
		t.Fatalf("obscured opening did not reset replay baseline: events=%d active=%v ready=%d", tr.left.EventCount(), tr.left.Active(), tr.left.LastReady())
	}
}

func TestReplayVerifiedRoundOpeningResetsAndDoesNotCountOpeningBeans(t *testing.T) {
	cfg := replayTestConfig()
	tr := newReplayTracker(cfg, 15*time.Second)
	at := time.Unix(1700000000, 0)
	// First round has one confirmed left substitute.
	tr.observe(replayFight(at, 4, 4))
	tr.observe(replayFight(at.Add(20*time.Millisecond), 3, 4))
	tr.observe(replayFight(at.Add(40*time.Millisecond), 3, 4))
	if tr.left.EventCount() != 1 {
		t.Fatal("setup event missing")
	}
	// The real new-round marker resets old clocks/counts and makes all opening
	// bean changes calibration-only until the marker has cleared.
	open := replayFight(at.Add(time.Second), 4, 4)
	open.RoundOpening = true
	tr.observe(open)
	open = replayFight(at.Add(1020*time.Millisecond), 3, 4)
	open.RoundOpening = true
	tr.observe(open)
	clear := replayFight(at.Add(1400*time.Millisecond), 3, 4)
	tr.observe(clear)
	if tr.left.EventCount() != 1 || tr.left.Active() || tr.left.LastReady() != 3 {
		t.Fatalf("opening did not clear prior-round clock without changing match count: events=%d active=%v ready=%d", tr.left.EventCount(), tr.left.Active(), tr.left.LastReady())
	}
	// A visible post-opening 3->2 must still start at its first capture.
	tr.observe(replayFight(at.Add(1420*time.Millisecond), 2, 4))
	events := tr.observe(replayFight(at.Add(1440*time.Millisecond), 2, 4))
	if len(events) != 1 || events[0].side != "left" || events[0].number != 2 || !events[0].firstObserved.Equal(at.Add(1420*time.Millisecond)) {
		t.Fatalf("real post-opening use was not counted correctly: %+v", events)
	}
}
