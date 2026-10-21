package main

import (
	"testing"
	"time"
)

// GE-02: the replay tracker uses the live session's cadence rule, not a fixed
// gap, so replays of slow or uneven recordings exercise the same bound.
func TestReplayObservationGapFollowsRecordedCadence(t *testing.T) {
	for _, tc := range []struct {
		name            string
		cadence, before time.Duration // blackout before the lower count
		events          int
	}{
		{"5 fps recording", 200 * time.Millisecond, 0, 1},
		{"60 ms cadence, 120 ms blackout", 60 * time.Millisecond, 120 * time.Millisecond, 1},
		{"60 ms cadence, 150 ms blackout", 60 * time.Millisecond, 150 * time.Millisecond, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newReplayTracker(replayTestConfig(), 15*time.Second)
			at := time.Unix(1_700_000_000, 0)
			for i := 0; i < 20; i++ {
				tracker.observe(replayFight(at, 4, 4))
				at = at.Add(tc.cadence)
			}
			at = at.Add(tc.before)
			events := 0
			for i := 0; i < 3; i++ {
				events += len(tracker.observe(replayFight(at, 4, 3)))
				at = at.Add(tc.cadence)
			}
			if events != tc.events {
				t.Fatalf("events=%d, want %d", events, tc.events)
			}
		})
	}
}
