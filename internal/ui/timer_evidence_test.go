package ui

import (
	"testing"
	"time"

	"narutotimer/internal/frame"
)

// evidenceSeq builds frames for session.captureOnce like a live MuMu capture:
// successful frames carry the client's Source() as CaptureMethod.
type evidenceSeq struct {
	base   time.Time
	at     time.Duration
	step   time.Duration
	method string
	frames []frame.Frame
}

func newEvidenceSeq(step time.Duration) *evidenceSeq {
	return &evidenceSeq{base: time.Now(), step: step, method: "MuMu 实例 0 · C:\\MuMu"}
}

func (q *evidenceSeq) good(left, right, n int) {
	for i := 0; i < n; i++ {
		q.frames = append(q.frames, frame.Frame{Fighting: true, Scene: "fight", LayoutProfile: "duel",
			Beads: testBeads(left, right), CapturedAt: q.base.Add(q.at), CaptureMethod: q.method})
		q.at += q.step
	}
}

// hold appends scene-uncertain frames for d, e.g. an ultimate cut-in hiding the HUD.
func (q *evidenceSeq) hold(d time.Duration) {
	for end := q.at + d; q.at < end; q.at += q.step {
		q.frames = append(q.frames, frame.Frame{Fighting: true, Hold: true, Scene: "fight", LayoutProfile: "duel",
			Beads: testBeads(-1, -1), CapturedAt: q.base.Add(q.at), CaptureMethod: q.method})
	}
}

func (q *evidenceSeq) run() *session {
	s := testSession(q.frames)
	for range q.frames {
		s.captureOnce()
	}
	return s
}

// GE-02: the same 600 ms HUD occlusion containing a 4→3 drop must give the
// same result whether or not a long cut-in happened earlier. Holds are cadence
// samples, so the earlier cut-in cannot widen the tolerance; the drop inside
// the occlusion resynchronizes the baseline instead of starting a late clock.
func TestObservationGapDoesNotDependOnEarlierOcclusion(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		q := newEvidenceSeq(20 * time.Millisecond)
		q.good(4, 4, 30)
		if earlier {
			q.hold(2500 * time.Millisecond) // e.g. an ultimate cut-in
			q.good(4, 4, 3)
		}
		q.hold(600 * time.Millisecond) // the substitute happens inside this occlusion
		q.good(4, 3, 10)
		s := q.run()
		if s.right.EventCount() != 0 || s.right.LastReady() != 3 {
			first, _ := s.right.LastEvent()
			t.Fatalf("earlier cut-in=%v: events=%d ready=%d first=%s; a drop hidden by a 600 ms occlusion must resync",
				earlier, s.right.EventCount(), s.right.LastReady(), first.Sub(q.base))
		}
	}
}

// GE-02: frames stop arriving at all (capture blackout). The documented bound
// is 3x the cadence, measured before the late frame.
func TestObservationGapIsThreeTimesCadence(t *testing.T) {
	for _, tc := range []struct {
		blackout time.Duration
		events   uint64
	}{{60 * time.Millisecond, 1}, {120 * time.Millisecond, 1}, {150 * time.Millisecond, 0}, {300 * time.Millisecond, 0}, {500 * time.Millisecond, 0}} {
		q := newEvidenceSeq(60 * time.Millisecond)
		q.method = "printwindow-fullcontent"
		q.good(4, 4, 40)
		q.at += tc.blackout
		q.good(4, 3, 2)
		if s := q.run(); s.right.EventCount() != tc.events {
			t.Fatalf("60 ms cadence, %s blackout: events=%d, want %d", tc.blackout, s.right.EventCount(), tc.events)
		}
	}
}

// GE-01: the opponent is out of beans and keeps pressing substitute. The bean
// recovers 15 s later and is used again before a second frame shows it lit.
func TestOpponentReuseRightAfterRecoveryIsCounted(t *testing.T) {
	for _, visible := range []int{1, 2} {
		q := newEvidenceSeq(20 * time.Millisecond)
		q.good(4, 1, 5)
		q.good(4, 0, 3) // substitute #1, first seen at 100 ms
		for q.at < 15*time.Second+100*time.Millisecond {
			q.good(4, 0, 1)
		}
		q.good(4, 1, visible) // the recovered bean, lit for `visible` frames
		reuse := q.base.Add(q.at)
		q.good(4, 0, 20) // substitute #2
		s := q.run()
		at, _ := s.right.LastEvent()
		if s.right.EventCount() != 2 || !at.Equal(reuse) {
			t.Fatalf("lit for %d frame(s): events=%d (want 2), last event at %s (want %s)",
				visible, s.right.EventCount(), at.Sub(q.base), reuse.Sub(q.base))
		}
		s.side = "left" // the opponent is on the right
		if got := s.detectedText(); got != "第 2 次" {
			t.Fatalf("lit for %d frame(s): overlay shows %q", visible, got)
		}
		if remaining := s.right.LatestRemaining(q.base.Add(q.at)); len(remaining) != 1 || remaining[0] < 14.5 {
			t.Fatalf("lit for %d frame(s): overlay clock %v", visible, remaining)
		}
	}
}
