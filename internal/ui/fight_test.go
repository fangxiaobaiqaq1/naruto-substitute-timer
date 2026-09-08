package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/frame"
)

func TestApplyFightResetsClocksAfterLeave(t *testing.T) {
	s := &session{cfg: config.Default()}
	s.cfg.Tracking.EnterFightFrames = 1
	s.cfg.Tracking.LeaveFightFrames = 10

	now := time.Unix(1_700_000_000, 0)
	cd := 13500 * time.Millisecond
	s.left.Observe(4, true, now, cd, 1)
	s.left.Observe(3, true, now.Add(time.Second), cd, 1)
	if !s.left.Active() {
		t.Fatal("setup: clock should be running")
	}

	s.applyFight(true)
	if !s.fighting {
		t.Fatal("one enter frame should start fight")
	}
	for i := 0; i < 9; i++ {
		s.applyFight(false)
		if !s.fighting || !s.left.Active() {
			t.Fatalf("leave %d/10 must keep clock, fighting=%v active=%v", i+1, s.fighting, s.left.Active())
		}
	}
	s.applyFight(false)
	if s.fighting {
		t.Fatal("10th leave frame must exit fight")
	}
	if s.left.Active() || s.left.LastReady() != 0 {
		t.Fatalf("leave confirm must reset clocks, active=%v last=%d", s.left.Active(), s.left.LastReady())
	}
}

func TestApplySceneHoldsOnDeathSwap(t *testing.T) {
	s := &session{cfg: config.Default()}
	s.cfg.Tracking.EnterFightFrames = 1
	s.cfg.Tracking.LeaveFightFrames = 10
	now := time.Unix(1_700_000_000, 0)
	cd := 13500 * time.Millisecond
	s.applyFight(true)
	s.left.Observe(4, true, now, cd, 1)
	s.left.Observe(3, true, now.Add(time.Second), cd, 1)
	for i := 0; i < 12; i++ {
		s.applyScene(frame.Frame{Scene: "vs", Hold: true})
	}
	if !s.fighting || !s.left.Active() || !s.holdFreeze {
		t.Fatalf("death-swap/vs must keep beans and clock, fighting=%v active=%v freeze=%v", s.fighting, s.left.Active(), s.holdFreeze)
	}
}

func TestApplySceneReturnFromHoldDoesNotStartClock(t *testing.T) {
	s := &session{cfg: config.Default()}
	s.cfg.Tracking.EnterFightFrames = 1
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	s.applyFight(true)
	s.left.Observe(4, true, now, cd, 1)
	s.applyScene(frame.Frame{Scene: "vs", Hold: true})
	s.applyScene(frame.Frame{
		Fighting: true,
		Scene:    "fight",
		Beads: []frame.Bead{
			{Label: "L1", Lit: true},
			{Label: "L2", Lit: true},
			{Label: "L3", Lit: true},
			{Label: "L4", Dark: true},
		},
	})
	if s.left.Active() {
		t.Fatal("returning from hold must not treat 4→3 as a substitute")
	}
	if s.left.LastReady() != 4 || !s.syncLeft {
		t.Fatalf("scene return alone must retain inherited 4 until bean verification, got %d", s.left.LastReady())
	}
}

func TestMaybeAutoSideFromVS(t *testing.T) {
	s := &session{cfg: config.Default()}
	s.cfg.UI.PlayerSide = "auto"
	s.maybeAutoSide(frame.Frame{PlayerSide: "right"})
	if s.side != "right" {
		t.Fatalf("side = %q", s.side)
	}
}

func TestMaybeAutoSideDoesNotOverrideManual(t *testing.T) {
	s := &session{cfg: config.Default()}
	s.cfg.UI.PlayerSide = "left"
	s.side = "left"
	s.maybeAutoSide(frame.Frame{PlayerSide: "right"})
	if s.side != "left" {
		t.Fatalf("manual side must stay, got %q", s.side)
	}
}

