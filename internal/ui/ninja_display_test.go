package ui

import (
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
	"testing"
	"time"
)

func TestNinjaDisplayDoesNotBlinkOrBecomeRuleEvidence(t *testing.T) {
	s := &session{cfg: config.Default(), side: "left"}
	at := time.Unix(1700000000, 0)
	s.updateHUD(frame.Frame{Fighting: true, Scene: "fight", LayoutProfile: "duel", RightNinja: ninja.FifthMizukage, CapturedAt: at})
	for _, elapsed := range []time.Duration{16 * time.Millisecond, 200 * time.Millisecond, 301 * time.Millisecond, 1500 * time.Millisecond} {
		s.updateHUD(frame.Frame{Fighting: true, Scene: "fight", CapturedAt: at.Add(elapsed)})
		got, pending := s.recentOpponentDisplay()
		if got != ninja.FifthMizukage || pending != (elapsed > nameDisplayGrace) {
			t.Fatalf("%v: %q pending=%v", elapsed, got, pending)
		}
		if s.opponentNinja() != "" {
			t.Fatal("display cache fed special cooldown evidence")
		}
	}
	s.updateHUD(frame.Frame{Fighting: true, CapturedAt: at.Add(2100 * time.Millisecond)})
	if name, _ := s.recentOpponentDisplay(); name != "" {
		t.Fatal("expired name remained")
	}
}

func TestNinjaDisplayGapRecoveryAndBoundaries(t *testing.T) {
	at := time.Unix(1700000000, 0)
	for _, kind := range []string{"vs", "lobby", "rewind", "different candidate", "new ninja"} {
		s := &session{cfg: config.Default(), side: "left", layoutProfile: "duel"}
		s.updateHUD(frame.Frame{Fighting: true, Scene: "fight", RightNinja: ninja.Naruto, CapturedAt: at})
		s.updateHUD(frame.Frame{Fighting: true, Scene: "fight", CapturedAt: at.Add(16 * time.Millisecond)})
		s.updateHUD(frame.Frame{Fighting: true, Scene: "fight", RightNinja: ninja.Naruto, CapturedAt: at.Add(32 * time.Millisecond)})
		if got, pending := s.recentOpponentDisplay(); got != ninja.Naruto || pending {
			t.Fatal("one-frame gap lost name")
		}
		f := frame.Frame{Fighting: true, Scene: "fight", CapturedAt: at.Add(50 * time.Millisecond)}
		switch kind {
		case "vs":
			f = frame.Frame{Scene: "vs", Hold: true, CapturedAt: f.CapturedAt}
		case "lobby":
			f = frame.Frame{Scene: "lobby", CapturedAt: f.CapturedAt}
		case "rewind":
			f.CapturedAt = at.Add(-time.Second)
		case "different candidate":
			f.RightNinjaCandidate = "其他忍者"
		case "new ninja":
			f.RightNinja = ninja.Obito
		}
		s.updateNinjaDisplay(f)
		want := ""
		if kind == "new ninja" {
			want = ninja.Obito
		}
		if got, _ := s.recentOpponentDisplay(); got != want {
			t.Errorf("%s: %q want %q", kind, got, want)
		}
	}
}
