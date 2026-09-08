package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"

	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/match"
)

func integrationConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cfg.UI.PlayerNames = nil
	return cfg
}

func TestConfiguredPipelineRecordedScenes(t *testing.T) {
	cfg := integrationConfig(t)
	for _, tc := range []struct {
		path, scene, profile string
		left, right          int
	}{
		{"../../inbox/fight/duel_live_20260821.png", "fight", "camp", 4, 2},
		{"../../inbox/fight/duel_20260821.png", "fight", "duel", 2, 4},
		{"../../inbox/regressions/duel-user-20260906.png", "fight", "duel", 2, 2},
		{"../../inbox/regressions/duel-second-round-20260906.png", "fight", "duel", 2, 4},
		{"../../inbox/regressions/duel-player-left-20260906.png", "fight", "duel", 2, 4},
		{"../../debug/lab-sdk-probe-20260906/frame-000001.png", "lobby", "", -1, -1},
		{"../../inbox/regressions/duel-lobby-20260906.png", "lobby", "", -1, -1},
	} {
		t.Run(tc.scene+tc.profile, func(t *testing.T) {
			eng, err := factory.New(factory.FromApp(cfg))
			if err != nil {
				t.Fatal(err)
			}
			img, err := loadRGBA(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "replay")
			if f.Err != nil || f.Hold || f.Scene != tc.scene || f.LayoutProfile != tc.profile {
				t.Fatalf("scene: %+v", f)
			}
			if tc.left < 0 {
				if len(f.Beads) != 0 {
					t.Fatal("lobby must not observe beads")
				}
				return
			}
			l, r := knownCount(f.Beads, 'L', 4), knownCount(f.Beads, 'R', 4)
			if l == nil || r == nil || *l != tc.left || *r != tc.right {
				t.Fatalf("counts: %v / %v; beads=%+v", l, r, f.Beads)
			}
		})
	}
}

func TestDuelGateIgnoresBackdropBehindRoundText(t *testing.T) {
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	img, err := loadRGBA("../../inbox/fight/duel_20260821.png")
	if err != nil {
		t.Fatal(err)
	}
	templateImg, err := loadRGBA("../../assets/templates/raw_duel_round.png")
	if err != nil {
		t.Fatal(err)
	}
	maskImg, err := loadRGBA("../../assets/templates/raw_duel_round.mask.png")
	if err != nil {
		t.Fatal(err)
	}
	templ := match.ScaleGray(match.ToGray(templateImg), 112, 42)
	mask := match.ScaleGray(match.ToGray(maskImg), 112, 42)
	roi := image.Rect(740, 13, 868, 67)
	hit, err := (match.NCC{}).Match(match.Query{Image: img, ROI: roi, Template: templ, Mask: mask})
	if err != nil || hit.Value < .8 {
		t.Fatalf("baseline marker: %+v %v", hit, err)
	}
	for _, background := range []color.RGBA{{240, 240, 240, 255}, {20, 24, 28, 255}, {190, 90, 30, 255}} {
		changed := image.NewRGBA(img.Bounds())
		draw.Draw(changed, changed.Bounds(), img, image.Point{}, draw.Src)
		for y := roi.Min.Y; y < roi.Max.Y; y++ {
			for x := roi.Min.X; x < roi.Max.X; x++ {
				p := image.Pt(x, y).Sub(hit.Peak)
				if !p.In(mask.Bounds()) || mask.GrayAt(p.X, p.Y).Y == 0 {
					changed.SetRGBA(x, y, background)
				}
			}
		}
		now := time.Now()
		f := frame.AnalyzeImage(changed, eng, factory.FromApp(cfg).Mode, now, now, "synthetic-backdrop")
		if f.Hold || !f.Fighting || f.LayoutProfile != "duel" {
			t.Fatalf("background %v hid the round marker: scene=%s hold=%v profile=%s", background, f.Scene, f.Hold, f.LayoutProfile)
		}
	}
	// A flat patch with no lettering must not match by average brightness.
	draw.Draw(img, roi, image.NewUniform(color.RGBA{70, 70, 70, 255}), image.Point{}, draw.Src)
	eng, err = factory.New(factory.FromApp(cfg)) // No established battle context.
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "synthetic-no-marker")
	if f.Fighting {
		t.Fatal("scene was established after removing its marker")
	}
}

