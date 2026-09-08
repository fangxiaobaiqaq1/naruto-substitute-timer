package ui

import (
	"testing"
	"time"

	"narutotimer/internal/frame"
)

func TestOpponentRoundReturnDoesNotRestartClock(t *testing.T) {
	at := time.Unix(1700000000, 0)
	frames := []frame.Frame{
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 4), CapturedAt: at},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 3), CapturedAt: at.Add(20 * time.Millisecond)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 3), CapturedAt: at.Add(40 * time.Millisecond)},
		{Hold: true, Scene: "vs", CapturedAt: at.Add(time.Second)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 2), CapturedAt: at.Add(3 * time.Second)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 2), CapturedAt: at.Add(3020 * time.Millisecond)},
	}
	s := testSession(frames)
	for range frames {
		s.captureOnce()
	}
	first, _ := s.right.LastEvent()
	if s.right.EventCount() != 1 || !first.Equal(at.Add(20*time.Millisecond)) || s.right.LastReady() != 2 {
		t.Fatalf("opponent round return restarted timer: events=%d first=%v ready=%d", s.right.EventCount(), first, s.right.LastReady())
	}
}
