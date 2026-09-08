package ui

import (
	"narutotimer/internal/frame"
	"strings"
	"testing"
	"time"
)

func TestUncertainAnimationBreaksExitVotes(t *testing.T) {
	for _, gap := range []frame.Frame{{Hold: true}, {Scene: "vs", Hold: true}, {Scene: "result", Hold: true}} {
		at := time.Unix(1700000000, 0)
		frames := []frame.Frame{{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 4), CapturedAt: at}}
		for i := 1; i <= 20; i++ {
			f := frame.Frame{Scene: "result"}
			if i%2 == 0 {
				f = gap
			}
			f.CapturedAt = at.Add(time.Duration(i) * 20 * time.Millisecond)
			frames = append(frames, f)
		}
		s := testSession(frames)
		for range frames {
			s.captureOnce()
		}
		if !s.fighting {
			t.Fatalf("interrupted/duplicate result hits accumulated into exit: %+v", gap)
		}
	}
}

func TestLongCinematicHoldsMatchAndClockWithoutBeanVotes(t *testing.T) {
	at := time.Unix(1700000000, 0)
	frames := []frame.Frame{
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 4), CapturedAt: at},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: at.Add(20 * time.Millisecond)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: at.Add(40 * time.Millisecond)},
	}
	for i := 1; i <= 150; i++ {
		frames = append(frames, frame.Frame{Hold: true, Scene: "blank", CapturedAt: at.Add(40*time.Millisecond + time.Duration(i)*20*time.Millisecond)})
	}
	s := testSession(frames)
	for i := range frames {
		s.captureOnce()
		if i < 3 {
			continue
		}
		if !s.fighting || s.left.EventCount() != 1 || !s.left.Active() {
			t.Fatal("animation reset clock or invented event")
		}
		if line := s.statusLine(); !strings.Contains(line, "决斗场 · 画面遮挡") || !strings.Contains(line, "左?/右?") {
			t.Fatalf("misleading cinematic status: %s", line)
		}
	}
}

func TestExitRejectsRepeatedTimestampButAcceptsStaticPage(t *testing.T) {
	at := time.Unix(1700000000, 0)
	s := testSession(nil)
	s.applyScene(frame.Frame{Fighting: true, Scene: "fight", CapturedAt: at})
	for range 20 {
		s.applyScene(frame.Frame{Scene: "result", CapturedAt: at.Add(time.Millisecond)})
	}
	if !s.fighting {
		t.Fatal("same captured frame reset match")
	}
	for i := 1; i <= 10; i++ {
		s.applyScene(frame.Frame{Scene: "result", Duplicate: true, CapturedAt: at.Add(time.Duration(i+1) * 20 * time.Millisecond)})
	}
	if s.fighting {
		t.Fatal("fresh captures of static result cannot exit")
	}
}
