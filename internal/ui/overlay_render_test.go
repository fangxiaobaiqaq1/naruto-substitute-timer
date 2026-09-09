package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	fynetest "fyne.io/fyne/v2/test"

	"narutotimer/internal/config"
)

// TIMER_UI_PREVIEW_DIR optionally saves the same software-rendered images used
// for layout checks. No native window or game capture is created by this test.
func TestOverlayRenderReadable(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	for _, state := range []struct {
		name          string
		lost          bool
		unknownSide   bool
		unknownBeans  bool
		clock, number string
	}{
		{name: "normal", clock: "12.4", number: "第 1 次"},
		{name: "unknown", lost: true, clock: "12.4", number: "第 1 次"},
		{name: "second-substitute", clock: "14.9", number: "第 2 次"},
		{name: "number-after-expiry", clock: "—", number: "第 2 次"},
		{name: "unknown-side", unknownSide: true, clock: "—", number: "左12次 / 右12次"},
		{name: "camp-unknown-left-beans", unknownBeans: true, clock: "12.4", number: "第 1 次"},
	} {
		t.Run(state.name, func(t *testing.T) {
			s := &session{cfg: config.Default(), side: "left", scene: "fight", fighting: true, beads: testBeads(2, 2)}
			if state.lost {
				s.scene, s.hold, s.beads = "", true, nil
			}
			if state.unknownBeans {
				s.layoutProfile = "camp"
				s.beads[0].Unknown = true
			}
			content := s.overlayContent()
			s.tag.Text = "对面·右"
			if state.unknownSide {
				s.side = ""
				s.tag.Text = "对面·待认边"
			}
			s.info.Text = s.statusLine()
			// A confirmed cooldown remains visible while observations are unknown.
			s.cd.Text = state.clock
			s.eventTag.Text = state.number
			s.cd.Color = clockLive
			w := fynetest.NewTempWindow(t, content)
			w.SetPadded(false)
			w.Resize(fyne.NewSize(280, 140))
			img := w.Canvas().Capture()
			if img.Bounds().Dx() != 280 {
				t.Fatalf("280px overlay expanded horizontally to %v", img.Bounds())
			}
			for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
				for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
					_, _, _, alpha := img.At(x, y).RGBA()
					if alpha != 0xffff {
						t.Fatalf("background is not opaque at (%d,%d): %d", x, y, alpha)
					}
				}
			}
			previousBottom := float32(0)
			for _, label := range []*canvas.Text{s.tag, s.cd, s.info} {
				pos, found := overlayObjectPosition(content, label, fyne.NewPos(0, 0))
				if !found {
					t.Fatalf("text %q is missing from overlay", label.Text)
				}
				size := label.MinSize()
				if pos.X < 0 || pos.Y < previousBottom || pos.X+size.Width > float32(img.Bounds().Dx()) || pos.Y+size.Height > float32(img.Bounds().Dy()) {
					t.Fatalf("text %q is clipped or overlapping: pos=%v min=%v canvas=%v previousBottom=%v", label.Text, pos, size, img.Bounds(), previousBottom)
				}
				previousBottom = pos.Y + size.Height
			}
			clockPos, _ := overlayObjectPosition(content, s.cd, fyne.NewPos(0, 0))
			numberPos, found := overlayObjectPosition(content, s.eventTag, fyne.NewPos(0, 0))
			numberSize := s.eventTag.MinSize()
			if !found || numberPos.X < clockPos.X+s.cd.MinSize().Width || numberPos.X+numberSize.Width > float32(img.Bounds().Dx()) || numberPos.Y < 0 || numberPos.Y+numberSize.Height > float32(img.Bounds().Dy()) {
				t.Fatalf("event number is clipped or covers the clock: %v %v", numberPos, numberSize)
			}
			if output := os.Getenv("TIMER_UI_PREVIEW_DIR"); output != "" {
				if err := os.MkdirAll(output, 0755); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(output, "user-report-ui-preview-"+state.name+".png")
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(file, img)
				closeErr := file.Close()
				if err != nil {
					t.Fatal(err)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
				t.Logf("saved %s (%dx%d)", path, img.Bounds().Dx(), img.Bounds().Dy())
			}
		})
	}
}

func overlayObjectPosition(root, target fyne.CanvasObject, parent fyne.Position) (fyne.Position, bool) {
	position := parent.Add(root.Position())
	if root == target {
		return position, true
	}
	if group, ok := root.(*fyne.Container); ok {
		for _, child := range group.Objects {
			if found, ok := overlayObjectPosition(child, target, position); ok {
				return found, true
			}
		}
	}
	return fyne.Position{}, false
}
