package app

import (
	"math"
	"testing"
	"time"
)

func TestSideClockStartsFromCaptureTimestamp(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 1)
	c.Observe(3, true, now.Add(200*time.Millisecond), cd, 1)
	// 开钟用掉豆那一帧的时间戳。200ms 后看：15 − 0.2 = 14.8
	got := c.Remaining(now.Add(400 * time.Millisecond))
	if len(got) != 1 {
		t.Fatalf("drop 1 bead should start one timer, got %v", got)
	}
	if got[0] < 14.75 || got[0] > 14.85 {
		t.Fatalf("remaining = 15 − (now−capture) = 14.8, got %.3f", got[0])
	}
}

func TestFormatCDTenths(t *testing.T) {
	if got := FormatCD([]float64{14.59}); got != "14.5" {
		t.Fatalf("FormatCD = %s", got)
	}
	if got := FormatCD([]float64{14.00}); got != "14.0" {
		t.Fatalf("FormatCD exact = %s", got)
	}
	if got := FormatCD(nil); got != "—" {
		t.Fatalf("empty = %s", got)
	}
}

func TestNextTenthDelayWaitsForBoundary(t *testing.T) {
	d := NextTenthDelay(14.87)
	if d < 65*time.Millisecond || d > 80*time.Millisecond {
		t.Fatalf("14.87 should wait ~70ms to 14.8, got %s", d)
	}
}

func TestSideClockUltDoesNotStartTimer(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 1)
	c.Observe(0, true, now.Add(200*time.Millisecond), cd, 1)
	if c.Active() {
		t.Fatalf("drop 4 beads is ult, must not start timer")
	}
}

func TestSideClockIgnoresTwoOrThreeDrop(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 1)
	c.Observe(2, true, now.Add(200*time.Millisecond), cd, 1)
	if c.Active() {
		t.Fatalf("drop 2 beads must not start timer")
	}
}

func TestSideClockKeepsStateWhenNotFighting(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 1)
	c.Observe(3, true, now.Add(200*time.Millisecond), cd, 1)
	c.Observe(0, false, now.Add(600*time.Millisecond), cd, 1)
	if !c.Active() || c.LastReady() != 3 {
		t.Fatalf("lost fight frame must not reset clock, last=%d active=%v", c.LastReady(), c.Active())
	}
}

func TestSideClockStackedTimers(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 1)
	c.Observe(3, true, now.Add(time.Second), cd, 1)
	c.Observe(2, true, now.Add(2*time.Second), cd, 1)
	got := c.Remaining(now.Add(2 * time.Second))
	if len(got) != 2 {
		t.Fatalf("two substitute uses should stack two timers, got %v", got)
	}
}

func TestSideClockSingleFlickerDoesNotStart(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 2)
	c.Observe(3, true, now.Add(80*time.Millisecond), cd, 2)
	c.Observe(4, true, now.Add(160*time.Millisecond), cd, 2)
	if c.Active() {
		t.Fatalf("one flash of drop-1 must not start a clock, remaining=%v", c.Remaining(now.Add(160*time.Millisecond)))
	}
}

func TestSideClockConfirmUsesFirstCapture(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 2)
	first := now.Add(80 * time.Millisecond)
	c.Observe(3, true, first, cd, 2)
	c.Observe(3, true, now.Add(160*time.Millisecond), cd, 2)
	got := c.Remaining(now.Add(280 * time.Millisecond))
	if len(got) != 1 {
		t.Fatalf("confirmed drop-1 should start one timer, got %v", got)
	}
	// 剩余从第一次看到掉豆的时间戳算，确认帧不加进冷却。
	if got[0] < 14.79 || got[0] > 14.81 {
		t.Fatalf("remaining = 15 − 0.20 = 14.8, got %.3f", got[0])
	}
}

func TestSideClockFlickerDoesNotRestart(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 1)
	c.Observe(3, true, now.Add(200*time.Millisecond), cd, 1)
	first := c.Remaining(now.Add(200 * time.Millisecond))
	c.Observe(4, true, now.Add(400*time.Millisecond), cd, 1)
	c.Observe(3, true, now.Add(600*time.Millisecond), cd, 1)
	got := c.Remaining(now.Add(600 * time.Millisecond))
	if len(got) != 1 {
		t.Fatalf("flicker must not start a second clock, got %v", got)
	}
	// 仍从第一次掉豆的时间戳往下减，不能被拽回 15。
	if got[0] > first[0] {
		t.Fatalf("clock jumped backward: first=%.3f now=%.3f", first[0], got[0])
	}
}

func TestSideClockSyncReadyDoesNotStart(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 1)
	c.SyncReady(3)
	c.Observe(3, true, now.Add(200*time.Millisecond), cd, 1)
	if c.Active() {
		t.Fatalf("sync after scene change must not start a clock, remaining=%v", c.Remaining(now.Add(200*time.Millisecond)))
	}
}

func TestSideClockUnknownBreaksConfirmation(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	c.Observe(4, true, now, 15*time.Second, 2)
	c.Observe(3, true, now.Add(16*time.Millisecond), 15*time.Second, 2)
	c.InvalidateObservation(now.Add(32 * time.Millisecond))
	if c.Active() {
		t.Fatal("a single drop followed by an unknown frame must not start a timer")
	}
	c.Observe(3, true, now.Add(48*time.Millisecond), 15*time.Second, 2)
	if c.Active() {
		t.Fatal("confirmation must restart after the unknown frame")
	}
	c.Observe(3, true, now.Add(64*time.Millisecond), 15*time.Second, 2)
	got := c.Remaining(now.Add(64 * time.Millisecond))
	if len(got) != 1 || math.Abs(got[0]-14.984) > 0.0001 {
		t.Fatalf("timer must start at the first fresh drop after unknown, got %v", got)
	}
}

