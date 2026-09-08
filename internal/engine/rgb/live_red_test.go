package rgb

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"

	xdraw "golang.org/x/image/draw"
)

func TestLiveCampNarutoRedMovingGlow(t *testing.T) {
	files, err := filepath.Glob("../../../inbox/regressions/camp-naruto-red-live-sequence-20260907/frame-*.png")
	if err != nil || len(files) != 10 {
		t.Fatalf("need ten original live frames: %d %v", len(files), err)
	}
	cfg := config.Default()
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	e.Prefer("camp")
	for _, path := range files {
		got := e.Analyze(loadRGBA(t, path))
		l, r := countReady(got.Beads)
		if got.LeftNinja != ninja.Naruto || l != 4 || r != 2 || knownCount(got.Beads) != 8 {
			t.Errorf("%s: %d/%d %+v", filepath.Base(path), l, r, got.Beads)
		}
	}
}

func TestLiveCampNarutoRedFullRow(t *testing.T) {
	cfg, err := config.Load("../../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	img := loadRGBA(t, "../../../inbox/regressions/camp-naruto-red-four-live-20260907.png")
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	e.Prefer("camp")
	for _, width := range []int{800, 960, 1280, 1600, 1920, 2560} {
		scaled := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
		xdraw.BiLinear.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Src, nil)
		got := e.Analyze(scaled)
		l, r := countReady(got.Beads)
		if got.LeftNinja != ninja.Naruto || l != 4 || r != 2 || knownCount(got.Beads) != 8 {
			t.Fatalf("%dpx live red HUD got left=%d right=%d known=%d: %+v", width, l, r, knownCount(got.Beads), got)
		}
	}
}

func TestRedHighlightCannotPromoteEmptyOrUnidentifiedBeans(t *testing.T) {
	area := detect.ContentArea{W: 1600, H: 900}
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left", Idx: 0}, X: 155, Y: 101}
	for _, name := range []string{"white-on-empty", "all-white", "red-wash", "red-band", "no-skin"} {
		t.Run(name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 400, 180))
			draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{38, 8, 8, 255}), image.Point{}, draw.Src)
			red := image.NewUniform(color.RGBA{235, 43, 9, 255})
			white := image.NewUniform(color.RGBA{255, 255, 255, 255})
			readout := ninja.Readout{Name: ninja.Naruto, Palette: ninja.Red, Slots: 4}
			switch name {
			case "white-on-empty":
				draw.Draw(img, image.Rect(151, 97, 160, 105), white, image.Point{}, draw.Src)
			case "all-white":
				draw.Draw(img, img.Bounds(), white, image.Point{}, draw.Src)
			case "red-wash":
				draw.Draw(img, img.Bounds(), red, image.Point{}, draw.Src)
			case "red-band":
				draw.Draw(img, image.Rect(0, 83, 400, 120), red, image.Point{}, draw.Src)
			case "no-skin":
				img = loadRGBA(t, "../../../inbox/regressions/camp-naruto-red-four-live-20260907.png")
				p.X = 180 // genuine bright core, but without a verified Naruto name
				readout = ninja.Readout{}
			}
			got := sampleCalibratedSpecial(img, []detect.BeadPosition{p}, area, config.Default().Vision, [2]ninja.Readout{readout, {}})
			if len(got) != 1 || !got[0].Unknown || got[0].Lit {
				t.Fatalf("effect or missing identity became a usable bean: %+v", got)
			}
		})
	}
}
