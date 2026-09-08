package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/frame"
)

func replayTestConfig() config.Config {
	cfg := config.Default()
	cfg.Layout.BeadsPerSide = 4
	cfg.UI.PollIntervalMS = 33
	cfg.Tracking.EnterFightFrames = 1
	cfg.Tracking.MinimumConfirmFrames = 2
	cfg.Scene.HoldScenes = []string{"vs"}
	cfg.Scene.EndScenes = []string{"lobby"}
	return cfg
}

func replayFight(at time.Time, left, right int) frame.Frame {
	f := frame.Frame{CapturedAt: at, Fighting: true, Scene: "fight", LayoutProfile: "camp"}
	for side, ready := range []int{left, right} {
		for i := 1; i <= 4; i++ {
			f.Beads = append(f.Beads, frame.Bead{
				Label: fmt.Sprintf("%c%d", "LR"[side], i), Lit: ready >= i,
				Dark: ready >= 0 && ready < i, Unknown: ready < 0,
			})
		}
	}
	return f
}

func TestKnownCountRejectsAmbiguousStatesAndAcceptsGold(t *testing.T) {
	cases := []struct {
		name string
		bead frame.Bead
		want int
	}{
		{"lit", frame.Bead{Lit: true}, 1},
		{"gold", frame.Bead{Gold: true}, 1},
		{"lit gold", frame.Bead{Lit: true, Gold: true}, 1},
		{"dark", frame.Bead{Dark: true}, 0},
		{"unknown", frame.Bead{Unknown: true, Lit: true}, -1},
		{"no state", frame.Bead{}, -1},
		{"lit dark", frame.Bead{Lit: true, Dark: true}, -1},
		{"gold dark", frame.Bead{Gold: true, Dark: true}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.bead
			b.Label = "L1"
			got := knownCount([]frame.Bead{b, {Label: "R1", Lit: true}}, 'L', 1)
			if tc.want < 0 {
				if got != nil {
					t.Fatalf("ambiguous state reported as %d ready", *got)
				}
			} else if got == nil || *got != tc.want {
				t.Fatalf("got %v, want %d", got, tc.want)
			}
		})
	}
	for _, expected := range []int{0, 2, 4} {
		if knownCount([]frame.Bead{{Label: "L1", Lit: true}}, 'L', expected) != nil {
			t.Fatalf("accepted incomplete/invalid expected count %d", expected)
		}
	}
}

func TestReplayHoldResynchronizesEachSideWhenKnown(t *testing.T) {
	tracker := newReplayTracker(replayTestConfig(), 15*time.Second)
	base := time.Unix(1700000000, 0)
	at := func(n int) time.Time { return base.Add(time.Duration(n) * 33 * time.Millisecond) }
	tracker.observe(replayFight(at(0), 4, 4))
	tracker.observe(replayFight(at(1), 3, 4))
	if events := tracker.observe(replayFight(at(2), 3, 4)); len(events) != 1 || events[0].side != "left" {
		t.Fatalf("expected initial left event, got %+v", events)
	}
	tracker.observe(frame.Frame{CapturedAt: at(3), Scene: "vs", Hold: true})
	if events := tracker.observe(replayFight(at(4), 2, -1)); len(events) != 0 {
		t.Fatalf("hold return invented an event: %+v", events)
	}
	if tracker.syncLeft || !tracker.syncRight || tracker.left.LastReady() != 3 {
		t.Fatal("known left and unknown right did not retain independent sync state")
	}
	for _, n := range []int{5, 6} {
		events := tracker.observe(replayFight(at(n), 2, 3))
		if len(events) != 0 {
			t.Fatalf("each side must independently calibrate without inventing a hidden decrease, got %+v", events)
		}
	}
	if tracker.syncRight || tracker.right.LastReady() != 3 || !tracker.left.Active() {
		t.Fatal("hold synchronization lost the existing clock or right baseline")
	}
	tracker.observe(replayFight(at(7), 2, 2))
	events := tracker.observe(replayFight(at(8), 2, 2))
	if len(events) != 1 || events[0].side != "right" || !events[0].firstObserved.Equal(at(7)) {
		t.Fatalf("real post-sync decrease missing or incorrectly timestamped: %+v", events)
	}
}

