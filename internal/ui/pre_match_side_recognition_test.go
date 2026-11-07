package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	timerapp "narutotimer/internal/app"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
)

func TestPreMatchSideRecognitionConfigDefaultsAndRoundTrip(t *testing.T) {
	if config.Default().UI.PreMatchSideRecognition {
		t.Fatal("VS recognition must be opt-in")
	}
	example, err := config.Load("../../config.example.json")
	if err != nil || example.UI.PreMatchSideRecognition {
		t.Fatalf("example config: pre-match=%v err=%v", example.UI.PreMatchSideRecognition, err)
	}
	for _, autoText := range []bool{false, true} {
		t.Run(fmt.Sprintf("battleOCR=%v", autoText), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			legacy := fmt.Sprintf(`{"schemaVersion":1,"ui":{"autoTextRecognition":%t}}`, autoText)
			if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil || cfg.UI.PreMatchSideRecognition {
				t.Fatalf("legacy config enabled VS recognition: %+v, %v", cfg.UI, err)
			}
			for _, enabled := range []bool{true, false} {
				cfg.UI.PreMatchSideRecognition = enabled
				if err := config.Save(path, cfg); err != nil {
					t.Fatal(err)
				}
				loaded, err := config.Load(path)
				if err != nil || loaded.UI.PreMatchSideRecognition != enabled || loaded.UI.AutoTextRecognition != autoText {
					t.Fatalf("independent settings did not round-trip: %+v, %v", loaded.UI, err)
				}
				data, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(data), fmt.Sprintf(`"preMatchSideRecognition": %t`, enabled)) {
					t.Fatalf("missing explicit JSON field (including false): %s, %v", data, err)
				}
			}
		})
	}
}

func TestPreMatchSideRecognitionCheckboxAppliesAndPersistsIndependently(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), remember: true, side: "right"}
	s.cfg.UI.PlayerSide = "right"
	s.cfg.UI.AutoTextRecognition = false
	var applied, textApplied []bool
	WithPreMatchSideRecognitionControl(func(on bool) {
		// The hook runs outside the session lock, after the in-flight guard changes.
		s.mu.Lock()
		current := s.cfg.UI.PreMatchSideRecognition
		s.mu.Unlock()
		if current != on {
			t.Error("hook ran before the UI-side guard was updated")
		}
		applied = append(applied, on)
	})(s)
	WithTextRecognitionControl(func(on bool) { textApplied = append(textApplied, on) })(s)
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.openSettings()
	t.Cleanup(func() { s.settings.Close() })
	root := s.settingsTabs.Items[0].Content
	check, ok := findObject(root, func(c *widget.Check) bool { return c.Text == "开局/换人画面自动认边" })
	if !ok || check.Checked || len(applied) != 0 {
		t.Fatal("missing/default-on VS checkbox, or rendering invoked the hook")
	}
	help, ok := findObject(root, func(l *widget.Label) bool { return strings.Contains(l.Text, "精确匹配") })
	if !ok {
		t.Fatal("missing VS recognition help")
	}
	for _, want := range []string{"我的账号名", "保存", "不改手动选择", "独立于", "VS"} {
		if !strings.Contains(help.Text, want) {
			t.Fatalf("help lacks %q: %s", want, help.Text)
		}
	}
	for i, enabled := range []bool{true, false, true} {
		fynetest.Tap(check)
		loaded, err := config.Load(s.cfgPath)
		if err != nil || loaded.UI.PreMatchSideRecognition != enabled || s.cfg.UI.PreMatchSideRecognition != enabled {
			t.Fatalf("checkbox did not apply/persist immediately: %+v, %v", loaded.UI, err)
		}
		if len(applied) != i+1 || applied[i] != enabled || len(textApplied) != 0 || loaded.UI.AutoTextRecognition {
			t.Fatalf("VS checkbox used the wrong hook: VS=%v battle=%v", applied, textApplied)
		}
		if s.side != "right" || s.cfg.UI.PlayerSide != "right" || loaded.UI.PlayerSide != "right" {
			t.Fatal("VS checkbox changed the manual side")
		}
	}
	textCheck, ok := findObject(root, func(c *widget.Check) bool { return strings.HasPrefix(c.Text, "战斗画面识别名字") })
	if !ok {
		t.Fatal("missing independent battle OCR checkbox")
	}
	for i, enabled := range []bool{true, false} {
		fynetest.Tap(textCheck)
		loaded, err := config.Load(s.cfgPath)
		if err != nil || loaded.UI.AutoTextRecognition != enabled || !loaded.UI.PreMatchSideRecognition || !s.cfg.UI.PreMatchSideRecognition || !check.Checked {
			t.Fatalf("battle OCR checkbox changed VS recognition: %+v, %v", loaded.UI, err)
		}
		if len(applied) != 3 || len(textApplied) != i+1 || textApplied[i] != enabled {
			t.Fatalf("battle OCR checkbox used the wrong hook: VS=%v battle=%v", applied, textApplied)
		}
	}
	s.rebuildSettings()
	check, ok = findObject(s.settingsTabs.Items[0].Content, func(c *widget.Check) bool { return c.Text == "开局/换人画面自动认边" })
	if !ok || !check.Checked || len(applied) != 3 {
		t.Fatal("rebuilding settings lost the saved checkbox or invoked its hook")
	}
}

func TestPreMatchSideRecognitionSaveFailureStillApplies(t *testing.T) {
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "missing", "config.json")}
	var applied []bool
	WithPreMatchSideRecognitionControl(func(on bool) { applied = append(applied, on) })(s)
	for i, enabled := range []bool{true, false} {
		if err := s.setPreMatchSideRecognition(enabled); err == nil {
			t.Fatal("save failure was not returned")
		}
		if s.cfg.UI.PreMatchSideRecognition != enabled || len(applied) != i+1 || applied[i] != enabled {
			t.Fatal("save failure prevented the live toggle")
		}
	}
}

