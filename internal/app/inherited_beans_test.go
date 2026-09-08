package app

import (
	"testing"
	"time"
)

func inheritedClock() (SideClock, time.Time) {
	var c SideClock
	c.SetObservationGap(150 * time.Millisecond)
	at := time.Unix(1700000000, 0)
	c.Observe(4, true, at, 15*time.Second, 2)
	c.Observe(3, true, at.Add(20*time.Millisecond), 15*time.Second, 2)
	c.Observe(3, true, at.Add(40*time.Millisecond), 15*time.Second, 2)
	c.InvalidateObservation(at.Add(time.Second))
	return c, at
}

func TestInheritedReturnRequiresFreshEvidenceAndRetainsClock(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []int
		want   uint64
	}{
		{"bright glint", []int{4, 3, 3}, 1}, {"dark glint", []int{2, 3, 3}, 1},
		{"lower count across hidden boundary", []int{2, 2}, 1}, {"drop after visible return", []int{3, 2, 2}, 2},
		{"recovered then used", []int{4, 4, 3, 3}, 2}, {"multi bean cost", []int{0, 0}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, at := inheritedClock()
			if c.ResumeInheritedObservation(at.Add(40 * time.Millisecond)) {
				t.Fatal("stale return consumed resume")
			}
			if !c.ResumeInheritedObservation(at.Add(3 * time.Second)) {
				t.Fatal("fresh return rejected")
			}
			for i, n := range tc.values {
				c.Observe(n, true, at.Add(3*time.Second+time.Duration(i)*20*time.Millisecond), 15*time.Second, 2)
			}
			if c.EventCount() != tc.want {
				t.Fatalf("%v: events %d want %d", tc.values, c.EventCount(), tc.want)
			}
			if tc.want == 1 && !c.lastEventAt.Equal(at.Add(20*time.Millisecond)) {
				t.Fatal("swap restarted old timer")
			}
		})
	}
}

func TestInheritedReturnDoesNotOverrideGeometryOrNewGaps(t *testing.T) {
	for _, geometry := range []bool{false, true} {
		c, at := inheritedClock()
		if geometry {
			c.ResyncObservation()
		}
		c.ResumeInheritedObservation(at.Add(3 * time.Second))
		c.Observe(2, true, at.Add(3*time.Second), 15*time.Second, 2)
		next := at.Add(3020 * time.Millisecond)
		if !geometry {
			c.InvalidateObservation(at.Add(3100 * time.Millisecond))
			next = at.Add(4 * time.Second)
		}
		c.Observe(2, true, next, 15*time.Second, 2)
		c.Observe(2, true, next.Add(20*time.Millisecond), 15*time.Second, 2)
		if c.EventCount() != 1 {
			t.Fatal("geometry/gap inherited an invalid comparison")
		}
	}
}

func TestInheritedReturnEvenWithOneFrameSettingRejectsSingleGlint(t *testing.T) {
	c, at := inheritedClock()
	c.ResumeInheritedObservation(at.Add(3 * time.Second))
	c.Observe(2, true, at.Add(3*time.Second), 15*time.Second, 1)
	c.Observe(3, true, at.Add(3020*time.Millisecond), 15*time.Second, 1)
	if c.EventCount() != 1 {
		t.Fatal("single returning frame invented event")
	}
}
