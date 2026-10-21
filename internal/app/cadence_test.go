package app

import (
	"testing"
	"time"
)

const testPoll = 16 * time.Millisecond

func feed(c *ObservationCadence, start time.Time, every time.Duration, n int) time.Time {
	at := start
	for i := 0; i < n; i++ {
		c.Next(at, testPoll)
		at = at.Add(every)
	}
	return at.Add(-every)
}

// GE-02: the bound for a frame comes from the cadence before it. A long
// interval must not widen its own tolerance (the old rule allowed about 9x).
func TestObservationGapComesFromCadenceBeforeTheFrame(t *testing.T) {
	var c ObservationCadence
	last := feed(&c, time.Unix(1_700_000_000, 0), 60*time.Millisecond, 40)
	if got := c.Next(last.Add(560*time.Millisecond), testPoll); got != 180*time.Millisecond {
		t.Fatalf("gap after 60 ms cadence = %s, want 3x cadence = 180ms", got)
	}
}

func TestObservationGapBounds(t *testing.T) {
	var c ObservationCadence
	if got := c.Next(time.Unix(1_700_000_000, 0), testPoll); got != 150*time.Millisecond {
		t.Fatalf("first frame gap = %s, want the 150ms floor", got)
	}
	if got := c.Next(time.Unix(1_700_000_000, 0).Add(10*time.Millisecond), 100*time.Millisecond); got != 300*time.Millisecond {
		t.Fatalf("gap with a 100ms poll interval = %s, want 3x poll", got)
	}
	var slow ObservationCadence
	last := feed(&slow, time.Unix(1_700_000_000, 0), 5*time.Second, 30)
	if got := slow.Next(last.Add(5*time.Second), testPoll); got != 8*time.Second {
		t.Fatalf("gap = %s, want the 8s ceiling", got)
	}
}

// A slow engine must still be learned: otherwise every frame would resync and
// a drop could never gather its confirmation frames.
func TestObservationGapFollowsSlowCadence(t *testing.T) {
	var c ObservationCadence
	last := feed(&c, time.Unix(1_700_000_000, 0), 20*time.Millisecond, 20)
	last = feed(&c, last.Add(400*time.Millisecond), 400*time.Millisecond, 20)
	if got := c.Next(last.Add(400*time.Millisecond), testPoll); got < 1100*time.Millisecond || got > 1200*time.Millisecond {
		t.Fatalf("gap after a 400 ms cadence = %s, want about 3x400ms", got)
	}
}

// A failed capture (no acquisition time) is not a cadence sample, and the
// interval spanning it is not one either: an outage must not inflate the
// tolerance for the frames after it.
func TestObservationGapIgnoresIntervalsAcrossFailedCaptures(t *testing.T) {
	var c ObservationCadence
	last := feed(&c, time.Unix(1_700_000_000, 0), 20*time.Millisecond, 30)
	c.Next(time.Time{}, testPoll) // capture error
	after := last.Add(2 * time.Second)
	if got := c.Next(after, testPoll); got != 150*time.Millisecond {
		t.Fatalf("gap for the frame after an outage = %s", got)
	}
	if got := c.Next(after.Add(20*time.Millisecond), testPoll); got != 150*time.Millisecond {
		t.Fatalf("outage inflated the next gap to %s", got)
	}
}

// Repeated or out-of-order timestamps are not intervals.
func TestObservationGapIgnoresStaleTimestamps(t *testing.T) {
	var c ObservationCadence
	last := feed(&c, time.Unix(1_700_000_000, 0), 20*time.Millisecond, 30)
	c.Next(last, testPoll)
	c.Next(last.Add(-time.Second), testPoll)
	if got := c.Next(last.Add(20*time.Millisecond), testPoll); got != 150*time.Millisecond {
		t.Fatalf("stale timestamps changed the gap: %s", got)
	}
}
