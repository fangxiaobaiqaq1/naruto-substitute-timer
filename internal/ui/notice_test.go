package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestLaunchNoticeContent(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	closed := false
	w := fynetest.NewTempWindow(t, nil)
	w.SetContent(noticeContent(w, func() { closed = true }))
	w.Resize(fyne.NewSize(420, w.Content().MinSize().Height))
	found, ok := false, (*widget.Button)(nil)
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *widget.Label:
			found = found || strings.Contains(v.Text, "永久开源免费")
		case *widget.Button:
			if v.Text == "我知道了" {
				ok = v
			}
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
		}
	}
	walk(w.Content())
	if !found || ok == nil {
		t.Fatalf("notice text/button missing: found=%v button=%v", found, ok != nil)
	}
	fynetest.Tap(ok)
	if !closed {
		t.Fatal("我知道了 did not dismiss the notice")
	}
	if dir := os.Getenv("TIMER_UI_PREVIEW_DIR"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "launch-notice.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, w.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
	}
}