func TestOppRemainingUsesOppositeSide(t *testing.T) {
	s := &session{}
	now := time.Unix(1_700_000_000, 0)
	cd := 15 * time.Second
	s.left.Observe(4, true, now, cd, 1)
	s.left.Observe(3, true, now.Add(time.Second), cd, 1)
	s.right.Observe(4, true, now, cd, 1)
	s.side = "left"
	if got := s.oppRemaining(now.Add(time.Second)); len(got) != 0 {
		t.Fatalf("my left clock must not show, got %v", got)
	}
	s.side = "right"
	if got := s.oppRemaining(now.Add(time.Second)); len(got) != 1 {
		t.Fatalf("opponent is left when I am right, got %v", got)
	}
}

func TestCommitBeadsShowsUnknownInsteadOfPreviousCount(t *testing.T) {
	s := &session{cfg: config.Default()}
	s.beads = []frame.Bead{
		{Label: "L1", Lit: true},
		{Label: "L2", Lit: true},
		{Label: "L3", Lit: true},
		{Label: "L4", Lit: true},
		{Label: "R1", Lit: true},
		{Label: "R2", Lit: true},
		{Label: "R3", Lit: true},
		{Label: "R4", Lit: true},
	}
	next := []frame.Bead{
		{Label: "L1", Lit: true},
		{Label: "L2", Lit: true},
		{Label: "L3", Lit: true},
		{Label: "L4", Unknown: true},
		{Label: "R1", Lit: true},
		{Label: "R2", Lit: true},
		{Label: "R3", Lit: true},
		{Label: "R4", Dark: true},
	}
	if !s.commitBeads(next) {
		t.Fatal("known right-side drop should commit even if left is unknown")
	}
	if left, right := s.visibleReady(true), s.visibleReady(false); left != "?" || right != "3" {
		t.Fatalf("left is unknown, right is freshly observed: got L=%s R=%s", left, right)
	}
}

func TestCommitBeadsAcceptsKnownDarkSide(t *testing.T) {
	s := &session{}
	s.beads = []frame.Bead{
		{Label: "L1", Lit: true}, {Label: "L2", Lit: true}, {Label: "L3", Lit: true}, {Label: "L4", Lit: true},
		{Label: "R1", Lit: true}, {Label: "R2", Lit: true}, {Label: "R3", Lit: true}, {Label: "R4", Lit: true},
	}
	next := []frame.Bead{
		{Label: "L1", Dark: true}, {Label: "L2", Dark: true}, {Label: "L3", Dark: true}, {Label: "L4", Dark: true},
		{Label: "R1", Lit: true}, {Label: "R2", Lit: true}, {Label: "R3", Lit: true}, {Label: "R4", Lit: true},
	}
	if !s.commitBeads(next) {
		t.Fatal("known dark left side should replace stale 4")
	}
	lc, rc := countReady(s.beads)
	if lc != 0 || rc != 4 {
		t.Fatalf("left should drop to 0, right stay 4, got L=%d R=%d", lc, rc)
	}
}

func TestApplyFightSingleMissDoesNotReset(t *testing.T) {
	s := &session{cfg: config.Default()}
	s.cfg.Tracking.EnterFightFrames = 1
	s.cfg.Tracking.LeaveFightFrames = 10
	now := time.Unix(1_700_000_000, 0)
	cd := 13500 * time.Millisecond
	s.applyFight(true)
	s.left.Observe(4, true, now, cd, 1)
	s.left.Observe(3, true, now.Add(time.Second), cd, 1)
	s.applyFight(false)
	if !s.fighting || !s.left.Active() {
		t.Fatalf("single miss must hold, fighting=%v active=%v", s.fighting, s.left.Active())
	}
}

func TestStatusLineShowsScene(t *testing.T) {
	s := &session{cfg: config.Default(), scene: "fight", fighting: true, engine: "rgb-duel", beads: testBeads(4, 3)}
	got := s.statusLine()
	if !strings.Contains(got, "决斗场") || !strings.Contains(got, "对局") || !strings.Contains(got, "左4/右3") {
		t.Fatalf("status = %q", got)
	}
}

