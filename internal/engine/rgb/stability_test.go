package rgb

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"testing"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

func TestRecordedDuelGoldSparkleRemainsFour(t *testing.T) {
	cfg := config.Default()
	for _, i := range []int{30, 31, 32, 91, 92, 93, 152, 153, 154, 175, 176, 177} {
		src := loadRGBA(t, fmt.Sprintf("../../../inbox/regressions/duel-gold-glint-20260908/frame-%04d.png", i))
		for _, width := range []int{800, 950, 999, 1280, 1600, 1920, 2560} {
			img := image.NewRGBA(image.Rect(0, 0, width, width*src.Bounds().Dy()/src.Bounds().Dx()))
			xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
			e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
			e.Prefer("duel")
			got := e.Analyze(img)
			l, r := countReady(got.Beads)
			if l != 4 || r != 1 || knownCount(got.Beads) != 8 {
				t.Errorf("frame%d width%d: want known4/1, got %+v", i, width, got.Beads)
			}
		}
	}
}

func TestOneObscuredCoreDoesNotEraseOtherObservedCores(t *testing.T) {
	cfg := config.Default()
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	img := loadRGBA(t, "../../../inbox/regressions/duel-gold-glint-20260908/frame-0030.png")
	area, _ := layout.ContentArea(img)
	positions := layout.PositionsIn("duel", area)
	p := positions[1]
	draw.Draw(img, image.Rect(p.X-5, p.Y-9, p.X+6, p.Y+10), image.NewUniform(color.RGBA{110, 110, 110, 255}), image.Point{}, draw.Src)
	got := sampleCalibrated(img, positions, area, cfg.Vision)
	for _, b := range got {
		if (b.Label == "L2") != b.Unknown {
			t.Fatalf("one missing core erased independent observations: %+v", got)
		}
	}
}
