package rgb

import (
	"image"
	_ "image/png"
	"os"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

// The source frame is user evidence and intentionally stays local. This test is
// runnable where it is available; the direct layout assertion is portable.
func TestUserMinatoKakashiDuelBeans(t *testing.T) {
	path := "../../../tmp/user-frames/minato-kakashi-beans.png"
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		t.Skip("local user screenshot is not distributed")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.Set(x, y, src.At(x, y))
		}
	}
	cfg, err := config.Load("../../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	e := NewConfigured(layout, cfg.Vision)
	e.Prefer("duel")
	got := e.AnalyzeAt(img, time.Unix(1700000000, 0))
	if got.LeftNinja != "波风水门[九喇嘛连结]" || got.LeftSlots != 4 || got.RightSlots != 4 {
		t.Fatalf("unexpected identity/topology: %+v", got)
	}
	for _, tc := range []struct {
		label string
		lit   bool
	}{
		{"L1", true}, {"L2", true}, {"L3", false}, {"L4", false},
		{"R1", true}, {"R2", true}, {"R3", false}, {"R4", false},
	} {
		var found bool
		for _, bead := range got.Beads {
			if bead.Label != tc.label {
				continue
			}
			found = true
			if bead.Unknown || bead.Lit != tc.lit {
				t.Fatalf("%s=%+v, want lit=%v", tc.label, bead, tc.lit)
			}
		}
		if !found {
			t.Fatalf("missing %s in %+v", tc.label, got.Beads)
		}
	}
}

func TestDefaultDuelProfileUsesNeutralBeanRow(t *testing.T) {
	cfg := config.Default()
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	positions := layout.PositionsIn("duel", detect.ContentArea{W: 1308, H: 735})
	for _, p := range positions {
		if p.Y != 84 {
			t.Fatalf("%s%d calibrated y=%d, want 84", p.Side, p.Idx+1, p.Y)
		}
	}
}
