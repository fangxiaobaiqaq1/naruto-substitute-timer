package app

import (
	"math"
	"testing"
	"time"
)

func TestAlternativeClockUsesSameConfirmedEvent(t *testing.T) {
	var clock SideClock
	now := time.Unix(1700000000, 0)
	if _, ok := clock.LatestRemainingFor(now, 10*time.Second); ok {
		t.Fatal("invented event")
	}
	clock.Observe(4, true, now, 15*time.Second, 2)
	clock.Observe(3, true, now.Add(16*time.Millisecond), 15*time.Second, 2)
	if _, ok := clock.LatestRemainingFor(now, 10*time.Second); ok {
		t.Fatal("candidate was not confirmed")
	}
	clock.Observe(3, true, now.Add(32*time.Millisecond), 15*time.Second, 2)
	for _, tc := range []struct {
		offset       time.Duration
		primary, alt float64
	}{
		{32 * time.Millisecond, 14.984, 9.984}, {10*time.Second + 16*time.Millisecond, 5, 0}, {15*time.Second + 16*time.Millisecond, 0, 0},
	} {
		primary, ok := clock.LatestRemainingFor(now.Add(tc.offset), 15*time.Second)
		alt, altOK := clock.LatestRemainingFor(now.Add(tc.offset), 10*time.Second)
		if !ok || !altOK || math.Abs(primary-tc.primary) > 1e-6 || math.Abs(alt-tc.alt) > 1e-6 || clock.EventCount() != 1 {
			t.Fatalf("offset=%v primary=%v alt=%v count=%v", tc.offset, primary, alt, clock.EventCount())
		}
	}
	// A fresh substitute replaces BOTH estimates, counted once.
	clock.Observe(2, true, now.Add(16*time.Second), 15*time.Second, 2)
	clock.Observe(2, true, now.Add(16*time.Second+16*time.Millisecond), 15*time.Second, 2)
	if alt, _ := clock.LatestRemainingFor(now.Add(16*time.Second+16*time.Millisecond), 10*time.Second); math.Abs(alt-9.984) > 1e-6 || clock.EventCount() != 2 {
		t.Fatalf("second event: %v/%d", alt, clock.EventCount())
	}
	clock.Reset()
	if _, ok := clock.LatestRemainingFor(now, 10*time.Second); ok {
		t.Fatal("reset revived old alternative")
	}
}