func TestReplayDuplicateCannotConfirmOrConsumePendingSync(t *testing.T) {
	tracker := newReplayTracker(replayTestConfig(), 15*time.Second)
	base := time.Unix(1700000000, 0)
	at := func(n int) time.Time { return base.Add(time.Duration(n) * 33 * time.Millisecond) }
	tracker.observe(replayFight(at(0), 4, 4))
	tracker.observe(replayFight(at(1), 3, 4))
	duplicate := replayFight(at(2), 3, 4)
	duplicate.Duplicate = true
	if events := tracker.observe(duplicate); len(events) != 0 || tracker.left.Active() {
		t.Fatal("duplicate confirmed a pending decrease")
	}
	if events := tracker.observe(replayFight(at(3), 3, 4)); len(events) != 1 || !events[0].firstObserved.Equal(at(1)) {
		t.Fatalf("next independent frame did not confirm the original observation: %+v", events)
	}
	tracker.observe(frame.Frame{CapturedAt: at(4), Hold: true, Scene: "vs"})
	duplicate = replayFight(at(5), 2, 3)
	duplicate.Duplicate = true
	tracker.observe(duplicate)
	if !tracker.syncLeft || !tracker.syncRight {
		t.Fatal("duplicate return consumed pending hold synchronization")
	}
	for _, n := range []int{6, 7} {
		events := tracker.observe(replayFight(at(n), 2, 3))
		if len(events) != 0 {
			t.Fatalf("fresh post-boundary observations calibrate, never infer hidden decreases: %+v", events)
		}
	}
}

func TestReplayHonorsEntryAndExitHysteresis(t *testing.T) {
	for _, leave := range []int{2, 12} {
		t.Run(fmt.Sprintf("leave-%d", leave), func(t *testing.T) {
			cfg := replayTestConfig()
			cfg.Tracking.EnterFightFrames = 3
			cfg.Tracking.LeaveFightFrames = leave
			tracker := newReplayTracker(cfg, 15*time.Second)
			base := time.Unix(1700000000, 0)
			at := func(n int) time.Time { return base.Add(time.Duration(n) * 33 * time.Millisecond) }
			tracker.observe(replayFight(at(0), 4, 4))
			tracker.observe(replayFight(at(1), 3, 4))
			if tracker.fighting || tracker.left.LastReady() != 0 {
				t.Fatal("observed beads before the configured entry threshold")
			}
			tracker.observe(replayFight(at(2), 3, 4))
			if !tracker.fighting || tracker.left.LastReady() != 3 || tracker.left.Active() {
				t.Fatal("entry must establish a baseline without a synthetic decrease")
			}
			tracker.observe(replayFight(at(3), 2, 4))
			tracker.observe(replayFight(at(4), 2, 4))
			if !tracker.left.Active() {
				t.Fatal("setup event was not confirmed")
			}
			threshold := max(10, leave)
			for i := 1; i < threshold; i++ {
				tracker.observe(frame.Frame{CapturedAt: at(4 + i), Scene: "lobby"})
			}
			// Unknown animation interrupts the exit streak, not the match clock.
			tracker.observe(frame.Frame{CapturedAt: at(4 + threshold), Scene: "unknown"})
			if !tracker.fighting || !tracker.left.Active() || tracker.leaveHits != 0 {
				t.Fatal("unknown scene did not interrupt exit votes while preserving clock")
			}
			for i := 1; i <= threshold; i++ {
				tracker.observe(frame.Frame{CapturedAt: at(4 + threshold + i), Scene: "lobby"})
				if i < threshold && !tracker.fighting {
					t.Fatal("fresh exit streak ended early")
				}
			}
			if tracker.fighting || tracker.left.Active() || tracker.right.Active() {
				t.Fatal("exit threshold failed to reset both clocks")
			}
		})
	}
}

func TestReplayProfileSwitchResetsClocksAndReentersFight(t *testing.T) {
	cfg := replayTestConfig()
	cfg.Tracking.EnterFightFrames = 2
	tracker := newReplayTracker(cfg, 15*time.Second)
	base := time.Unix(1700000000, 0)
	at := func(n int) time.Time { return base.Add(time.Duration(n) * 33 * time.Millisecond) }
	tracker.observe(replayFight(at(0), 4, 4))
	tracker.observe(replayFight(at(1), 4, 4))
	tracker.observe(replayFight(at(2), 3, 4))
	tracker.observe(replayFight(at(3), 3, 4))
	if !tracker.left.Active() {
		t.Fatal("setup clock is inactive")
	}
	for n := 4; n <= 6; n++ {
		f := replayFight(at(n), 2, 4)
		f.LayoutProfile = "duel"
		if events := tracker.observe(f); len(events) != 0 || tracker.left.Active() {
			t.Fatalf("profile switch preserved or invented a clock: %+v", events)
		}
		if n == 4 && tracker.fighting {
			t.Fatal("profile switch bypassed entry confirmation")
		}
	}
	if !tracker.fighting || tracker.layoutProfile != "duel" || tracker.left.LastReady() != 2 {
		t.Fatal("new profile did not establish its independent baseline")
	}
}

