package app

import (
	"testing"
	"time"
)

// Exercise the live 150ms observation gap rather than jumping directly over
// fifteen seconds (a jump intentionally resynchronizes instead of counting).
func TestImmediateSubstituteAcrossExpiryWithLiveCadence(t *testing.T) {
	for _, offset := range []time.Duration{-40 * time.Millisecond, 0, 40 * time.Millisecond} {
		t.Run(offset.String(), func(t *testing.T) {
			var c SideClock
			c.SetObservationGap(150 * time.Millisecond)
			base := time.Unix(1700000000, 0)
			observe := func(ms int, n int) {
				c.Observe(n, true, base.Add(time.Duration(ms)*time.Millisecond), 15*time.Second, 2)
			}
			observe(0, 4)
			observe(20, 3)
			observe(40, 3)
			next := 15020 + int(offset/time.Millisecond)
			for ms := 60; ms < next; ms += 20 {
				observe(ms, 3)
			}
			observe(next, 2)
			observe(next+20, 2)
			first, serial := c.LastEvent()
			if c.EventCount() != 2 || serial != 2 || !first.Equal(base.Add(time.Duration(next)*time.Millisecond)) {
				t.Fatalf("boundary swallowed event: %+v", c)
			}
			got := c.LatestRemaining(base.Add(time.Duration(next+20) * time.Millisecond))
			if len(got) != 1 || got[0] < 14.97 {
				t.Fatalf("latest clock hidden: %v", got)
			}
		})
	}
}