func TestCaptureOnceNeverConfirmsUsingDisplayCache(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	unknownLeft := testBeads(3, 3)
	unknownLeft[3] = frame.Bead{Label: "L4", Unknown: true}
	frames := []frame.Frame{
		{Fighting: true, Beads: testBeads(4, 4), CapturedAt: now},
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(16 * time.Millisecond)},
		{Fighting: true, Beads: unknownLeft, CapturedAt: now.Add(32 * time.Millisecond)},
		{Fighting: true, Beads: testBeads(3, 3), CapturedAt: now.Add(48 * time.Millisecond)},
		{Fighting: true, Beads: testBeads(4, 3), CapturedAt: now.Add(64 * time.Millisecond)},
	}
	s := testSession(frames)
	s.captureOnce()
	s.captureOnce()
	s.captureOnce()
	if s.left.Active() {
		t.Fatal("4→false 3→unknown must not start a timer using the retained display value")
	}
	if got := s.visibleReady(true); got != "?" {
		t.Fatalf("unknown current side must be visibly unknown, got %s", got)
	}
	s.captureOnce()
	if s.left.Active() || !s.right.Active() {
		t.Fatalf("left confirmation must restart while fresh right frames can confirm, left=%v right=%v", s.left.Active(), s.right.Active())
	}
	s.captureOnce()
	if s.left.Active() {
		t.Fatal("the false left drop must never become a timer")
	}
}

func TestCaptureOnceUntrustedFramesInterruptConfirmation(t *testing.T) {
	for name, interrupted := range map[string]frame.Frame{
		"hold":       {Hold: true, Fighting: true, Beads: testBeads(3, 4)},
		"error":      {Err: errors.New("capture failed"), Fighting: true, Beads: testBeads(3, 4)},
		"empty":      {Fighting: true},
		"not fight":  {Fighting: false, Beads: testBeads(3, 4)},
		"empty left": {Fighting: true, Beads: testBeads(3, 4)[4:]},
	} {
		t.Run(name, func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			interrupted.CapturedAt = now.Add(32 * time.Millisecond)
			s := testSession([]frame.Frame{
				{Fighting: true, Beads: testBeads(4, 4), CapturedAt: now},
				{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(16 * time.Millisecond)},
				interrupted,
				{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(48 * time.Millisecond)},
			})
			for i := 0; i < 4; i++ {
				s.captureOnce()
			}
			if s.left.Active() {
				t.Fatal("a missing or untrusted observation must break drop confirmation")
			}
		})
	}
}

func TestCaptureOnceLostObservationHidesOldCountsButKeepsConfirmedCooldown(t *testing.T) {
	for name, lost := range map[string]frame.Frame{
		"unknown scene": {Hold: true, Scene: "", Beads: testBeads(4, 1)},
		"capture error": {Err: errors.New("capture failed"), Fighting: true, Scene: "fight", Beads: testBeads(4, 1)},
		"empty frame":   {Hold: true, Fighting: true, Scene: "fight"},
		"scene switch":  {Hold: true, Scene: "vs"},
		"lobby":         {Scene: "lobby", Beads: testBeads(4, 1)},
	} {
		t.Run(name, func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			lost.CapturedAt = now.Add(48 * time.Millisecond)
			s := testSession([]frame.Frame{
				{Fighting: true, Scene: "fight", Beads: testBeads(4, 4), CapturedAt: now},
				{Fighting: true, Scene: "fight", Beads: testBeads(4, 3), CapturedAt: now.Add(16 * time.Millisecond)},
				{Fighting: true, Scene: "fight", Beads: testBeads(4, 3), CapturedAt: now.Add(32 * time.Millisecond)},
				lost,
				{Fighting: true, Scene: "fight", Beads: testBeads(2, 2), CapturedAt: now.Add(64 * time.Millisecond)},
			})
			for i := 0; i < 3; i++ {
				s.captureOnce()
			}
			if !s.right.Active() {
				t.Fatal("setup: opponent cooldown should be confirmed")
			}
			s.captureOnce()
			if got := s.statusLine(); !strings.Contains(got, "左?/右?") {
				t.Fatalf("lost observation displayed old counts: %s", got)
			}
			if !s.right.Active() {
				t.Fatal("a lost observation must preserve an already confirmed cooldown")
			}
			s.captureOnce()
			if got := s.statusLine(); !strings.Contains(got, "左2/右2") {
				t.Fatalf("new valid observation must replace unknown counts: %s", got)
			}
		})
	}
}

