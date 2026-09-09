package rgb

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

func TestNativePurpleIdleSweepFrames(t *testing.T) {
	files, err := filepath.Glob("../../../debug/camp-flicker-20260909-frames/frame-*.png")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("local native purple idle sweep evidence is not distributed")
	}
	remaining, err := filepath.Glob("../../../debug/camp-flicker-20260909-remaining/frame-*.png")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, remaining...)
	cfg := config.Default()
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	e.Prefer("camp")
	for i, file := range files {
		img := loadRGBA(t, file)
		got := e.AnalyzeAt(img, time.Unix(1700000000, 0).Add(time.Duration(i)*20*time.Millisecond))
		left, right := countReady(got.Beads)
		if left != 4 || right != 4 || knownCount(got.Beads) != 8 || got.RightNinja != ninja.Obito {
			t.Errorf("%s: L%d/R%d, %+v", filepath.Base(file), left, right, got.Beads)
		}
	}
}

func TestPurpleHighlightNeedsFilledBodyInCurrentFrame(t *testing.T) {
	area := detect.ContentArea{W: 1600, H: 900}
	p := detect.BeadPosition{Bead: detect.Bead{Side: "right", Idx: 0}, X: 100, Y: 100}
	palette := [2]ninja.Readout{{}, {Name: ninja.Obito, Palette: ninja.Purple, Slots: 4}}
	purple := color.RGBA{255, 70, 255, 255}
	for _, kind := range []string{"white", "wash", "wash white core", "empty rim", "lower color under white cover"} {
		t.Run(kind, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
			switch kind {
			case "white", "lower color under white cover":
				draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
				if kind != "white" {
					draw.Draw(img, image.Rect(97, 103, 104, 109), image.NewUniform(purple), image.Point{}, draw.Src)
				}
			case "wash", "wash white core":
				draw.Draw(img, image.Rect(70, 70, 131, 131), image.NewUniform(purple), image.Point{}, draw.Src)
				if kind != "wash" {
					draw.Draw(img, image.Rect(97, 97, 104, 104), image.White, image.Point{}, draw.Src)
				}
			case "empty rim":
				for y := -10; y <= 10; y++ {
					for x := -8; x <= 8; x++ {
						if detect.InBeadDiamond(0, 0, 16, 20, x, y) && !detect.InBeadDiamond(0, 0, 12, 16, x, y) {
							img.SetRGBA(p.X+x, p.Y+y, purple)
						}
					}
				}
				draw.Draw(img, image.Rect(97, 97, 104, 104), image.White, image.Point{}, draw.Src)
			}
			if purpleGlintBody(img, p, 6, 7) {
				t.Fatal("unfilled or unbounded effect admitted as body")
			}
			got := sampleCalibratedSpecial(img, []detect.BeadPosition{p}, area, config.Default().Vision, palette)
			if got[0].Lit {
				t.Fatalf("effect voted as ready: %+v", got)
			}
		})
	}
}

func TestWhiteFlareOnRealEmptyPurpleSlotCannotAddBean(t *testing.T) {
	file := "../../../inbox/regressions/review-20260909/4.png"
	if _, err := os.Stat(file); os.IsNotExist(err) {
		t.Skip("local user screenshot is not distributed")
	}
	img := loadRGBA(t, file)
	cfg := config.Default()
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	area, ok := layout.ContentArea(img)
	if !ok {
		t.Fatal("native content area")
	}
	positions := layout.PositionsIn("camp", area)
	for _, p := range positions {
		if p.Side == "right" && p.Idx >= 2 {
			draw.Draw(img, image.Rect(p.X-1, p.Y-1, p.X+2, p.Y+2), image.White, image.Point{}, draw.Src)
		}
	}
	e := NewConfigured(layout, cfg.Vision)
	e.Prefer("camp")
	got := e.Analyze(img)
	for _, b := range got.Beads {
		if (b.Label == "R3" || b.Label == "R4") && b.Lit {
			t.Fatalf("empty slot gained vote: %+v", b)
		}
	}
}
