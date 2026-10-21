package rgb

import (
	"fmt"
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

// User video 「火影忍者(15).mp4」 (2026-10-21, ranked duel recorded on the
// phone): left 宇智波佐助[侠隐江湖](白方小…), right 神秘面具男(爱有天意…) with
// the yellow spiral-mask portrait, catalog 90174 (a 70px icon). No title
// template exists for 神秘面具男, so on Android (no OCR) the portrait is the
// only source of that name, and the avatar matcher missed it on every frame
// at 1920, 1600 and 1280 wide (ninja/avatar.go, the full scan's second look).
//
// The private crops (inbox/regressions/video15-20261021, gitignored) are the
// top quarter of eight frames, 0.25 s apart in the video, at the three widths;
// each goes back onto a black 16:9 canvas (the content area of a 16:9 frame
// is the whole frame, so the black rest changes no HUD geometry).
var video15Frames = []string{"00000", "00150", "00300", "00450", "00600", "00750", "00900", "00990"}

func video15Frame(t *testing.T, width int, frame string) *image.RGBA {
	t.Helper()
	path := filepath.Join("../../../inbox/regressions/video15-20261021", fmt.Sprintf("top_%d_%s.png", width, frame))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("private user video crops are not distributed")
	}
	strip := loadRGBA(t, path)
	img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
	draw.Draw(img, strip.Bounds(), strip, strip.Bounds().Min, draw.Src)
	return img
}

func TestVideo15DuelNamesBothSides(t *testing.T) {
	for _, width := range []int{1920, 1600, 1280} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			cfg := config.Default()
			e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
			e.Prefer("duel")
			at := time.Unix(1700000000, 0)
			// 250 ms apart: full portrait scans alternate with the tracker's
			// re-verification of the identity found (500 ms scan interval).
			for i, frame := range video15Frames {
				got := e.AnalyzeAt(video15Frame(t, width, frame), at.Add(time.Duration(i)*250*time.Millisecond))
				if got.LeftNinja != ninja.SasukeXiayin || got.RightNinja != "神秘面具男" || got.LeftSlots != 4 || got.RightSlots != 4 {
					t.Errorf("frame %s: names %q/%q slots %d/%d", frame, got.LeftNinja, got.RightNinja, got.LeftSlots, got.RightSlots)
				}
			}
		})
	}
}
