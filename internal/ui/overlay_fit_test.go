package ui

import (
	"math"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"narutotimer/internal/config"
	"narutotimer/internal/ninja"
)

// Fyne's native GLFW Resize/fitContent enforces Content.MinSize; its software
// test window does not. Mirror that behavior before checking/capturing an
// unpadded overlay, rather than approving a preview smaller than a real window.
func fitNativeOverlayPreview(w fyne.Window) {
	size := w.Canvas().Size().Max(w.Content().MinSize())
	w.Resize(fyne.NewSize(float32(math.Ceil(float64(size.Width))), float32(math.Ceil(float64(size.Height)))))
}

func overlayButton(root fyne.CanvasObject, label string) *widget.Button {
	if button, ok := root.(*widget.Button); ok && button.Text == label {
		return button
	}
	if scroll, ok := root.(*container.Scroll); ok {
		return overlayButton(scroll.Content, label)
	}
	if group, ok := root.(*fyne.Container); ok {
		for _, child := range group.Objects {
			if found := overlayButton(child, label); found != nil {
				return found
			}
		}
	}
	return nil
}

func TestCompactOverlayKeepsStatusAboveButtonsWhenClockChanges(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := &session{cfg: config.Default(), side: "left", scene: "fight", fighting: true, beads: testBeads(2, 3)}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.win.SetPadded(false)
	at := time.Now()
	s.right.Observe(4, true, at.Add(-time.Millisecond), s.cooldown(), 1)
	s.right.Observe(3, true, at, s.cooldown(), 1)
	for _, name := range []string{"", ninja.FifthMizukage, "照美冥[泳装]"} {
		s.win.Resize(fyne.NewSize(280, 120)) // current user's compact configuration
		s.cfg.UI.NinjaQuery = name
		s.refreshOverlay()
		s.refreshClockAt(at.Add(time.Second))
		fitNativeOverlayPreview(s.win)
		button := overlayButton(s.win.Content(), "设置")
		if button == nil {
			t.Fatal("missing settings button")
		}
		info, _ := overlayObjectPosition(s.win.Content(), s.info, fyne.NewPos(0, 0))
		footer, _ := overlayObjectPosition(s.win.Content(), button, fyne.NewPos(0, 0))
		if bottom := info.Y + s.info.MinSize().Height; bottom > footer.Y {
			t.Errorf("%q status overlaps footer: bottom=%.2f footer=%.2f canvas=%v minimum=%v", name, bottom, footer.Y, s.win.Canvas().Size(), s.overlay.MinSize())
		}
	}
}
