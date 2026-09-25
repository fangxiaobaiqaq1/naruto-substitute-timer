package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/capture"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
)

func TestVerifiedRoundOpeningSurvivesObscuredBeans(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	frames := []frame.Frame{
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 4), CapturedAt: base},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: base.Add(20 * time.Millisecond)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: base.Add(40 * time.Millisecond)},
		// The marker is visible even though one bean strip is obscured.
		{Fighting: true, Hold: true, Scene: "fight", LayoutProfile: "duel", RoundOpening: true, Beads: testBeads(-1, 4), CapturedAt: base.Add(time.Second)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", RoundOpening: true, Beads: testBeads(3, 4), CapturedAt: base.Add(1020 * time.Millisecond)},
	}
	s := testSession(frames)
	for range frames {
		s.captureOnce()
	}
	if s.left.EventCount() != 1 || s.left.Active() || s.left.LastReady() != 3 {
		t.Fatalf("obscured opening did not reset/calibrate round: events=%d active=%v ready=%d", s.left.EventCount(), s.left.Active(), s.left.LastReady())
	}
}

func TestVerifiedRoundOpeningResetsPriorRoundAndBaselinesOpeningBeans(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	frames := []frame.Frame{
		// Previous round: a real left substitute has already been counted.
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 4), CapturedAt: base},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: base.Add(20 * time.Millisecond)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: base.Add(40 * time.Millisecond)},
		// The verified round marker starts the next round. Its animation may
		// oscillate the visible count, but it must not start a clock.
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", RoundOpening: true, Beads: testBeads(4, 4), CapturedAt: base.Add(time.Second)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", RoundOpening: true, Beads: testBeads(3, 4), CapturedAt: base.Add(1020 * time.Millisecond)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", RoundOpening: false, Beads: testBeads(3, 4), CapturedAt: base.Add(1400 * time.Millisecond)},
		// A post-opening real drop still uses its first observed timestamp.
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(2, 4), CapturedAt: base.Add(1420 * time.Millisecond)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(2, 4), CapturedAt: base.Add(1440 * time.Millisecond)},
	}
	s := testSession(frames)
	for range frames {
		s.captureOnce()
	}
	if s.left.EventCount() != 2 || !s.left.Active() || s.right.EventCount() != 0 {
		t.Fatalf("new round retained or fabricated counters: left=%d right=%d ready=%d", s.left.EventCount(), s.right.EventCount(), s.left.LastReady())
	}
	if got, _ := s.left.LastEvent(); !got.Equal(base.Add(1420 * time.Millisecond)) {
		t.Fatalf("post-opening substitute started at %v, want %v", got, base.Add(1420*time.Millisecond))
	}
}

func TestFirstCompleteFightFrameOnlyEstablishesBaseline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	frames := []frame.Frame{
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: base},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 4), CapturedAt: base.Add(20 * time.Millisecond)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(2, 4), CapturedAt: base.Add(40 * time.Millisecond)},
		{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(2, 4), CapturedAt: base.Add(60 * time.Millisecond)},
	}
	s := testSession(frames)
	for range frames {
		s.captureOnce()
	}
	if s.left.EventCount() != 1 || !s.left.Active() {
		t.Fatalf("first fight frame was treated as a drop: events=%d active=%v ready=%d", s.left.EventCount(), s.left.Active(), s.left.LastReady())
	}
	if got, _ := s.left.LastEvent(); !got.Equal(base.Add(40 * time.Millisecond)) {
		t.Fatalf("real post-baseline drop started at %v, want %v", got, base.Add(40*time.Millisecond))
	}
}

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