func TestBattlePipelineAtMultipleResolutions(t *testing.T) {
	cfg := integrationConfig(t)
	for _, tc := range []struct {
		path, profile string
		left, right   int
	}{
		{"fight/duel_live_20260821.png", "camp", 4, 2},
		{"fight/duel_20260821.png", "duel", 2, 4},
		{"regressions/duel-user-20260906.png", "duel", 2, 2},
		{"regressions/duel-second-round-20260906.png", "duel", 2, 4},
		{"regressions/duel-player-left-20260906.png", "duel", 2, 4},
	} {
		src, err := loadRGBA("../../inbox/" + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range []int{640, 800, 960, 1280, 1600, 1920, 2560} {
			t.Run(fmt.Sprintf("%s/%d", tc.path, w), func(t *testing.T) {
				img := image.NewRGBA(image.Rect(0, 0, w, w*9/16))
				xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
				eng, err := factory.New(factory.FromApp(cfg))
				if err != nil {
					t.Fatal(err)
				}
				now := time.Unix(1700000000, 0)
				f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "resolution-test")
				l, r := knownCount(f.Beads, 'L', 4), knownCount(f.Beads, 'R', 4)
				if !f.Fighting || f.Hold || f.LayoutProfile != tc.profile || l == nil || r == nil || *l != tc.left || *r != tc.right {
					t.Fatalf("scene=%s profile=%s fight=%v hold=%v l=%v r=%v beads=%+v", f.Scene, f.LayoutProfile, f.Fighting, f.Hold, l, r, f.Beads)
				}
			})
		}
	}
}

func TestSceneAndBeadsShareContentArea(t *testing.T) {
	cfg := integrationConfig(t)
	src, err := loadRGBA("../../inbox/regressions/duel-user-20260906.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		bounds, content image.Rectangle
	}{
		{"horizontal-bars", image.Rect(0, 0, 1280, 960), image.Rect(0, 120, 1280, 840)},
		{"vertical-bars", image.Rect(0, 0, 1920, 900), image.Rect(160, 0, 1760, 900)},
		{"nonzero-origin", image.Rect(30, 50, 1630, 950), image.Rect(30, 50, 1630, 950)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(tc.bounds)
			xdraw.BiLinear.Scale(img, tc.content, src, src.Bounds(), draw.Src, nil)
			eng, err := factory.New(factory.FromApp(cfg))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Unix(1700000000, 0)
			f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "test")
			l, r := knownCount(f.Beads, 'L', 4), knownCount(f.Beads, 'R', 4)
			if !f.Fighting || f.Hold || f.LayoutProfile != "duel" || l == nil || r == nil || *l != 2 || *r != 2 {
				t.Fatalf("inconsistent mapping: %+v", f)
			}
		})
	}
}

func TestSyntheticVisualDropPassesThroughDetectorAndClock(t *testing.T) {
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	full, err := loadRGBA("../../inbox/fight/duel_live_20260821.png")
	if err != nil {
		t.Fatal(err)
	}
	drop := image.NewRGBA(full.Bounds())
	draw.Draw(drop, drop.Bounds(), full, image.Point{}, draw.Src)
	// Synthetic regression fixture only: replace the fourth calibrated core
	// with dark pixels. This does not assert real gameplay event accuracy.
	p := cfg.Layout.Profile("camp").Left.NominalCenters[3]
	x, y := int(p.X*1600), int(p.Y*900)
	draw.Draw(drop, image.Rect(x-7, y-8, x+8, y+9), image.NewUniform(color.RGBA{28, 54, 98, 255}), image.Point{}, draw.Src)
	freshDrop := image.NewRGBA(drop.Bounds())
	draw.Draw(freshDrop, freshDrop.Bounds(), drop, image.Point{}, draw.Src)
	freshDrop.SetRGBA(800, 500, color.RGBA{50, 60, 70, 255})
	now := time.Unix(1700000000, 0)
	tracker := newReplayTracker(cfg, 15*time.Second)
	for i, img := range []*image.RGBA{full, drop, drop, freshDrop} {
		at := now.Add(time.Duration(i) * 25 * time.Millisecond)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, at, at, "synthetic")
		f.Duplicate = i == 2
		events := tracker.observe(f)
		if i < 3 && len(events) != 0 {
			t.Fatalf("premature event at %d: %+v", i, events)
		}
		if i == 3 && (len(events) != 1 || events[0].side != "left" || !events[0].firstObserved.Equal(now.Add(25*time.Millisecond))) {
			t.Fatalf("confirmed event: %+v", events)
		}
	}
}
