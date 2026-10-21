package frame

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/engine/rgb"
	"narutotimer/internal/safego"
)

// GE-03 with the real bead engine: the auditor's out-of-range vision sizes (now
// rejected by config.Validate) made rgb sampling panic with "makeslice: len out
// of range" on BOTH layouts, one of them on an engine helper goroutine, which no
// caller could recover. Any future engine bug on an unusual frame has the same
// blast radius; it must now fail that frame and keep the process alive.
func TestRealEnginePanicOnHelperGoroutineBecomesErrorFrame(t *testing.T) {
	safego.SetLog(nil)
	cfg := config.Default()
	cfg.Vision.SampleWidthReferencePX, cfg.Vision.SampleHeightReferencePX, cfg.Vision.CoreScale = 1e9, 1e9, 1e9
	eng := rgb.NewConfigured(engine.NewConfiguredLayout(detect.ModeStretch, cfg.Layout), cfg.Vision)
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{30, 40, 60, 255}), image.Point{}, draw.Src)
	for i := 0; i < 3; i++ { // the engine stays usable after a failed frame
		at := time.Now()
		f := AnalyzeImage(img, eng, detect.ModeStretch, at, at, "probe")
		if f.Err == nil || !f.Hold || !strings.Contains(f.Err.Error(), "makeslice") {
			t.Fatalf("frame %d: err=%v hold=%v", i, f.Err, f.Hold)
		}
	}
}
