package app

import (
	"math"
	"testing"
	"time"
)

// reuseScript drives one side at a fixed cadence: the opponent is out of beans
// (1 → 0 at 100 ms, so its only cooldown ends at 15.1 s) and keeps pressing
// substitute. Times are offsets from base.
type reuseScript struct {
	c    SideClock
	base time.Time
	at   time.Duration
	step time.Duration
	need int
}

func newReuseScript(need int) *reuseScript {
	s := &reuseScript{base: time.Unix(1_700_000_000, 0), step: 20 * time.Millisecond, need: need}
	s.frames(1, 5) // 0..80 ms
	s.frames(0, 3) // first substitute: first seen at 100 ms
	if s.c.EventCount() != 1 {
		panic("setup: the first substitute was not counted")
	}
	return s
}

func (s *reuseScript) frames(ready, n int) {
	for i := 0; i < n; i++ {
		s.c.Observe(ready, true, s.base.Add(s.at), 15*time.Second, s.need)
		s.at += s.step
	}
}

// until observes ready on every frame up to (not including) offset.
func (s *reuseScript) until(ready int, offset time.Duration) {
	for s.at < offset {
		s.frames(ready, 1)
	}
}

func (s *reuseScript) now() time.Time { return s.base.Add(s.at) }

// GE-01: a bean that recovers when its cooldown ends and is used again at
// once is lit for at most one frame. That +1 coincides with the expected
// recovery, so it is a recovery plus a new substitute, which starts its clock
// at the first frame after the flash.
func TestSideClockReuseRightAfterRecoveryIsCounted(t *testing.T) {
	for _, need := range []int{1, 2} {
		s := newReuseScript(need)
		s.until(0, 15100*time.Millisecond)
		s.frames(1, 1) // 15.10 s: recovered bean visible for one frame
		reuse := s.now()
		s.frames(0, 20) // 15.12 s: used again
		if got := s.c.EventCount(); got != 2 {
			t.Fatalf("need=%d: re-use right after recovery not counted: events=%d", need, got)
		}
		at, serial := s.c.LastEvent()
		if !at.Equal(reuse) || serial != 2 {
			t.Fatalf("need=%d: re-use must start at the first frame after the flash: at=%s serial=%d", need, at.Sub(s.base), serial)
		}
		got := s.c.LatestRemaining(s.now())
		want := 15 - s.now().Sub(reuse).Seconds()
		if len(got) != 1 || math.Abs(got[0]-want) > 1e-9 {
			t.Fatalf("need=%d: latest clock = %v, want %.3f", need, got, want)
		}
		if r := s.c.Remaining(s.now()); len(r) != 1 {
			t.Fatalf("need=%d: the recovered bean's old clock must not stay alongside the new one: %v", need, r)
		}
		if s.c.LastReady() != 0 {
			t.Fatalf("need=%d: baseline = %d, want 0", need, s.c.LastReady())
		}
	}
}

// The flash can come slightly before the computed end: the clock starts at the
// first frame that showed the drop, up to one capture interval after the press.
func TestSideClockReuseFlashBeforeComputedEnd(t *testing.T) {
	s := newReuseScript(2)
	s.until(0, 14960*time.Millisecond)
	s.frames(1, 1) // 14.96 s, 140 ms before the computed end
	reuse := s.now()
	s.frames(0, 3)
	if s.c.EventCount() != 2 {
		t.Fatalf("flash before the computed end not paired with it: events=%d", s.c.EventCount())
	}
	if at, _ := s.c.LastEvent(); !at.Equal(reuse) {
		t.Fatalf("event at %s, want %s", at.Sub(s.base), reuse.Sub(s.base))
	}
	if r := s.c.Remaining(s.now()); len(r) != 1 || r[0] < 14.9 {
		t.Fatalf("the old clock must be replaced by the new one: %v", r)
	}
}

// Away from an expected recovery a one-frame +1 is still a glint.
func TestSideClockOneFrameGlintAwayFromRecoveryIsIgnored(t *testing.T) {
	for _, flash := range []time.Duration{8 * time.Second, 14700 * time.Millisecond, 15500 * time.Millisecond} {
		s := newReuseScript(2)
		s.until(0, flash)
		s.frames(1, 1)
		s.frames(0, 10)
		if s.c.EventCount() != 1 || s.c.LastReady() != 0 {
			t.Fatalf("glint at %s became an event: events=%d ready=%d", flash, s.c.EventCount(), s.c.LastReady())
		}
	}
}

