package factory

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

func stabilityImage(t *testing.T, path string) *image.RGBA {
	t.Helper()
	f, err := os.Open(filepath.Join("../../../inbox", path))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img
}

func TestRealProfileChangeStillAdmitted(t *testing.T) {
	cfg := config.Default()
	cfg.Scene.Manifest = filepath.Join("../../..", cfg.Scene.Manifest)
	e, err := New(FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	timed := e.(engine.TimedEngine)
	camp := stabilityImage(t, "regressions/camp-idle-glint-20260907/diagnostic-01.png")
	duel := stabilityImage(t, "fight/duel_20260821.png")
	if camp.Bounds() != duel.Bounds() {
		t.Fatal("fixture geometry differs; test must exercise a real profile transition, not resize reset")
	}
	at := time.Unix(1700000000, 0)
	timed.AnalyzeAt(camp, at)
	for i, ms := range []int{20, 160, 360} {
		// Change only one irrelevant background pixel to model independent frames.
		duel.SetRGBA(800, 450, color.RGBA{uint8(i), 0, 0, 255})
		r := timed.AnalyzeAt(duel, at.Add(time.Duration(ms)*time.Millisecond))
		if i == 2 && (!r.Fighting || r.LayoutProfile != "duel") {
			t.Fatalf("real new profile could not replace old context: %+v", r)
		}
	}
}

// Real HUD/control pixels with explicitly SYNTHETIC local obstructions. This
// verifies that a single missing bean cannot turn into whole-scene uncertainty.
func TestPartialBeadAndPrimaryMarkerOcclusionAreIndependent(t *testing.T) {
	cfg := config.Default()
	cfg.Scene.Manifest = filepath.Join("../../..", cfg.Scene.Manifest)
	e, err := New(FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	timed := e.(engine.TimedEngine)
	src := stabilityImage(t, "regressions/camp-idle-glint-20260907/diagnostic-01.png")
	at := time.Unix(1700000000, 0)
	if r := timed.AnalyzeAt(src, at); !r.Fighting || r.LayoutProfile != "camp" {
		t.Fatalf("setup: %+v", r)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	for _, rect := range []image.Rectangle{image.Rect(w*44/100, 0, w*56/100, h*12/100), image.Rect(w*45/100, h*87/100, w*55/100, h*99/100)} {
		draw.Draw(img, rect, image.Black, image.Point{}, draw.Src)
	}
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	area, _ := layout.ContentArea(img)
	p := layout.PositionsIn("camp", area)[1]
	draw.Draw(img, image.Rect(p.X-5, p.Y-9, p.X+6, p.Y+10), image.Black, image.Point{}, draw.Src)
	for _, ms := range []int{20, 800, 1600} {
		r := timed.AnalyzeAt(img, at.Add(time.Duration(ms)*time.Millisecond))
		if !r.Fighting || r.Uncertain || r.Scene != "fight" || r.LayoutProfile != "camp" {
			t.Fatalf("one obscured slot caused scene loss: %+v", r)
		}
		unknown := 0
		for _, b := range r.Beads {
			if b.Unknown {
				unknown++
			}
		}
		if unknown != 1 {
			t.Fatalf("obscured slot fabricated or spread unknown: %+v", r.Beads)
		}
	}
	// A recognized page must clear context immediately. Its colors may not
	// subsequently resurrect a battle from the continuation button alone.
	page := stabilityImage(t, "result/失败.png")
	if r := timed.AnalyzeAt(page, at.Add(2*time.Second)); r.Fighting || r.Scene != "result" {
		t.Fatalf("result hidden by context: %+v", r)
	}
	if r := timed.AnalyzeAt(img, at.Add(2200*time.Millisecond)); r.Fighting {
		t.Fatalf("stale context reestablished fight without a marker: %+v", r)
	}
}
