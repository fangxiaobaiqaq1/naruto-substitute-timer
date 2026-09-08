package ui

import (
	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"image/png"
	"narutotimer/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTextRecognitionToggleAppliesAndPersistsImmediately(t *testing.T) {
	var applied []bool
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), textControl: func(on bool) { applied = append(applied, on) }}
	for _, enabled := range []bool{false, true} {
		if err := s.setTextRecognition(enabled); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(s.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.UI.AutoTextRecognition != enabled || s.cfg.UI.AutoTextRecognition != enabled || applied[len(applied)-1] != enabled {
			t.Fatal("OCR checkbox did not update runtime and config")
		}
	}
	s.textStatus = "unavailable"
	if !strings.Contains(s.textStatusText(), "模板") {
		t.Fatal("OCR failure lacks fallback explanation")
	}
}

func TestSessionWaitStopsWithoutWaitingForNextCapture(t *testing.T) {
	s := &session{done: make(chan struct{})}
	close(s.done)
	if s.wait(time.Hour) {
		t.Fatal("closed UI retained background loops")
	}
}

func TestTextRecognitionSettingsRender(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := &session{cfg: config.Default(), side: "left", textStatus: "reading", cfgPath: filepath.Join(t.TempDir(), "config.json")}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.openSettings()
	t.Cleanup(func() {
		if s.settings != nil {
			s.settings.Close()
		}
	})
	s.settings.Resize(fyne.NewSize(380, 430))
	if s.textStatusLabel == nil || !strings.Contains(s.textStatusLabel.Text, "不等待") {
		t.Fatal("settings omitted background text status")
	}
	if output := os.Getenv("TIMER_UI_PREVIEW_DIR"); output != "" {
		if err := os.MkdirAll(output, 0755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(output, "background-text-settings.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, s.settings.Canvas().Capture())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
