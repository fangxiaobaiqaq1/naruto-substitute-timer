package app

import (
	"testing"
	"time"
)

func TestNewRoundLowerInheritedCountIsBaselineNotSubstitute(t *testing.T) {
	c, at := inheritedClock()
	oldStart, serial := c.LastEvent()
	if !c.ResumeInheritedObservation(at.Add(3 * time.Second)) {
		t.Fatal("fresh round rejected")
	}
	// The hidden interval supplies no observed drop. Two lower return frames
	// prove the CURRENT count, not that a substitute happened in this round.
	for i := range 4 {
		c.Observe(2, true, at.Add(3*time.Second+time.Duration(i)*20*time.Millisecond), 15*time.Second, 2)
	}
	if c.EventCount() != 1 || c.LastReady() != 2 {
		t.Fatalf("round return fabricated substitute: count=%d ready=%d", c.EventCount(), c.LastReady())
	}
	if got, n := c.LastEvent(); !got.Equal(oldStart) || n != serial {
		t.Fatal("round restarted existing cooldown")
	}
	// A genuinely observed drop AFTER the new baseline is usable immediately,
	// with its original acquisition time, not after a fixed grace period.
	first := at.Add(3080 * time.Millisecond)
	c.Observe(1, true, first, 15*time.Second, 2)
	c.Observe(1, true, first.Add(20*time.Millisecond), 15*time.Second, 2)
	if got, _ := c.LastEvent(); c.EventCount() != 2 || !got.Equal(first) {
		t.Fatal("real post-baseline substitute lost")
	}
}

func TestReturningCountNeedsStableEvidenceNotSingleBrightFrame(t *testing.T) {
	for _, values := range [][]int{{4, 3, 3}, {2, 3, 3}, {2, 1, 1}, {0, 0}} {
		c, at := inheritedClock()
		c.ResumeInheritedObservation(at.Add(3 * time.Second))
		for i, n := range values {
			c.Observe(n, true, at.Add(3*time.Second+time.Duration(i)*20*time.Millisecond), 15*time.Second, 2)
		}
		if c.EventCount() != 1 {
			t.Fatalf("return %v added an event", values)
		}
	}
}

func TestRoundReturnCannotReviveExpiredClock(t *testing.T) {
	c, at := inheritedClock()
	origin, serial := c.LastEvent()
	c.ResumeInheritedObservation(at.Add(20 * time.Second))
	for i := range 3 {
		c.Observe(2, true, at.Add(20*time.Second+time.Duration(i)*20*time.Millisecond), 15*time.Second, 2)
	}
	if c.Active() || len(c.LatestRemaining(at.Add(21*time.Second))) != 0 || c.EventCount() != 1 {
		t.Fatal("new round revived an expired substitute")
	}
	if got, n := c.LastEvent(); n != serial || !got.Equal(origin) {
		t.Fatal("expired event origin changed")
	}
}

func TestUnknownAndOneFrameSettingCannotConfirmRoundDifference(t *testing.T) {
	c, at := inheritedClock()
	c.ResumeInheritedObservation(at.Add(3 * time.Second))
	c.Observe(2, true, at.Add(3*time.Second), 15*time.Second, 1)
	if c.LastReady() != 3 {
		t.Fatal("single lower return replaced inherited baseline")
	}
	c.InvalidateObservation(at.Add(3010 * time.Millisecond))
	c.Observe(2, true, at.Add(3020*time.Millisecond), 15*time.Second, 1)
	if c.LastReady() != 3 {
		t.Fatal("unknown frame failed to break baseline confirmation")
	}
	c.Observe(2, true, at.Add(3040*time.Millisecond), 15*time.Second, 1)
	if c.LastReady() != 2 || c.EventCount() != 1 {
		t.Fatal("return should only calibrate")
	}
	c.Observe(1, true, at.Add(3060*time.Millisecond), 15*time.Second, 1)
	if c.EventCount() != 2 {
		t.Fatal("round calibration changed configured post-return confirmation latency")
	}
}
