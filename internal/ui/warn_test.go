package ui

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"narutotimer/internal/config"
	"narutotimer/internal/sound"
)

func TestWarnOptionsAreOffByDefault(t *testing.T) {
	if !config.Default().UI.ShowNotice {
		t.Fatal("launch notice must default on")
	}
	ui := config.Default().UI
	if ui.WarnRed || ui.WarnSound || ui.WarnSoundFile != "" {
		t.Fatalf("last-3-seconds options must default off: %+v", ui)
	}
}

func TestLastThreeSecondsRedAndSingleReminder(t *testing.T) {
	for _, tc := range []struct {
		name       string
		red, beep  bool
		wantColor  any
		wantSounds int
	}{
		{"default", false, false, clockWarn, 0},
		{"red", true, false, clockAlert, 0},
		{"sound", false, true, clockWarn, 1},
		{"both", true, true, clockAlert, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := fynetest.NewApp()
			t.Cleanup(a.Quit)
			a.Settings().SetTheme(newChromaTheme())
			played := make(chan string, 8)
			old := playWarnSound
			playWarnSound = func(path string) { played <- path }
			t.Cleanup(func() { playWarnSound = old })
			s := &session{cfg: config.Default(), side: "left"}
			s.cfg.UI.WarnRed, s.cfg.UI.WarnSound = tc.red, tc.beep
			s.win = fynetest.NewTempWindow(t, s.overlayContent())
			s.scene, s.fighting, s.beads = "fight", true, testBeads(2, 3)
			at := time.Now()
			s.right.Observe(4, true, at.Add(-16*time.Millisecond), s.cooldown(), 2)
			s.right.Observe(3, true, at, s.cooldown(), 2)
			s.right.Observe(3, true, at.Add(16*time.Millisecond), s.cooldown(), 2)
			for _, step := range []struct {
				elapsed time.Duration
				warn    bool
			}{{time.Second, false}, {12 * time.Second, true}, {12500 * time.Millisecond, true}, {13 * time.Second, true}, {16 * time.Second, false}} {
				s.refreshClockAt(at.Add(step.elapsed))
				if dir := os.Getenv("TIMER_UI_PREVIEW_DIR"); dir != "" && step.elapsed == 12500*time.Millisecond {
					s.win.SetPadded(false)
					s.win.Resize(fyne.NewSize(280, 140))
					if f, err := os.Create(filepath.Join(dir, "overlay-last3-"+tc.name+".png")); err == nil {
						_ = png.Encode(f, s.win.Canvas().Capture())
						f.Close()
					}
				}
				if step.warn && s.cd.Color != tc.wantColor {
					t.Fatalf("at %v colour %v, want %v", step.elapsed, s.cd.Color, tc.wantColor)
				}
				if !step.warn && (s.cd.Color == clockWarn || s.cd.Color == clockAlert) {
					t.Fatalf("at %v warned early/late: %v", step.elapsed, s.cd.Color)
				}
			}
			time.Sleep(20 * time.Millisecond)
			if len(played) != tc.wantSounds {
				t.Fatalf("reminders=%d, want %d (once per countdown)", len(played), tc.wantSounds)
			}
		})
	}
}

func TestWarnSoundImportValidatesAndCopies(t *testing.T) {
	dir := t.TempDir()
	if _, err := sound.Import(strings.NewReader("x"), "a.ogg", dir); err == nil {
		t.Fatal("unsupported extension accepted")
	}
	if _, err := sound.Import(strings.NewReader("not a wave file"), "a.wav", dir); err == nil {
		t.Fatal("non-RIFF WAV accepted")
	}
	if _, err := sound.Import(bytes.NewReader(make([]byte, sound.MaxImportBytes+1)), "a.mp3", dir); err == nil {
		t.Fatal("oversized file accepted")
	}
	got, err := sound.Import(bytes.NewReader(sound.DefaultWAV), "提示.WAV", dir)
	if err != nil || filepath.Base(got) != "warn-sound.wav" {
		t.Fatalf("import: %q %v", got, err)
	}
	if b, _ := os.ReadFile(got); !bytes.Equal(b, sound.DefaultWAV) {
		t.Fatal("imported copy differs")
	}
	// Importing an MP3 replaces the previous WAV rather than piling up files.
	if got, err = sound.Import(strings.NewReader("ID3..."), "b.mp3", dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != "warn-sound.mp3" {
		t.Fatalf("stale imports kept: %v", entries)
	}
}

func TestWarnSettingsPreview(t *testing.T) {
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(newChromaTheme())
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json")}
	w := fynetest.NewTempWindow(t, nil)
	w.SetContent(sectionCard("倒计时提醒（可选）", s.warnControls(w)))
	w.Resize(fyne.NewSize(460, w.Content().MinSize().Height))
	if dir := os.Getenv("TIMER_UI_PREVIEW_DIR"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "warn-settings.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, w.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWarnSecondsIsConfigurable(t *testing.T) {
	if config.Default().UI.WarnSeconds != 3 {
		t.Fatal("default reminder must stay 3.0 s")
	}
	for text, want := range map[string]float64{"3": 3, "2.5": 2.5, " 0.1 ": 0.1, "15": 15, "4，5": 4.5} {
		if got, err := parseWarnSeconds(text); err != nil || got != want {
			t.Fatalf("%q -> %v %v", text, got, err)
		}
	}
	for _, bad := range []string{"", "0", "15.1", "2.55", "abc", "-1", "NaN"} {
		if _, err := parseWarnSeconds(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	cfg := config.Default()
	for _, v := range []float64{0, 15.5, 2.55} {
		cfg.UI.WarnSeconds = v
		if config.Validate(cfg) == nil {
			t.Fatalf("config accepted warnSeconds=%v", v)
		}
	}
	// 5.0 s threshold: amber from 5.0, not at 5.1.
	for _, tc := range []struct {
		left float64
		want any
	}{{5.15, clockLive}, {5.0, clockWarn}, {0.4, clockWarn}} {
		if got := clockColor([]float64{tc.left}, warnTenths(5)); got != tc.want {
			t.Fatalf("%.2f s left: %v want %v", tc.left, got, tc.want)
		}
	}
}