func TestCaptureOncePartialUnknownShowsOnlyFreshKnownSide(t *testing.T) {
	partial := testBeads(2, 2)
	partial[0].Unknown = true
	s := testSession([]frame.Frame{
		{Fighting: true, Scene: "fight", Beads: testBeads(4, 1)},
		{Fighting: true, Hold: true, Scene: "fight", Beads: partial},
	})
	s.captureOnce()
	s.captureOnce()
	if got := s.statusLine(); !strings.Contains(got, "左?/右2") || !strings.Contains(got, "待识别") {
		t.Fatalf("partial observation must expose uncertainty per side: %s", got)
	}
}

func TestBeanUncertaintyDoesNotChangeSceneModeOrBlockOtherSide(t *testing.T) {
	now := time.Unix(1700000000, 0)
	var frames []frame.Frame
	for i := 0; i < 9; i++ {
		beads := testBeads(4, 2)
		if i >= 1 {
			beads = testBeads(4, 1)
		}
		if i%2 == 1 {
			beads[0].Unknown = true
		}
		frames = append(frames, frame.Frame{Fighting: true, Scene: "fight", LayoutProfile: "camp", Beads: beads, CapturedAt: now.Add(time.Duration(i) * 20 * time.Millisecond)})
	}
	s := testSession(frames)
	for i := range frames {
		s.captureOnce()
		got := s.statusLine()
		if !strings.Contains(got, "训练场 · 对局") || strings.Contains(got, "待识别") {
			t.Errorf("frame%d changed scene mode: %s", i, got)
		}
		if i%2 == 1 && !strings.Contains(got, "左?/右1") {
			t.Errorf("frame%d hid uncertainty: %s", i, got)
		}
	}
	if !s.right.Active() || s.left.Active() {
		t.Fatal("one unknown side blocked valid opposite-side event or invented an event")
	}
}

func TestVisibleReadyRejectsIncompleteSide(t *testing.T) {
	s := session{cfg: config.Default(), beads: testBeads(4, 4)[:7]}
	if left, right := s.visibleReady(true), s.visibleReady(false); left != "4" || right != "?" {
		t.Fatalf("missing slot must not look like a known three: left=%s right=%s", left, right)
	}
}

func TestCaptureOnceRepeatedTimestampDoesNotConfirm(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := testSession([]frame.Frame{
		{Fighting: true, Beads: testBeads(4, 4), CapturedAt: now},
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(16 * time.Millisecond)},
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(16 * time.Millisecond)},
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(32 * time.Millisecond)},
	})
	for i := 0; i < 3; i++ {
		s.captureOnce()
	}
	if s.left.Active() {
		t.Fatal("re-reading a captured frame must not confirm an event")
	}
	s.captureOnce()
	if !s.left.Active() {
		t.Fatal("a second fresh drop frame should confirm an event")
	}
}

func TestCaptureOnceDuplicatePixelsDoNotConfirm(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := testSession([]frame.Frame{
		{Fighting: true, Beads: testBeads(4, 4), CapturedAt: now},
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(16 * time.Millisecond)},
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(32 * time.Millisecond), Duplicate: true},
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(48 * time.Millisecond)},
	})
	for i := 0; i < 3; i++ {
		s.captureOnce()
	}
	if s.left.Active() {
		t.Fatal("new acquisition timestamp with the same pixels confirmed a drop")
	}
	s.captureOnce()
	if !s.left.Active() {
		t.Fatal("fresh visual evidence should confirm the drop")
	}
}

