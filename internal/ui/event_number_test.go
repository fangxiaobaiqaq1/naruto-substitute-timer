package ui

import (
	"math"
	"testing"
	"time"

	fynetest "fyne.io/fyne/v2/test"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
)

func TestCaptureNewSubstituteAtOldClockBoundary(t *testing.T) {
	for _, remaining := range []time.Duration{100 * time.Millisecond, 10 * time.Millisecond, -6 * time.Millisecond} {
		t.Run(remaining.String(), func(t *testing.T) {
			at := time.Unix(1700000000, 0)
			makeFrame := func(offset time.Duration, right int) frame.Frame {
				return frame.Frame{CapturedAt: at.Add(offset), Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, right)}
			}
			frames := []frame.Frame{makeFrame(0, 4), makeFrame(16*time.Millisecond, 3), makeFrame(32*time.Millisecond, 3)}
			confirmed := 15*time.Second + 16*time.Millisecond - remaining
			first := confirmed - 32*time.Millisecond
			// Keep independent observations flowing through the real UI gap rule.
			for tick := 82 * time.Millisecond; tick < first; tick += 50 * time.Millisecond {
				frames = append(frames, makeFrame(tick, 3))
			}
			frames = append(frames, makeFrame(first, 2), makeFrame(confirmed, 2))
			s := testSession(frames)
			s.side = "left"
			for i := 0; i < len(frames)-1; i++ {
				s.captureOnce()
			}
			if s.detectedText() != "第 1 次" {
				t.Fatalf("candidate changed number: %s", s.detectedText())
			}
			s.captureOnce()
			latest := s.oppRemaining(at.Add(confirmed))
			if s.detectedText() != "第 2 次" || len(latest) != 1 || math.Abs(latest[0]-14.968) > 1e-6 {
				t.Fatalf("new substitute hidden: %s / %v", s.detectedText(), latest)
			}
			s.side = "right"
			if s.detectedText() != "第 0 次" || len(s.oppRemaining(at.Add(confirmed))) != 0 {
				t.Fatal("number did not follow selected opponent")
			}
			s.side = ""
			if s.detectedText() != "左0 / 右2" {
				t.Fatal("unknown player side mislabeled opponent")
			}
		})
	}
}

func TestEventBadgeRefreshesWhenRoundedClockTextDoesNotChange(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	s := &session{cfg: config.Default(), side: "left"}
	content := s.overlayContent()
	fynetest.NewTempWindow(t, content)
	at := time.Now()
	cd := 15 * time.Second
	s.right.Observe(4, true, at.Add(-time.Second), cd, 1)
	s.right.Observe(3, true, at, cd, 1)
	s.refreshClockAt(at)
	if s.cd.Text != "15.0" || s.eventTag.Text != "第 1 次" {
		t.Fatalf("first: %s / %s", s.cd.Text, s.eventTag.Text)
	}
	s.right.Observe(2, true, at.Add(time.Millisecond), cd, 1)
	s.refreshClockAt(at.Add(time.Millisecond))
	if s.cd.Text != "15.0" || s.eventTag.Text != "第 2 次" {
		t.Fatalf("same rounded time hid event: %s / %s", s.cd.Text, s.eventTag.Text)
	}
	s.refreshClockAt(at.Add(16 * time.Second))
	if s.cd.Text != "—" || s.eventTag.Text != "第 2 次" {
		t.Fatal("clock expiry erased detection number")
	}
}
