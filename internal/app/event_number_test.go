package app

import (
	"math"
	"testing"
	"time"
)

func TestNewEventIsIndependentOfOldCooldownBoundary(t *testing.T) {
	for _, remaining := range []time.Duration{100 * time.Millisecond, 10 * time.Millisecond, -6 * time.Millisecond} {
		t.Run(remaining.String(), func(t *testing.T) {
			var c SideClock
			at := time.Unix(1700000000, 0)
			cd := 15 * time.Second
			c.Observe(4, true, at.Add(-16*time.Millisecond), cd, 2)
			c.Observe(3, true, at, cd, 2)
			c.Observe(3, true, at.Add(16*time.Millisecond), cd, 2)
			confirmed := at.Add(cd - remaining)
			first := confirmed.Add(-32 * time.Millisecond)
			c.Observe(2, true, first, cd, 2)
			if c.EventCount() != 1 {
				t.Fatal("unconfirmed drop incremented number")
			}
			c.Observe(2, true, confirmed, cd, 2)
			latest := c.LatestRemaining(confirmed)
			if c.EventCount() != 2 || len(latest) != 1 || math.Abs(latest[0]-14.968) > 1e-6 {
				t.Fatalf("new event hidden: count=%d latest=%v", c.EventCount(), latest)
			}
			stamp, serial := c.LastEvent()
			if !stamp.Equal(first) || serial != 2 {
				t.Fatalf("incorrect event identity: %s/%d", stamp, serial)
			}
			if c.EventCount() != 2 || len(c.LatestRemaining(confirmed.Add(16*time.Second))) != 0 {
				t.Fatal("expiry changed event number")
			}
			c.InvalidateObservation(confirmed.Add(17 * time.Second))
			c.ResyncObservation()
			c.SyncReady(2)
			if c.EventCount() != 2 {
				t.Fatal("observation interruption erased history")
			}
			c.Reset()
			if c.EventCount() != 0 || len(c.LatestRemaining(confirmed)) != 0 {
				t.Fatal("new match kept old displayed event")
			}
			_, after := c.LastEvent()
			if after != serial {
				t.Fatal("reset broke diagnostic serial")
			}
		})
	}
}

func TestLatestExpiryCannotRevealPreviousLongerClock(t *testing.T) {
	var c SideClock
	at := time.Unix(1700000000, 0)
	c.Observe(4, true, at, 15*time.Second, 1)
	c.Observe(3, true, at.Add(time.Second), 15*time.Second, 1)
	c.Observe(2, true, at.Add(2*time.Second), 10*time.Second, 1)
	if len(c.Remaining(at.Add(13*time.Second))) != 1 {
		t.Fatal("test requires a previous longer clock")
	}
	if len(c.LatestRemaining(at.Add(13*time.Second))) != 0 {
		t.Fatal("display returned to previous substitute")
	}
	if c.EventCount() != 2 {
		t.Fatal("expiry changed confirmed count")
	}
}

func TestEventNumberRejectsFlickerUnknownAndDuplicate(t *testing.T) {
	var c SideClock
	at := time.Unix(1700000000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, at, cd, 2)
	c.Observe(3, true, at.Add(16*time.Millisecond), cd, 2)
	c.Observe(3, true, at.Add(16*time.Millisecond), cd, 2)
	c.InvalidateObservation(at.Add(32 * time.Millisecond))
	c.Observe(3, true, at.Add(48*time.Millisecond), cd, 2)
	c.Observe(4, true, at.Add(64*time.Millisecond), cd, 2)
	if c.EventCount() != 0 {
		t.Fatal("unconfirmed evidence became an event")
	}
	c.Observe(3, true, at.Add(80*time.Millisecond), cd, 2)
	c.Observe(3, true, at.Add(96*time.Millisecond), cd, 2)
	if c.EventCount() != 1 {
		t.Fatal("confirmed event not counted exactly once")
	}
}
