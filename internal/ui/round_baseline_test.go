package ui

import (
	"fmt"
	"testing"
	"time"

	"narutotimer/internal/frame"
)

func TestOpeningBaselineDoesNotBlockOtherSide(t *testing.T) {
	for _, visible := range []int{0, 1} {
		t.Run(fmt.Sprint(visible), func(t *testing.T) {
			base := time.Unix(1700000000, 0)
			var frames []frame.Frame
			for i, ready := range []int{4, 4, 3, 3, 3, 3, 3, 3} {
				f := frame.Frame{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(ready, ready), CapturedAt: base.Add(400*time.Millisecond + time.Duration(i)*20*time.Millisecond)}
				if i == 0 {
					f.RoundOpening, f.CapturedAt = true, base
				}
				// The other side is obscured until frame 4, then returns with a
				// lower count. That is its baseline, NOT a retrospectively guessed use.
				if i >= 1 && i <= 3 {
					for j := range f.Beads {
						if j/4 != visible {
							f.Beads[j].Unknown = true
						}
					}
				}
				frames = append(frames, f)
			}
			s := testSession(frames)
			s.cfg.Tracking.EnterFightFrames = 1
			for range frames {
				s.captureOnce()
			}
			clock, other := &s.left, &s.right
			if visible == 1 {
				clock, other = other, clock
			}
			first, _ := clock.LastEvent()
			if clock.EventCount() != 1 || other.EventCount() != 0 || !first.Equal(frames[2].CapturedAt) {
				t.Fatalf("events=%d/%d first=%v", clock.EventCount(), other.EventCount(), first)
			}
			if s.roundBaseline != ([2]bool{}) {
				t.Fatal("baseline did not clear independently")
			}
		})
	}
}

func TestOpponentRoundReturnDoesNotRestartClock(t *testing.T) {
	at := time.Unix(1700000000, 0)
	frames := []frame.Frame{
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 4), CapturedAt: at},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 3), CapturedAt: at.Add(20 * time.Millisecond)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 3), CapturedAt: at.Add(40 * time.Millisecond)},
		{Hold: true, Scene: "vs", CapturedAt: at.Add(time.Second)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 2), CapturedAt: at.Add(3 * time.Second)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 2), CapturedAt: at.Add(3020 * time.Millisecond)},
	}
	s := testSession(frames)
	for range frames {
		s.captureOnce()
	}
	first, _ := s.right.LastEvent()
	if s.right.EventCount() != 1 || !first.Equal(at.Add(20*time.Millisecond)) || s.right.LastReady() != 2 {
		t.Fatalf("opponent round return restarted timer: events=%d first=%v ready=%d", s.right.EventCount(), first, s.right.LastReady())
	}
}
