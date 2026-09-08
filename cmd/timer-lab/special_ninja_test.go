package main

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

// These are synthetic compositions, not unaltered full-screen captures: real
// user-supplied corner crops are aligned to the independently calibrated slots
// of an already recorded battle. Separate crop tests verify the original pixels.
func TestSpecialCornerThroughConfiguredPipeline(t *testing.T) {
	cfg := integrationConfig(t)
	for _, tc := range []struct {
		file, name         string
		x, y, slots, ready int
	}{
		{"hashirama-full", ninja.Hashirama, 88, 44, 6, 6}, {"hashirama-three", ninja.Hashirama, 81, 59, 6, 3}, {"hashirama-four", ninja.Hashirama, 83, 52, 6, 4},
		{"madara-full", ninja.Madara, 92, 61, 6, 6}, {"madara-four", ninja.Madara, 91, 58, 6, 4},
		{"obito-full", ninja.Obito, 86, 55, 4, 4}, {"obito-two", ninja.Obito, 87, 56, 4, 2},
		{"naruto-full", ninja.Naruto, 93, 56, 4, 4}, {"naruto-two", ninja.Naruto, 87, 61, 4, 2},
	} {
		t.Run(tc.file, func(t *testing.T) {
			for _, w := range []int{720, 960, 1440, 1920} {
				img := specialComposite(t, cfg, tc.file, tc.x, tc.y, w)
				eng, err := factory.New(factory.FromApp(cfg))
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "synthetic-special-hud")
				ready := knownCount(f.Beads, 'L', f.Slots(true, cfg.Layout.BeadsPerSide))
				if !f.Fighting || f.Hold || f.LeftNinja != tc.name || f.LeftSlots != tc.slots || ready == nil || *ready != tc.ready {
					t.Fatalf("width %d: fight=%v hold=%v name=%q slots=%d count=%v beads=%+v", w, f.Fighting, f.Hold, f.LeftNinja, f.LeftSlots, ready, f.Beads)
				}
			}
		})
	}
}

func specialComposite(t *testing.T, cfg config.Config, name string, x, y, width int) *image.RGBA {
	t.Helper()
	base, err := loadRGBA("../../inbox/regressions/duel-player-left-20260906.png")
	if err != nil {
		t.Fatal(err)
	}
	crop, err := loadRGBA(filepath.Join("../../inbox/regressions/special-ninjas-20260907", name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
	xdraw.BiLinear.Scale(img, img.Bounds(), base, base.Bounds(), draw.Src, nil)
	layout := engine.NewConfiguredLayout(factory.FromApp(cfg).Mode, cfg.Layout)
	area, ok := layout.ContentArea(img)
	if !ok {
		t.Fatal("area")
	}
	first := layout.PositionsIn("duel", area)[0]
	scale := float64(width) / 960
	corner := image.NewRGBA(image.Rect(0, 0, int(float64(crop.Bounds().Dx())*scale+.5), int(float64(crop.Bounds().Dy())*scale+.5)))
	xdraw.BiLinear.Scale(corner, corner.Bounds(), crop, crop.Bounds(), draw.Src, nil)
	at := image.Pt(first.X-int(float64(x)*scale+.5), first.Y-int(float64(y)*scale+.5))
	draw.Draw(img, corner.Bounds().Add(at), corner, image.Point{}, draw.Src)
	return img
}

func TestSixSlotSubstituteThroughReplayTracker(t *testing.T) {
	cfg := integrationConfig(t)
	tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700000000, 0)
	count := 0
	for i, tc := range []struct {
		file string
		x, y int
	}{{"hashirama-four", 83, 52}, {"hashirama-three", 81, 59}, {"hashirama-three", 81, 59}} {
		img := specialComposite(t, cfg, tc.file, tc.x, tc.y, 960)
		img.SetRGBA(800, 320, color.RGBA{uint8(i * 30), 50, 70, 255})
		now := at.Add(time.Duration(i) * 16 * time.Millisecond)
		f := frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, now, now, "synthetic-six-slot-drop")
		count += len(tracker.observe(f))
	}
	if count != 1 || tracker.left.EventCount() != 1 || tracker.right.EventCount() != 0 {
		t.Fatalf("events %d left=%d right=%d", count, tracker.left.EventCount(), tracker.right.EventCount())
	}
}
