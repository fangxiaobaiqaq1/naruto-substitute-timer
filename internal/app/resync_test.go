package app

import (
	"math"
	"testing"
	"time"
)

func TestSideClockResyncKeepsCooldownAndRejectsOldConfirmation(t *testing.T) {
	var c SideClock
	base := time.Unix(1700000000, 0)
	at := func(n int) time.Time { return base.Add(time.Duration(n) * 16 * time.Millisecond) }
	observe := func(n, ready int) { c.Observe(ready, true, at(n), 15*time.Second, 2) }
	observe(0, 4)
	observe(1, 3)
	observe(2, 3)
	observe(3, 2) // This pending candidate belongs to the old coordinate system.
	c.ResyncObservation()
	observe(3, 0) // Replayed/out-of-order evidence must not become a baseline.
	if c.hasPrev {
		t.Fatal("resynchronization allowed the old timestamp to establish a baseline")
	}
	observe(4, 2)
	observe(5, 2)
	first, serial := c.LastEvent()
	remaining := c.Remaining(at(5))
	if serial != 1 || !first.Equal(at(1)) || c.LastReady() != 2 || len(remaining) != 1 || math.Abs(remaining[0]-14.936) > 0.0001 {
		t.Fatalf("resync changed an existing cooldown or confirmed an old candidate: serial=%d first=%s ready=%d remaining=%v", serial, first, c.LastReady(), remaining)
	}
	observe(6, 1)
	observe(7, 1)
	first, serial = c.LastEvent()
	if serial != 2 || !first.Equal(at(6)) || len(c.Remaining(at(7))) != 2 {
		t.Fatalf("real post-resync drop lost or timestamp shifted: serial=%d first=%s", serial, first)
	}
}