func TestMuMuObservationGapRequiresNormalizedMethodIdentity(t *testing.T) {
	s := &session{cfg: config.Default()}
	s.cfg.UI.PollIntervalMS = 50
	displaySource := `MuMu 实例 0 · E:\Program Files\Netease\MuMu`
	if got := s.observationGap(displaySource); got != 150*time.Millisecond {
		t.Fatalf("display source selected SDK gap: %s", got)
	}
	if got := s.observationGap(capture.MethodMuMuSDK); got != 1250*time.Millisecond {
		t.Fatalf("normalized SDK method gap = %s, want 1250ms", got)
	}
	if got := s.observationGap(capture.MethodLeidianADB); got != leidianADBObservationGap {
		t.Fatalf("Leidian gap changed: %s", got)
	}
}

func TestCaptureOnceMuMuHalfSecondConfirmationUsesFirstCapture(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	first := base.Add(100 * time.Millisecond)
	confirmation := first.Add(500 * time.Millisecond)
	s := testSession([]frame.Frame{
		{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, Beads: testBeads(4, 3), CapturedAt: base},
		{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, Beads: testBeads(4, 2), CapturedAt: first},
		{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, Beads: testBeads(4, 2), CapturedAt: confirmation},
	})
	for range []int{0, 1, 2} {
		s.captureOnce()
	}
	at, serial := s.right.LastEvent()
	if serial != 1 || !at.Equal(first) {
		t.Fatalf("event=%v/%d, want first lower frame %v", at, serial, first)
	}
	if got := s.right.LatestRemaining(confirmation); len(got) != 1 || got[0] != 14.5 {
		t.Fatalf("remaining at confirmation=%v, want 14.5 from first frame", got)
	}
}

func TestMuMuObservationGapUsesCaptureBudgetAndActualCadence(t *testing.T) {
	if got := mumuObservationGap(1200, 50, 0); got != 1250*time.Millisecond {
		t.Fatalf("gap=%s, want 1250ms", got)
	}
	if got := mumuObservationGap(200, 800, 0); got != time.Second {
		t.Fatalf("slow poll gap=%s, want 1s", got)
	}
	if got := mumuObservationGap(200, 50, 900*time.Millisecond); got != 1100*time.Millisecond {
		t.Fatalf("analysis-bound gap=%s, want 1100ms", got)
	}
}

func TestCaptureOnceMuMuSlowPollCadenceStillConfirmsCurrentFrames(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	s := testSession([]frame.Frame{
		{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 3), CapturedAt: base},
		{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 2), CapturedAt: base.Add(800 * time.Millisecond)},
		{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 2), CapturedAt: base.Add(1750 * time.Millisecond)},
	})
	s.cfg.Capture.TimeoutMS = 200
	s.cfg.UI.PollIntervalMS = 800 // Larger than capture timeout: supported cadence is 1s.
	for range []int{0, 1, 2} {
		s.captureOnce()
	}
	at, serial := s.right.LastEvent()
	if serial != 1 || !at.Equal(base.Add(800*time.Millisecond)) {
		t.Fatalf("slow-poll valid frames did not confirm from first lower capture: %v/%d", at, serial)
	}
	if got := s.right.LatestRemaining(base.Add(1750 * time.Millisecond)); len(got) != 1 || got[0] != 14.05 {
		t.Fatalf("event formula changed: remaining=%v", got)
	}
}

func TestCaptureOnceMuMuRightDropConfirmsAcrossSuccessfulSDKCalls(t *testing.T) {
	for _, slots := range []int{4, 6} {
		t.Run(fmt.Sprintf("%d slots", slots), func(t *testing.T) {
			base := time.Unix(1_700_000_000, 0)
			s := testSession([]frame.Frame{
				{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: slots, Beads: testBeadsForSlots(4, 3, slots), CapturedAt: base},
				{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: slots, Beads: testBeadsForSlots(4, 2, slots), CapturedAt: base.Add(1040 * time.Millisecond)},
				{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: slots, Beads: testBeadsForSlots(4, 2, slots), CapturedAt: base.Add(2080 * time.Millisecond)},
			})
			for range []int{0, 1, 2} {
				s.captureOnce()
			}
			first, serial := s.right.LastEvent()
			if serial != 1 || !first.Equal(base.Add(1040*time.Millisecond)) || s.right.EventCount() != 1 {
				t.Fatalf("right 3/%d→2/%d was not confirmed exactly once: at=%v serial=%d count=%d", slots, slots, first, serial, s.right.EventCount())
			}
		})
	}
}