// An expected recovery pairs with at most one observation: once a recovery is
// confirmed (or inferred), a later glint near the same end is a glint.
func TestSideClockExpectedRecoveryIsUsedOnce(t *testing.T) {
	t.Run("confirmed", func(t *testing.T) {
		s := newReuseScript(2)
		s.until(0, 15100*time.Millisecond)
		s.frames(1, 2) // confirmed recovery
		s.frames(0, 2) // ordinary substitute: event 2
		s.frames(1, 1) // glint 180 ms after the end
		s.frames(0, 5)
		if s.c.EventCount() != 2 {
			t.Fatalf("glint reused an already recovered bean: events=%d", s.c.EventCount())
		}
	})
	t.Run("inferred", func(t *testing.T) {
		s := newReuseScript(2)
		s.until(0, 15100*time.Millisecond)
		s.frames(1, 1)
		s.frames(0, 2) // inferred re-use: event 2
		s.frames(1, 1) // glint 60 ms later
		s.frames(0, 5)
		if s.c.EventCount() != 2 {
			t.Fatalf("one recovery produced two re-uses: events=%d", s.c.EventCount())
		}
	})
}

// The bean stays lit for two frames: an ordinary confirmed recovery, then an
// ordinary drop. This worked before and must not change.
func TestSideClockTwoFrameRecoveryThenUse(t *testing.T) {
	s := newReuseScript(2)
	s.until(0, 15100*time.Millisecond)
	s.frames(1, 2)
	use := s.now()
	s.frames(0, 5)
	if at, serial := s.c.LastEvent(); s.c.EventCount() != 2 || serial != 2 || !at.Equal(use) {
		t.Fatalf("events=%d at=%s", s.c.EventCount(), at.Sub(s.base))
	}
}

// Any interruption between the flash and the re-use frame removes the pairing:
// the inference needs two consecutive fresh observations.
func TestSideClockReuseNeedsConsecutiveObservations(t *testing.T) {
	s := newReuseScript(2)
	s.until(0, 15100*time.Millisecond)
	s.frames(1, 1)
	s.c.InvalidateObservation(s.now())
	s.at += s.step
	s.frames(0, 5)
	if s.c.EventCount() != 1 {
		t.Fatalf("flash across an unknown frame was paired: events=%d", s.c.EventCount())
	}
}

// After a character swap the inherited baseline only recalibrates; a one-frame
// +1 must not turn into a substitute there either.
func TestSideClockReuseNotInferredAcrossInheritedReturn(t *testing.T) {
	s := newReuseScript(2)
	s.until(0, 15080*time.Millisecond)
	if !s.c.ResumeInheritedObservation(s.now()) {
		t.Fatal("resume rejected")
	}
	s.at += s.step
	s.frames(1, 1) // 15.10 s, first fresh frame after the swap
	s.frames(0, 5)
	if s.c.EventCount() != 1 {
		t.Fatalf("inherited return invented a substitute: events=%d", s.c.EventCount())
	}
}

// ResetRound drops the prior round's cooldowns, including one that has just
// ended, so they cannot pair with a flash in the new round.
func TestSideClockResetRoundForgetsExpectedRecoveries(t *testing.T) {
	s := newReuseScript(2)
	s.until(0, 15140*time.Millisecond) // the 15.10 s end has passed
	s.c.ResetRound()
	s.frames(0, 1) // new round baseline
	s.frames(1, 1) // 15.16 s
	s.frames(0, 5)
	if s.c.EventCount() != 1 {
		t.Fatalf("prior round's cooldown paired with a flash: events=%d", s.c.EventCount())
	}
}

// Reset (new match) forgets them as well.
func TestSideClockResetForgetsExpectedRecoveries(t *testing.T) {
	s := newReuseScript(2)
	s.until(0, 15140*time.Millisecond)
	s.c.Reset()
	s.frames(0, 1)
	s.frames(1, 1)
	s.frames(0, 5)
	if s.c.EventCount() != 0 {
		t.Fatalf("prior round's cooldown paired with a flash: events=%d", s.c.EventCount())
	}
}
