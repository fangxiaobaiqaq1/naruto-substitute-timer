package app

import (
	"testing"
	"time"
)

func TestCapturedDuplicatesKeepContinuityWithoutVoting(t *testing.T) {
	at := time.Unix(1700000000, 0)
	var c SideClock
	c.SetObservationGap(150 * time.Millisecond)
	if c.HoldObservation(2, at) {
		t.Fatal("a duplicate cannot create a baseline")
	}
	c.Observe(2, true, at, 15*time.Second, 2)
	for i := 1; i <= 64; i++ {
		if !c.HoldObservation(2, at.Add(time.Duration(i)*16*time.Millisecond)) {
			t.Fatalf("fresh repeated capture %d lost continuity", i)
		}
	}
	firstDrop := at.Add(1040 * time.Millisecond)
	c.Observe(1, true, firstDrop, 15*time.Second, 2)
	for i := 1; i <= 4; i++ {
		if !c.HoldObservation(1, firstDrop.Add(time.Duration(i)*16*time.Millisecond)) || c.EventCount() != 0 {
			t.Fatal("duplicate supplied a confirmation vote or lost the pending observation")
		}
	}
	if c.HoldObservation(2, firstDrop.Add(65*time.Millisecond)) || c.HoldObservation(1, firstDrop) || c.HoldObservation(1, time.Time{}) {
		t.Fatal("contradictory count, replayed time, or heartbeat accepted")
	}
	c.Observe(1, true, firstDrop.Add(80*time.Millisecond), 15*time.Second, 2)
	if event, _ := c.LastEvent(); c.EventCount() != 1 || !event.Equal(firstDrop) {
		t.Fatalf("fresh confirmation lost the first actual drop: events=%d time=%v", c.EventCount(), event)
	}
	if c.HoldObservation(1, firstDrop.Add(time.Second)) {
		t.Fatal("a duplicate bridged an expired capture gap")
	}
	c.Observe(0, true, firstDrop.Add(time.Second), 15*time.Second, 2)
	c.Observe(0, true, firstDrop.Add(1016*time.Millisecond), 15*time.Second, 2)
	if c.EventCount() != 1 {
		t.Fatal("capture blackout invented another event")
	}
}
