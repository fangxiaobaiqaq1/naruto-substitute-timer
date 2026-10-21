package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/config"
)

func findObject[T fyne.CanvasObject](root fyne.CanvasObject, match func(T) bool) (T, bool) {
	if v, ok := root.(T); ok && match(v) {
		return v, true
	}
	switch c := root.(type) {
	case *container.Scroll:
		return findObject(c.Content, match)
	case *fyne.Container:
		for _, child := range c.Objects {
			if found, ok := findObject(child, match); ok {
				return found, true
			}
		}
	}
	var zero T
	return zero, false
}

// GE-04: the export window says exactly what the support ZIP will contain,
// and account names stay out unless explicitly ticked.
func TestDiagnosticsWindowShowsSupportBundleContents(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	s := &session{cfg: config.Default(), done: make(chan struct{})}
	w := fynetest.NewTempWindow(t, widget.NewLabel(""))
	controls := s.diagnosticsControls(w)
	contents, ok := findObject(controls, func(l *widget.Label) bool { return strings.HasPrefix(l.Text, "支持诊断包将包含") })
	if !ok {
		t.Fatal("export window does not list the bundle contents")
	}
	for _, want := range []string{"代号", "%USERNAME%", "不含任何图像", "没有诊断会话"} {
		if !strings.Contains(contents.Text, want) {
			t.Fatalf("contents lack %q:\n%s", want, contents.Text)
		}
	}
	names, okNames := findObject(controls, func(c *widget.Check) bool { return strings.Contains(c.Text, "包含账号名") })
	images, okImages := findObject(controls, func(c *widget.Check) bool { return strings.Contains(c.Text, "原帧/回放图像") })
	if !okNames || !okImages || names.Checked || images.Checked {
		t.Fatal("privacy options missing or enabled by default")
	}
	names.SetChecked(true)
	images.SetChecked(true)
	if !strings.Contains(contents.Text, "账号名原文") || !strings.Contains(contents.Text, "原帧 PNG") || strings.Contains(contents.Text, "代号") {
		t.Fatalf("contents not updated for the opt-ins:\n%s", contents.Text)
	}
}