func TestCaptureOnceMuMuUntrustedFramesRestartRightDropConfirmation(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	unknown := testBeads(4, 2)
	unknown[7].Unknown = true
	for _, tc := range []struct {
		name        string
		interrupted frame.Frame
	}{
		{name: "unknown", interrupted: frame.Frame{Fighting: true, Scene: "fight", Beads: unknown}},
		{name: "hold", interrupted: frame.Frame{Fighting: true, Hold: true, Scene: "fight", Beads: testBeads(4, 2)}},
		{name: "duplicate", interrupted: frame.Frame{Fighting: true, Scene: "fight", Duplicate: true, Beads: testBeads(4, 2)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			interrupted := tc.interrupted
			interrupted.CaptureMethod = capture.MethodMuMuSDK
			interrupted.RightSlots = 4
			interrupted.CapturedAt = base.Add(200 * time.Millisecond)
			frames := []frame.Frame{
				{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 3), CapturedAt: base},
				{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 2), CapturedAt: base.Add(100 * time.Millisecond)},
				interrupted,
				{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 2), CapturedAt: base.Add(300 * time.Millisecond)},
				{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 2), CapturedAt: base.Add(400 * time.Millisecond)},
			}
			s := testSession(frames)
			for i := range frames {
				s.captureOnce()
				if i == 2 && s.right.EventCount() != 0 {
					t.Fatal("untrusted frame supplied a stale confirmation vote")
				}
			}
			if s.right.EventCount() != 1 {
				t.Fatalf("two fresh low observations after %s should confirm once, got %d", tc.name, s.right.EventCount())
			}
		})
	}
}

func TestCaptureOnceMuMuGapOverBoundRebaselinesRightLowObservation(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	s := testSession([]frame.Frame{
		{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 3), CapturedAt: base},
		{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 2), CapturedAt: base.Add(1040 * time.Millisecond)},
		// Default bound is 1200ms + 16ms minimum poll cadence; this is outside it.
		{Fighting: true, Scene: "fight", CaptureMethod: capture.MethodMuMuSDK, RightSlots: 4, Beads: testBeads(4, 2), CapturedAt: base.Add(2300 * time.Millisecond)},
	})
	for range []int{0, 1, 2} {
		s.captureOnce()
	}
	if s.right.EventCount() != 0 || s.right.Active() || s.right.LastReady() != 2 {
		t.Fatalf("over-gap low observation invented an event: count=%d active=%v ready=%d", s.right.EventCount(), s.right.Active(), s.right.LastReady())
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
		{Fighting: true, Beads: testBeads(3, 4), CapturedAt: now.Add(64 * time.Millisecond)},
	})
	for i := 0; i < 4; i++ {
		s.captureOnce()
	}
	if s.left.Active() {
		t.Fatal("a duplicate must clear the pending vote rather than confirming with stale evidence")
	}
	s.captureOnce()
	if !s.left.Active() {
		t.Fatal("two fresh visual observations after a duplicate should confirm the drop")
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
	return testBeadsForSlots(leftReady, rightReady, 4)
}

func testBeadsForSlots(leftReady, rightReady, slots int) []frame.Bead {
	var beads []frame.Bead
	for side, prefix := range []string{"L", "R"} {
		ready := leftReady
		if side == 1 {
			ready = rightReady
		}
		for i := 0; i < slots; i++ {
			beads = append(beads, frame.Bead{Label: fmt.Sprintf("%s%d", prefix, i+1), Lit: i < ready, Dark: i >= ready})
		}
	}
	return beads
}
