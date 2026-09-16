package main

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

func purpleEvidence(t *testing.T, path string) *image.RGBA {
	t.Helper()
	img, err := loadRGBA(path)
	if os.IsNotExist(err) {
		t.Skip("local user screenshots are not distributed")
	}
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestSeptemberPurpleScreenshotsThroughPipeline(t *testing.T) {
	configs := []config.Config{config.Default(), integrationConfig(t)}
	configs[0].Scene.Manifest = filepath.Join("../..", configs[0].Scene.Manifest)
	for _, tc := range []struct {
		path, name  string
		left, right int
	}{
		{"../../inbox/regressions/camp-sasuke-xiayin-20260916/three.png", ninja.SasukeXiayin, 3, 2},
		{"../../inbox/regressions/camp-sasuke-xiayin-20260916/glint.png", ninja.SasukeXiayin, 4, 2},
		{"../../inbox/regressions/camp-obito-flicker-20260916/game.png", ninja.Obito, 4, 6},
	} {
		src := purpleEvidence(t, tc.path)
		for cfgIndex, cfg := range configs {
			for _, width := range []int{800, 960, 1280, 1308, 1600, 1920, 2560} {
				t.Run(fmt.Sprintf("%s/%s/config%d/%d", tc.name, filepath.Base(tc.path), cfgIndex, width), func(t *testing.T) {
					img := src
					if width != src.Bounds().Dx() {
						img = image.NewRGBA(image.Rect(0, 0, width, width*9/16))
						xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
					}
					eng, err := factory.New(factory.FromApp(cfg))
					if err != nil {
						t.Fatal(err)
					}
					tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
					for i := range 3 {
						now := time.Unix(100, 0).Add(time.Duration(i) * 20 * time.Millisecond)
						f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "user-still-purple")
						f.Duplicate = i > 0
						left, right := knownCount(f.Beads, 'L', f.LeftSlots), knownCount(f.Beads, 'R', f.RightSlots)
						if f.Err != nil || !f.Fighting || f.Hold || f.LayoutProfile != "camp" || f.LeftNinja != tc.name || f.RightNinja != ninja.Madara || f.LeftSlots != 4 || f.RightSlots != 6 || left == nil || right == nil || *left != tc.left || *right != tc.right {
							t.Fatalf("frame %d: %+v", i, f)
						}
						if events := tracker.observe(f); len(events) != 0 {
							t.Fatalf("still image fabricated events: %+v", events)
						}
					}
				})
			}
		}
	}
}

func TestSasukeSyntheticDropUsesFirstFrameAndIgnoresDuplicates(t *testing.T) {
	full := purpleEvidence(t, "../../inbox/regressions/camp-sasuke-xiayin-20260916/glint.png")
	three := purpleEvidence(t, "../../inbox/regressions/camp-sasuke-xiayin-20260916/three.png")
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
	at := time.Unix(100, 0)
	events := 0
	// Synthetic temporal sequence from still screenshots, not a real recording.
	for i, img := range []*image.RGBA{full, three, three, three, three} {
		now := at.Add(time.Duration(i) * 20 * time.Millisecond)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "synthetic-purple-drop")
		f.Duplicate = i == 2 || i == 4
		got := tracker.observe(f)
		if i != 3 && len(got) != 0 {
			t.Fatalf("unexpected early/repeated event at %d: %+v", i, got)
		}
		for _, event := range got {
			if event.side != "left" || !event.firstObserved.Equal(at.Add(20*time.Millisecond)) {
				t.Fatalf("wrong drop timestamp: %+v", event)
			}
		}
		events += len(got)
	}
	if events != 1 || tracker.left.EventCount() != 1 || tracker.right.EventCount() != 0 {
		t.Fatalf("events=%d", events)
	}
}