func TestPreMatchSideRecognitionRejectsInFlightVS(t *testing.T) {
	for _, hold := range []bool{false, true} {
		t.Run(fmt.Sprintf("hold=%v", hold), func(t *testing.T) {
			s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json")}
			s.cfg.UI.PlayerNames = []string{"我的账号"}
			s.cfg.UI.PreMatchSideRecognition = true
			s.maybeAutoSide(frame.Frame{Scene: "fight", PlayerSide: "left", PlayerName: "我的账号", OppName: "原对手"})
			before, beforeAt := s.autoIdentity, s.autoIdentityAt
			s.provider = func() frame.Frame {
				pending := frame.Frame{Scene: "vs", Hold: hold, PlayerSide: "right", PlayerName: "我的账号", OppName: "晚到的对手"}
				// captureOnce has already started; disable before it consumes the result.
				if err := s.setPreMatchSideRecognition(false); err != nil {
					t.Fatal(err)
				}
				return pending
			}
			s.captureOnce()
			if s.side != "left" || s.oppName != "原对手" || s.autoIdentity != before || !s.autoIdentityAt.Equal(beforeAt) {
				t.Fatal("disabled in-flight VS result changed the side or cached identity")
			}
		})
	}
}

func TestPreMatchSideRecognitionDisableDropsOnlyVSSourceCache(t *testing.T) {
	for _, scene := range []string{"vs", "fight"} {
		s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), remember: true, side: "left"}
		s.cfg.UI.PlayerSide = "left"
		s.cfg.UI.PlayerNames = []string{"我的账号"}
		s.cfg.UI.PreMatchSideRecognition = true
		s.maybeAutoSide(frame.Frame{Scene: scene, PlayerSide: "right", PlayerName: "我的账号"})
		// Current scene is not necessarily the origin of the remembered side.
		s.scene = "fight"
		if err := s.setPreMatchSideRecognition(false); err != nil {
			t.Fatal(err)
		}
		if s.side != "left" {
			t.Fatal("disabling VS recognition changed the manual side")
		}
		if err := s.selectSideMode("auto"); err != nil {
			t.Fatal(err)
		}
		want := ""
		if scene == "fight" {
			want = "right"
		}
		if s.side != want || s.autoIdentity.Side != want {
			t.Fatalf("source=%s: reused=%q cached=%q want=%q", scene, s.side, s.autoIdentity.Side, want)
		}
	}
}

func TestPreMatchSideRecognitionSwapPreservesClocks(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			at := time.Unix(1700000000, 0)
			frames := []frame.Frame{
				{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(4, 4), CapturedAt: at, PlayerSide: "left", PlayerName: "我的账号"},
				{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 3), CapturedAt: at.Add(20 * time.Millisecond)},
				{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(3, 3), CapturedAt: at.Add(40 * time.Millisecond)},
				{Hold: true, Scene: "vs", CapturedAt: at.Add(time.Second), PlayerSide: "right", PlayerName: "我的账号"},
				{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(2, 2), CapturedAt: at.Add(3 * time.Second)},
				{Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(2, 2), CapturedAt: at.Add(3020 * time.Millisecond)},
				{Fighting: true, Scene: "fight", LayoutProfile: "duel", RoundOpening: true, Beads: testBeads(4, 4), CapturedAt: at.Add(4 * time.Second)},
			}
			s := testSession(frames)
			s.cfgPath = filepath.Join(t.TempDir(), "config.json")
			s.cfg.UI.PlayerSide = "auto"
			s.cfg.UI.PlayerNames = []string{"我的账号"}
			s.cfg.UI.AutoTextRecognition = false
			s.cfg.UI.PreMatchSideRecognition = !enabled
			checkClocks := func(stage string, ready int) {
				t.Helper()
				for i, clock := range []*timerapp.SideClock{&s.left, &s.right} {
					first, _ := clock.LastEvent()
					if clock.EventCount() != 1 || !clock.Active() || !first.Equal(at.Add(20*time.Millisecond)) || clock.LastReady() != ready {
						t.Fatalf("%s changed clock %d: events=%d active=%v first=%v ready=%d", stage, i, clock.EventCount(), clock.Active(), first, clock.LastReady())
					}
				}
			}
			for range 3 {
				s.captureOnce()
			}
			checkClocks("before toggle", 3)
			if err := s.setPreMatchSideRecognition(enabled); err != nil {
				t.Fatal(err)
			}
			checkClocks("toggle", 3)
			s.captureOnce()
			checkClocks("VS", 3)
			wantSide := "left"
			if enabled {
				wantSide = "right"
			}
			if s.side != wantSide || !s.fighting || !s.holdFreeze || !s.inheritOnReturn || s.roundOpeningActive {
				t.Fatalf("VS changed match state or ignored side control: side=%q fighting=%v freeze=%v inherit=%v", s.side, s.fighting, s.holdFreeze, s.inheritOnReturn)
			}
			s.captureOnce()
			s.captureOnce()
			checkClocks("return", 2)
			for _, clock := range []*timerapp.SideClock{&s.left, &s.right} {
				if remaining := clock.Remaining(frames[5].CapturedAt); len(remaining) != 1 || remaining[0] != 12 {
					t.Fatalf("swap restarted cooldown: %v", remaining)
				}
			}
			// Only the existing verified round-opening marker resets round cooldowns.
			s.captureOnce()
			if !s.roundOpeningActive || s.left.Active() || s.right.Active() || s.left.EventCount() != 1 || s.right.EventCount() != 1 {
				t.Fatal("VS option changed verified round-opening behavior")
			}
		})
	}
}
