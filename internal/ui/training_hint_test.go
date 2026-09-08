package ui

import (
	"testing"

	fynetest "fyne.io/fyne/v2/test"
	"narutotimer/internal/config"
)

func TestTrainingWithoutAccountAsksForSideInsteadOfGuessing(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	s := &session{cfg: config.Default(), scene: "fight", layoutProfile: "camp", fighting: true}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.refreshOverlay()
	if s.side != "" || s.tag.Text != "训练场·请选我方边" || s.cd.Text != "—" {
		t.Fatalf("missing training side hint or guessed a player: %q %q %q", s.side, s.tag.Text, s.cd.Text)
	}
	s.setSideModeLocked("left")
	s.refreshOverlay()
	if s.tag.Text != "对面·右 · 忍者未确认" {
		t.Fatalf("manual selection did not clear hint: %q", s.tag.Text)
	}
	s.setSideModeLocked("auto")
	s.layoutProfile = "duel"
	s.refreshOverlay()
	if s.tag.Text != "对面·待认边" {
		t.Fatalf("training hint leaked into duel: %q", s.tag.Text)
	}
}
