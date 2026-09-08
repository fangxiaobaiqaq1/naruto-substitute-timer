package ui

import (
	"narutotimer/internal/frame"
	"testing"
	"time"
)

func TestSwapInheritsBeansRejectingOneBrightReturnFrame(t *testing.T) {
	for _, after := range [][]int{{4, 3, 3}, {3, 2, 2}, {2, 2}} {
		base := time.Unix(1700000000, 0)
		frames := []frame.Frame{
			{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 4), CapturedAt: base},
			{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: base.Add(20 * time.Millisecond)},
			{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: base.Add(40 * time.Millisecond)},
			{Hold: true, Scene: "vs", CapturedAt: base.Add(time.Second)},
		}
		for i, n := range after {
			frames = append(frames, frame.Frame{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(n, 4), CapturedAt: base.Add(3*time.Second + time.Duration(i)*20*time.Millisecond)})
		}
		s := testSession(frames)
		for range frames {
			s.captureOnce()
		}
		want := uint64(2)
		if after[0] != 3 {
			want = 1
		}
		if s.left.EventCount() != want || s.right.EventCount() != 0 {
			t.Errorf("return %v: left=%d right=%d want left=%d", after, s.left.EventCount(), s.right.EventCount(), want)
		}
		if after[0] != 3 {
			first, _ := s.left.LastEvent()
			if !first.Equal(base.Add(20 * time.Millisecond)) {
				t.Fatal("swap moved confirmed cooldown origin")
			}
		}
	}
}
