package rgb

import (
	"image"
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

// The game recordings are supplied locally rather than committed. Set
// NARUTO_VIDEO_FRAMES to a directory containing per-video subdirectories with
// PNG frames (the repository's GStreamer extraction command can produce it).
// This turns the supplied recordings into an opt-in regression without making
// private gameplay media part of the test fixture set.
func TestSuppliedVideoRecognitionEvidence(t *testing.T) {
	root := os.Getenv("NARUTO_VIDEO_FRAMES")
	if root == "" {
		t.Skip("set NARUTO_VIDEO_FRAMES to extracted supplied-video PNGs")
	}
	cases := []struct {
		dir                   string
		left, right           string
		leftSlots, rightSlots int
		leftReady, rightReady int
		frame                 string
	}{
		// These are actual 1280x720 MuMu frames. The Itachi assertion exercises
		// the video-derived current-title template at both supplied recordings;
		// its 13px energy row offset must expose the four current beans.
		{"2026-09-23 16-33-17", ninja.Madara, ninja.ItachiHyakusen, 4, 4, 4, 4, "frame-03.png"},
		{"2026-09-23 16-33-25", ninja.Madara, ninja.ItachiHyakusen, 4, 4, 4, 4, "frame-15.png"},
		{"2026-09-23 16-36-05", ninja.Hashirama, ninja.MinatoKyubi, 6, 4, 5, 0, "frame-02.png"},
		{"2026-09-23 16-39-30", ninja.Obito, ninja.SasukeXiayin, 4, 4, 4, 2, "frame-05.png"},
	}
	cfg := config.Default()
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			img := loadVideoRGBA(t, filepath.Join(root, tc.dir, tc.frame))
			layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
			got := NewConfigured(layout, cfg.Vision).AnalyzeAt(img, time.Unix(1700000000, 0))
			left, right := countReady(got.Beads)
			if got.LeftNinja != tc.left || got.RightNinja != tc.right || got.LeftSlots != tc.leftSlots || got.RightSlots != tc.rightSlots || left != tc.leftReady || right != tc.rightReady {
				t.Fatalf("recognition=%q/%q slots=%d/%d ready=%d/%d, want %q/%q slots=%d/%d ready=%d/%d: %+v", got.LeftNinja, got.RightNinja, got.LeftSlots, got.RightSlots, left, right, tc.left, tc.right, tc.leftSlots, tc.rightSlots, tc.leftReady, tc.rightReady, got)
			}
		})
	}
}

func loadVideoRGBA(t *testing.T, path string) *image.RGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img
}