func TestReplaySequenceDetectsGapsAndRejectsInvalidOrder(t *testing.T) {
	var sequence replaySequence
	for _, tc := range []struct {
		index   int
		offset  float64
		missing int
	}{{42, 0, 0}, {43, 33, 0}, {46, 66, 2}, {47, 66, 0}} {
		missing, err := sequence.advance(sample{Index: tc.index, OffsetMS: tc.offset})
		if err != nil || missing != tc.missing {
			t.Fatalf("sample %d: missing=%d, err=%v; want missing=%d", tc.index, missing, err, tc.missing)
		}
	}
	for _, tc := range []struct {
		name  string
		next  sample
		error string
	}{
		{"missing index", sample{OffsetMS: 99}, "must be positive"},
		{"negative index", sample{Index: -1, OffsetMS: 99}, "must be positive"},
		{"duplicate index", sample{Index: 47, OffsetMS: 99}, "must strictly increase"},
		{"backwards index", sample{Index: 46, OffsetMS: 99}, "must strictly increase"},
		{"backwards timestamp", sample{Index: 48, OffsetMS: 65}, "timestamps go backwards"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := sequence
			if _, err := copy.advance(tc.next); err == nil || !strings.Contains(err.Error(), tc.error) {
				t.Fatalf("got %v, want error containing %q", err, tc.error)
			}
			if copy != sequence {
				t.Fatal("rejected sample changed the sequence baseline")
			}
		})
	}
}

func TestReplayMissingIndexRebuildsBaselineWithoutInventingDrop(t *testing.T) {
	tracker := newReplayTracker(replayTestConfig(), 15*time.Second)
	var sequence replaySequence
	base := time.Unix(1700000000, 0)
	at := func(index int) time.Time { return base.Add(time.Duration(index) * 33 * time.Millisecond) }
	observe := func(index, left, right int, duplicate bool) []replayEvent {
		t.Helper()
		missing, err := sequence.advance(sample{Index: index, OffsetMS: float64(index * 33)})
		if err != nil {
			t.Fatal(err)
		}
		if missing > 0 {
			tracker.resyncAfterRecordingGap()
		}
		f := replayFight(at(index), left, right)
		f.Duplicate = duplicate
		return tracker.observe(f)
	}
	observe(1, 4, 4, false)
	observe(2, 4, 3, false)
	if events := observe(3, 4, 3, false); len(events) != 1 || events[0].side != "right" {
		t.Fatalf("setup event missing: %+v", events)
	}
	// Index 4 starts a left decrease; index 5 is absent. The 66 ms gap is
	// below the normal time limit, but cannot supply a second confirmation.
	if events := observe(4, 3, 3, false); len(events) != 0 {
		t.Fatalf("single drop frame confirmed too early: %+v", events)
	}
	if events := observe(6, 3, 3, true); len(events) != 0 {
		t.Fatalf("duplicate after gap created an event: %+v", events)
	}
	if tracker.left.LastReady() != 4 {
		t.Fatal("duplicate after gap consumed the fresh baseline")
	}
	for _, index := range []int{7, 8} {
		if events := observe(index, 3, 3, false); len(events) != 0 || tracker.left.Active() {
			t.Fatalf("inferred an unseen decrease across the manifest gap at %d: %+v", index, events)
		}
	}
	if tracker.left.LastReady() != 3 || !tracker.right.Active() || tracker.right.EventCount() != 1 {
		t.Fatal("gap failed to rebuild the left baseline or erased the confirmed right cooldown")
	}
	observe(9, 2, 3, false)
	if events := observe(10, 2, 3, false); len(events) != 1 || events[0].side != "left" || !events[0].firstObserved.Equal(at(9)) {
		t.Fatalf("fresh decrease after the new baseline was lost or mistimed: %+v", events)
	}
}

func TestReplayMissingIndexInterruptsSceneAdmission(t *testing.T) {
	cfg := replayTestConfig()
	cfg.Tracking.EnterFightFrames = 3
	tracker := newReplayTracker(cfg, 15*time.Second)
	base := time.Unix(1700000000, 0)
	at := func(index int) time.Time { return base.Add(time.Duration(index) * 33 * time.Millisecond) }
	tracker.observe(replayFight(at(1), 4, 4))
	tracker.observe(replayFight(at(2), 4, 4))
	tracker.resyncAfterRecordingGap()
	for _, index := range []int{4, 5} {
		tracker.observe(replayFight(at(index), 3, 4))
		if tracker.fighting {
			t.Fatal("fight entry votes crossed a missing manifest sample")
		}
	}
	tracker.observe(replayFight(at(6), 3, 4))
	if !tracker.fighting || tracker.left.LastReady() != 3 || tracker.left.Active() {
		t.Fatal("fresh scene streak failed to establish the new match baseline")
	}
}