func TestSideClockRepeatedAndOutOfOrderFramesDoNotConfirm(t *testing.T) {
	for _, staleOffset := range []time.Duration{16 * time.Millisecond, 8 * time.Millisecond} {
		t.Run(staleOffset.String(), func(t *testing.T) {
			var c SideClock
			now := time.Unix(1_700_000_000, 0)
			c.Observe(4, true, now, 15*time.Second, 2)
			first := now.Add(16 * time.Millisecond)
			c.Observe(3, true, first, 15*time.Second, 2)
			for i := 0; i < 3; i++ {
				c.Observe(3, true, now.Add(staleOffset), 15*time.Second, 2)
			}
			if c.Active() || c.LastReady() != 4 {
				t.Fatalf("stale frames cannot confirm a drop, active=%v ready=%d", c.Active(), c.LastReady())
			}
			c.Observe(3, true, now.Add(32*time.Millisecond), 15*time.Second, 2)
			if !c.Active() || c.LastReady() != 3 {
				t.Fatal("a second new frame should confirm the drop")
			}
		})
	}
}

func TestSideClockRecoveryWhileAnotherCooldownIsActive(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 1)
	c.Observe(3, true, now.Add(time.Second), cd, 1)
	c.Observe(2, true, now.Add(2*time.Second), cd, 1)
	// The first bean recovers at t=16 while the second still cools until t=17.
	c.Observe(3, true, now.Add(16100*time.Millisecond), cd, 1)
	if c.LastReady() != 2 {
		t.Fatal("a single recovery frame could be a flash and must not raise the baseline")
	}
	c.Observe(3, true, now.Add(16200*time.Millisecond), cd, 1)
	if c.LastReady() != 3 {
		t.Fatalf("confirmed recovery must raise the baseline while another timer runs, got %d", c.LastReady())
	}
	c.Observe(2, true, now.Add(16300*time.Millisecond), cd, 1)
	got := c.Remaining(now.Add(16300 * time.Millisecond))
	if len(got) != 2 || math.Abs(got[0]-0.7) > 0.0001 || math.Abs(got[1]-15) > 0.0001 {
		t.Fatalf("using the recovered bean must start a new timer alongside the existing one, got %v", got)
	}
}

func TestSideClockLastBeanDropUsesFirstCapture(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	c.Observe(1, true, now, 15*time.Second, 2)
	c.Observe(0, true, now.Add(16*time.Millisecond), 15*time.Second, 2)
	c.Observe(0, true, now.Add(32*time.Millisecond), 15*time.Second, 2)
	got := c.Remaining(now.Add(32 * time.Millisecond))
	if len(got) != 1 || math.Abs(got[0]-14.984) > 0.0001 {
		t.Fatalf("1→0 must record a real first-capture timestamp, got %v", got)
	}
}

func TestSideClockRecoveryCandidateInterruptsDropCandidate(t *testing.T) {
	var c SideClock
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	c.Observe(4, true, now, cd, 2)
	c.Observe(3, true, now.Add(16*time.Millisecond), cd, 2)
	c.Observe(3, true, now.Add(32*time.Millisecond), cd, 2)
	c.Observe(2, true, now.Add(48*time.Millisecond), cd, 2)
	c.Observe(4, true, now.Add(64*time.Millisecond), cd, 2)
	c.Observe(2, true, now.Add(80*time.Millisecond), cd, 2)
	if got := c.Remaining(now.Add(80 * time.Millisecond)); len(got) != 1 {
		t.Fatalf("nonconsecutive drop observations must not start a second timer, got %v", got)
	}
}

func TestSideClockLongObservationGapResynchronizes(t *testing.T) {
	var c SideClock
	c.SetObservationGap(150 * time.Millisecond)
	now := time.Unix(1700000000, 0)
	c.Observe(4, true, now, 15*time.Second, 2)
	c.Observe(3, true, now.Add(16*time.Millisecond), 15*time.Second, 2)
	c.InvalidateObservation(now.Add(100 * time.Millisecond))
	// Restoring after a stopped/minimized source cannot backdate a new event.
	c.Observe(2, true, now.Add(2*time.Second), 15*time.Second, 2)
	c.Observe(2, true, now.Add(2016*time.Millisecond), 15*time.Second, 2)
	if c.Active() || c.LastReady() != 2 {
		t.Fatalf("gap invented event: %+v", c)
	}
}

func TestSideClockDiagnosticEventSerialSurvivesReset(t *testing.T) {
	var c SideClock
	now := time.Unix(1700000000, 0)
	for i := 0; i < 2; i++ {
		base := now.Add(time.Duration(i) * time.Second)
		c.Observe(4, true, base, 15*time.Second, 2)
		first := base.Add(16 * time.Millisecond)
		c.Observe(3, true, first, 15*time.Second, 2)
		c.Observe(3, true, base.Add(32*time.Millisecond), 15*time.Second, 2)
		at, serial := c.LastEvent()
		if serial != uint64(i+1) || !at.Equal(first) {
			t.Fatalf("event = %s / %d", at, serial)
		}
		c.Reset()
	}
}
