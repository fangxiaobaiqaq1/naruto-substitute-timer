package rgb

import (
	"image"
	"image/draw"
	_ "image/png"
	"os"
	"testing"

	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

func TestAnalyzeCountsGoldBeadsReady(t *testing.T) {
	img := loadRGBA(t, "../../../debug/live-now.png")
	eng := New(engine.NewDefaultLayout(detect.ModeAuto))
	res := eng.Analyze(img)
	left, right := countReady(res.Beads)
	if left != 4 || right != 4 {
		t.Fatalf("full gold HUD must read 4/4, got L=%d R=%d name=%s beads=%+v", left, right, res.Name, res.Beads)
	}
}

func TestAnalyzeCountsFourCyanBeads(t *testing.T) {
	img := loadRGBA(t, "../../../debug/four-as-three.png")
	eng := New(engine.NewDefaultLayout(detect.ModeAuto))
	eng.Prefer("duel")
	res := eng.Analyze(img)
	left, right := countReady(res.Beads)
	if left != 4 || right != 4 {
		t.Fatalf("full cyan HUD must read 4/4, got L=%d R=%d name=%s beads=%+v", left, right, res.Name, res.Beads)
	}
}

func TestAnalyzePicksDuelLayout(t *testing.T) {
	img := loadRGBA(t, "../../../debug/false-cd.png")
	eng := New(engine.NewDefaultLayout(detect.ModeAuto))
	eng.Prefer("duel")
	res := eng.Analyze(img)
	if res.Name != "rgb-duel" {
		t.Fatalf("duel HUD should pick duel layout, got %s", res.Name)
	}
}

func countReady(beads []engine.BeadInfo) (left, right int) {
	for _, b := range beads {
		if !b.Lit || b.Unknown {
			continue
		}
		if len(b.Label) > 0 && b.Label[0] == 'R' {
			right++
		} else {
			left++
		}
	}
	return
}

func loadRGBA(t *testing.T, path string) *image.RGBA {
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
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	return out
}
