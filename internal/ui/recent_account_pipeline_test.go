package ui

import (
	fynetest "fyne.io/fyne/v2/test"
	"image"
	"image/draw"
	"image/png"
	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/identity"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecentAccountScreenshotSelectsCorrectOpponentWithoutNinja(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Cleanup(func() { identity.SetMineNames(config.Default().UI.PlayerNames) })
	cfg := config.Default()
	cfg.UI.PlayerNames = []string{"白方小"}
	cfg.UI.PlayerSide = "auto"
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("inbox/regressions/duel-shino-right-account-20260907.png")
	if err != nil {
		t.Fatal(err)
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	at := time.Now()
	observed := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, at, at, "fixture")
	if observed.PlayerSide != "left" || observed.RightNinja != "" {
		t.Fatalf("account depended on ninja/OCR: %+v", observed)
	}
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	s := &session{cfg: cfg, provider: func() frame.Frame { return observed }}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.captureOnce()
	if s.side != "left" || !strings.HasPrefix(s.tag.Text, "对面·右") {
		t.Fatalf("wrong opponent side: %q %q", s.side, s.tag.Text)
	}
	if s.visibleReady(true) != "2" || s.visibleReady(false) != "2" {
		t.Fatalf("counts changed: %s/%s", s.visibleReady(true), s.visibleReady(false))
	}
}
