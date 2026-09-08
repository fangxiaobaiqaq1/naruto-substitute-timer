package rgb

import (
	"image"
	"image/draw"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

func TestUserOrdinaryNamesDoNotGateBeans(t *testing.T) {
	for _, tc := range []struct{ file string }{
		{"duel-shino-name-20260907.png"},
		{"duel-long-account-tayuya-20260907.png"},
	} {
		img := loadRGBA(t, "../../../inbox/regressions/"+tc.file)
		for _, width := range []int{800, 950, 1280, 1920} {
			cfg := config.Default()
			e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
			e.Prefer("duel")
			frame := image.NewRGBA(image.Rect(0, 0, width, width*img.Bounds().Dy()/img.Bounds().Dx()))
			xdraw.BiLinear.Scale(frame, frame.Bounds(), img, img.Bounds(), draw.Src, nil)
			at := time.Unix(100, 0)
			got := e.AnalyzeAt(frame, at)
			if got.LeftNinja != "" || got.RightNinja != "" {
				t.Errorf("%s %dpx ordinary name supplied by special detector: %q/%q", tc.file, width, got.LeftNinja, got.RightNinja)
			}
			if knownCount(got.Beads) != 8 || got.LeftSlots != 4 || got.RightSlots != 4 {
				t.Fatalf("%s %dpx bean topology: %+v", tc.file, width, got)
			}
			// Erase only lettering, never the bean row. A display-only name's
			// failed next-frame recheck must not turn otherwise good beans unknown.
			draw.Draw(frame, image.Rect(0, 16*width/950, width, 40*width/950), image.Black, image.Point{}, draw.Src)
			lost := e.AnalyzeAt(frame, at.Add(20*time.Millisecond))
			if lost.LeftNinja != "" || lost.RightNinja != "" || knownCount(lost.Beads) != 8 {
				t.Errorf("%s %dpx name gap affected beans or retained name: %+v", tc.file, width, lost)
			}
			l, r := countReady(got.Beads)
			ll, rr := countReady(lost.Beads)
			if l != ll || r != rr {
				t.Errorf("name-only gap changed counts: %d/%d -> %d/%d", l, r, ll, rr)
			}
		}
	}
}