func TestCaptureOnceLayoutSwitchStartsNewBaseline(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := testSession([]frame.Frame{
		{Fighting: true, LayoutProfile: "camp", Beads: testBeads(4, 4), CapturedAt: now},
		{Fighting: true, LayoutProfile: "camp", Beads: testBeads(3, 4), CapturedAt: now.Add(16 * time.Millisecond)},
		{Fighting: true, LayoutProfile: "camp", Beads: testBeads(3, 4), CapturedAt: now.Add(32 * time.Millisecond)},
		{Fighting: true, LayoutProfile: "duel", Beads: testBeads(2, 4), CapturedAt: now.Add(48 * time.Millisecond)},
		{Fighting: true, LayoutProfile: "duel", Beads: testBeads(2, 4), CapturedAt: now.Add(64 * time.Millisecond)},
	})
	for i := 0; i < 3; i++ {
		s.captureOnce()
	}
	if !s.left.Active() {
		t.Fatal("setup: expected training timer")
	}
	s.captureOnce()
	s.captureOnce()
	if s.left.Active() || s.left.LastReady() != 2 {
		t.Fatalf("new HUD reused old clock: active=%v ready=%d", s.left.Active(), s.left.LastReady())
	}
}

func TestContradictoryBeadCannotVote(t *testing.T) {
	beads := testBeads(4, 4)
	beads[3].Dark = true
	if sideObservable(beads, true) {
		t.Fatal("a bead cannot be both dark and available")
	}
}

func TestStatusLineNamesTraining(t *testing.T) {
	s := session{scene: "fight", fighting: true, layoutProfile: "camp"}
	if got := s.statusLine(); !strings.Contains(got, "训练场") {
		t.Fatalf("status=%s", got)
	}
}

func TestReturnFromHoldWaitsForKnownSideToCalibrateWithoutDrop(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	unknownLeft := testBeads(3, 4)
	unknownLeft[3] = frame.Bead{Label: "L4", Unknown: true}
	s := testSession([]frame.Frame{
		{Fighting: true, Beads: testBeads(4, 4), CapturedAt: now},
		{Hold: true, Scene: "vs", CapturedAt: now.Add(16 * time.Millisecond)},
		{Fighting: true, Beads: unknownLeft, CapturedAt: now.Add(32 * time.Millisecond)},
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(48 * time.Millisecond)},
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(64 * time.Millisecond)},
	})
	for i := 0; i < 5; i++ {
		s.captureOnce()
	}
	if s.left.Active() || s.left.LastReady() != 3 || s.left.EventCount() != 0 {
		t.Fatalf("two fresh post-hold lower counts only calibrate, active=%v ready=%d", s.left.Active(), s.left.LastReady())
	}
	first, _ := s.left.LastEvent()
	if !first.IsZero() {
		t.Fatal("hidden interval supplied an invented event time")
	}
}

func TestCaptureScheduleIncludesProcessingTime(t *testing.T) {
	if got := nextCaptureWait(16*time.Millisecond, 10*time.Millisecond); got != 6*time.Millisecond {
		t.Fatalf("16ms cadence with 10ms processing should wait 6ms, got %s", got)
	}
	if got := nextCaptureWait(16*time.Millisecond, 30*time.Millisecond); got != time.Millisecond {
		t.Fatalf("slow processing must yield without accumulating a full extra interval, got %s", got)
	}
	s := &session{cfg: config.Default(), fighting: true}
	s.cfg.UI.PollIntervalMS = 16
	if got := s.poll(); got != 16*time.Millisecond {
		t.Fatalf("front-end polling must allow 16ms, got %s", got)
	}
}

func testSession(frames []frame.Frame) *session {
	s := &session{cfg: config.Default()}
	s.cfg.Tracking.MinimumConfirmFrames = 2
	s.cfg.UI.PlayerSide = "left"
	index := 0
	s.provider = func() frame.Frame {
		f := frames[index]
		index++
		return f
	}
	return s
}

func testBeads(leftReady, rightReady int) []frame.Bead {
	var beads []frame.Bead
	for side, labels := range [][]string{{"L1", "L2", "L3", "L4"}, {"R1", "R2", "R3", "R4"}} {
		ready := leftReady
		if side == 1 {
			ready = rightReady
		}
		for i, label := range labels {
			beads = append(beads, frame.Bead{Label: label, Lit: i < ready, Dark: i >= ready})
		}
	}
	return beads
}
