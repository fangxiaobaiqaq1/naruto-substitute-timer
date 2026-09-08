package ui

import (
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"narutotimer/internal/catalog"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

func TestOnlyFifthMizukageShowsSameEventDualClocks(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := &session{cfg: config.Default(), side: "left"}
	s.cfg.UI.NinjaQuery = ninja.FifthMizukage
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.win.SetPadded(false)
	s.win.Resize(fyne.NewSize(280, 120))
	s.scene, s.fighting, s.beads = "fight", true, testBeads(2, 3)
	s.refreshOverlay()
	at := time.Now()
	s.right.Observe(4, true, at.Add(-16*time.Millisecond), s.cooldown(), 2)
	s.right.Observe(3, true, at, s.cooldown(), 2)
	s.refreshClockAt(at)
	if s.altCD.Text != "—" || s.cd.Text != "—" {
		t.Fatal("unconfirmed drop started clocks")
	}
	s.right.Observe(3, true, at.Add(16*time.Millisecond), s.cooldown(), 2)
	for _, tc := range []struct {
		elapsed   time.Duration
		main, alt string
	}{
		{16 * time.Millisecond, "14.9", "9.9"}, {5 * time.Second, "10.0", "5.0"}, {10 * time.Second, "5.0", "就绪"}, {15 * time.Second, "—", "就绪"},
	} {
		s.refreshClockAt(at.Add(tc.elapsed))
		fitNativeOverlayPreview(s.win)
		if s.cd.Text != tc.main || s.altCD.Text != tc.alt || !s.alternateBox.Visible() || s.eventTag.Text != "第 1 次" {
			t.Fatalf("at %v: %q/%q event %q", tc.elapsed, s.cd.Text, s.altCD.Text, s.eventTag.Text)
		}
		for _, obj := range []fyne.CanvasObject{s.cd, s.altCD, s.eventTag, s.info} {
			pos, found := overlayObjectPosition(s.win.Content(), obj, fyne.NewPos(0, 0))
			size := obj.MinSize()
			if !found || pos.X < 0 || pos.Y < 0 || pos.X+size.Width > float32(s.win.Canvas().Capture().Bounds().Dx()) || pos.Y+size.Height > float32(s.win.Canvas().Capture().Bounds().Dy()) {
				t.Fatalf("clipped live dual display: pos=%v size=%v", pos, size)
			}
		}
	}
	// A second event starts both candidates afresh, with one increment.
	s.right.Observe(2, true, at.Add(16*time.Second), s.cooldown(), 2)
	s.right.Observe(2, true, at.Add(16*time.Second+16*time.Millisecond), s.cooldown(), 2)
	s.refreshClockAt(at.Add(16*time.Second + 16*time.Millisecond))
	fitNativeOverlayPreview(s.win)
	if s.cd.Text != "14.9" || s.altCD.Text != "9.9" || s.eventTag.Text != "第 2 次" {
		t.Fatal("second substitute did not replace both candidates")
	}
	if output := os.Getenv("TIMER_UI_PREVIEW_DIR"); output != "" {
		if err := os.MkdirAll(output, 0755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(output, "fifth-mizukage-dual.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, s.win.Canvas().Capture())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"照美冥", "照美冥[泳装]", ninja.Hashirama, "980230", "908290"} {
		s.cfg.UI.NinjaQuery = name
		s.refreshClockAt(at.Add(17 * time.Second))
		if s.alternateBox.Visible() {
			t.Fatalf("wrong version has alternate: %q", name)
		}
		if s.cd.Text != "14.0" {
			t.Fatalf("wrong primary duration for %s: %s", name, s.cd.Text)
		}
	}
}

func TestSpecialClockUsesOpponentNotOwnEvent(t *testing.T) {
	s := &session{cfg: config.Default(), side: "left"}
	s.cfg.UI.NinjaQuery = ninja.FifthMizukage
	at := time.Now()
	s.left.Observe(4, true, at, 15*time.Second, 1)
	s.left.Observe(3, true, at.Add(time.Millisecond), 15*time.Second, 1)
	if dual, text, _ := s.dualClockText(at.Add(time.Second)); !dual || text != "—" {
		t.Fatal("own substitute started opponent's ten-second clock")
	}
	s.side = "right"
	if _, text, _ := s.dualClockText(at.Add(time.Second)); text != "9.0" {
		t.Fatalf("manual side switch selected wrong clock: %s", text)
	}
	s.side = ""
	if dual, _, _ := s.dualClockText(at); dual {
		t.Fatal("unknown side displayed a guessed opponent")
	}
}

func TestLegacyTablesCannotChangeNormalPrimaryCooldown(t *testing.T) {
	s := &session{cfg: config.Default(), subs: &catalog.Table{Default: 10}}
	s.cfg.UI.SubstituteCooldownSeconds = 10
	for _, name := range []string{"980230", "908290", ninja.Hashirama, ninja.FifthMizukage, ""} {
		s.cfg.UI.NinjaQuery = name
		if s.cooldown() != 15*time.Second {
			t.Fatalf("old table changed current policy for %q", name)
		}
	}
}

func sixBeads(lit int) []frame.Bead {
	var out []frame.Bead
	for i := range 6 {
		out = append(out, frame.Bead{Label: string([]byte{'L', byte('1' + i)}), Lit: i < lit, Dark: i >= lit})
	}
	for i := range 4 {
		out = append(out, frame.Bead{Label: string([]byte{'R', byte('1' + i)}), Lit: true})
	}
	return out
}

func TestMixedSlotTopologiesDoNotInventSubstitutes(t *testing.T) {
	s := &session{cfg: config.Default()}
	at := time.Now()
	feed := func(offset time.Duration, lit int) {
		f := frame.Frame{CapturedAt: at.Add(offset), Fighting: true, Scene: "fight", LeftSlots: 6, RightSlots: 4, LeftNinja: ninja.Hashirama, Beads: sixBeads(lit)}
		s.updateHUD(f)
		s.commitBeads(f.Beads)
		s.observeBeads(f)
	}
	feed(0, 6)
	feed(16*time.Millisecond, 5)
	feed(32*time.Millisecond, 5)
	if s.left.EventCount() != 1 || s.right.EventCount() != 0 {
		t.Fatalf("six-slot drop count: %d/%d", s.left.EventCount(), s.right.EventCount())
	}
	if got := s.left.LatestRemaining(at.Add(32 * time.Millisecond)); len(got) != 1 || math.Abs(got[0]-14.984) > 1e-6 {
		t.Fatalf("six-slot timer %v", got)
	}
	if s.visibleReady(true) != "5/6" || s.visibleReady(false) != "4/4" {
		t.Fatalf("topology not shown: %s %s", s.visibleReady(true), s.visibleReady(false))
	}
	// A six->four character swap is not a 5->4 substitute.
	f := frame.Frame{CapturedAt: at.Add(48 * time.Millisecond), Fighting: true, LeftSlots: 4, RightSlots: 4, Beads: testBeads(4, 4)}
	s.updateHUD(f)
	s.observeBeads(f)
	f.CapturedAt = at.Add(64 * time.Millisecond)
	s.observeBeads(f)
	if s.left.EventCount() != 1 {
		t.Fatal("six->four topology change counted as substitute")
	}
}
