package ui

import (
	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"image/png"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOverlayTableCandidateIsExplicitAndCannotEnableDualClock(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := &session{cfg: config.Default(), side: "left", scene: "fight", fighting: true, layoutProfile: "duel", beads: testBeads(3, 2)}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.win.SetPadded(false)
	s.win.Resize(fyne.NewSize(280, 140))
	s.updateHUD(frame.Frame{RightNinjaCandidate: "奈良鹿丸", LeftSlots: 4, RightSlots: 4})
	s.refreshOverlay()
	s.refreshClockAt(time.Now())
	fitNativeOverlayPreview(s.win)
	if s.tag.Text != "对面·右 · 奈良鹿丸（待确认）" {
		t.Fatalf("unclear candidate label: %q", s.tag.Text)
	}
	if s.opponentNinja() != "" {
		t.Fatal("display-only candidate is cooldown evidence")
	}
	if s.win.Canvas().Capture().Bounds().Dx() != 280 {
		t.Fatal("candidate widened overlay")
	}
	if dir := os.Getenv("TIMER_UI_PREVIEW_DIR"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "table-name-candidate.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, s.win.Canvas().Capture())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	s.rightNinjaCandidate = ninja.FifthMizukage
	s.refreshClockAt(time.Now())
	if s.alternateBox.Visible() || s.opponentNinja() != "" {
		t.Fatal("candidate activated special rule")
	}
	s.updateHUD(frame.Frame{RightNinja: ninja.Naruto})
	s.refreshOverlay()
	if !strings.Contains(s.tag.Text, "鸣人·第六尾") || strings.Contains(s.tag.Text, "待确认") {
		t.Fatalf("confirmed exact variant hidden: %q", s.tag.Text)
	}
	s.updateHUD(frame.Frame{})
	s.refreshOverlay()
	if !strings.Contains(s.tag.Text, "忍者未确认") {
		t.Fatal("unknown recognition state hidden")
	}
}
