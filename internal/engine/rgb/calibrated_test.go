package rgb

import (
	"image"
	"image/color"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

func TestCalibratedHUDDoesNotFollowEmptyBeadRim(t *testing.T) {
	for _, tc := range []struct {
		name, path   string
		left, right  []float64
		y            float64
		wantL, wantR int
	}{
		{"camp", "../../../inbox/fight/duel_live_20260821.png", []float64{155, 180, 205, 230}, []float64{1391, 1366, 1340, 1315}, 101, 4, 2},
		{"duel", "../../../inbox/fight/duel_20260821.png", []float64{172, 198, 223, 249}, []float64{1423, 1398, 1373, 1348}, 103, 2, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			points := func(xs []float64) config.SideLayout {
				var s config.SideLayout
				for _, x := range xs {
					s.NominalCenters = append(s.NominalCenters, config.NormalizedPoint{X: x / 1600, Y: tc.y / 900})
				}
				return s
			}
			cfg.Layout.Profiles = map[string]config.LayoutProfile{tc.name: {Left: points(tc.left), Right: points(tc.right)}}
			e := NewConfigured(engine.NewConfiguredLayout(detect.ModeStretch, cfg.Layout), cfg.Vision)
			e.Prefer(tc.name)
			img := loadRGBA(t, tc.path)
			for _, width := range []int{1600, 800, 1920} {
				height := width * 9 / 16
				scaled := image.NewRGBA(image.Rect(0, 0, width, height))
				for y := 0; y < height; y++ {
					for x := 0; x < width; x++ {
						scaled.SetRGBA(x, y, img.RGBAAt(x*1600/width, y*900/height))
					}
				}
				res := e.Analyze(scaled)
				l, r := countReady(res.Beads)
				if l != tc.wantL || r != tc.wantR || knownCount(res.Beads) != 8 {
					t.Fatalf("%dpx: got %d/%d, want %d/%d; beads=%+v", width, l, r, tc.wantL, tc.wantR, res.Beads)
				}
			}
		})
	}
}

func TestCalibratedCoreRejectsBrightNeighborsAndWhiteOcclusion(t *testing.T) {
	cfg := config.Default().Vision
	img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left", Idx: 0}, X: 100, Y: 100}
	// A cyan rim nearby must not pull a dark center towards itself.
	for y := 88; y <= 112; y++ {
		for x := 88; x <= 112; x++ {
			img.SetRGBA(x, y, color.RGBA{25, 210, 245, 255})
		}
	}
	for y := 96; y <= 104; y++ {
		for x := 97; x <= 103; x++ {
			img.SetRGBA(x, y, color.RGBA{28, 54, 98, 255})
		}
	}
	area := detect.ComputeContentArea(1600, 900, detect.ModeStretch)
	beads := sampleCalibrated(img, []detect.BeadPosition{p}, area, cfg)
	if len(beads) != 1 || beads[0].Lit || beads[0].Unknown {
		t.Fatalf("rim changed a dark bead: %+v", beads)
	}
	for y := 96; y <= 104; y++ {
		for x := 97; x <= 103; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	beads = sampleCalibrated(img, []detect.BeadPosition{p}, area, cfg)
	if !beads[0].Unknown {
		t.Fatalf("white effect must remain unknown: %+v", beads)
	}
}

func TestGoldHighlightRequiresSaturatedGoldEvidence(t *testing.T) {
	cfg := config.Default().Vision
	img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	p := detect.BeadPosition{Bead: detect.Bead{Side: "left", Idx: 0}, X: 100, Y: 100}
	area := detect.ComputeContentArea(1600, 900, detect.ModeStretch)
	for y := 95; y <= 105; y++ {
		for x := 95; x <= 105; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 199, 255})
		}
	}
	beads := sampleCalibrated(img, []detect.BeadPosition{p}, area, cfg)
	if !beads[0].Unknown {
		t.Fatal("pale effect without saturated gold must remain unknown")
	}
	for y := 99; y <= 101; y++ {
		img.SetRGBA(100, y, color.RGBA{255, 189, 0, 255})
		img.SetRGBA(101, y, color.RGBA{255, 189, 0, 255})
	}
	img.SetRGBA(99, 100, color.RGBA{255, 189, 0, 255})
	beads = sampleCalibrated(img, []detect.BeadPosition{p}, area, cfg)
	if beads[0].Unknown || !beads[0].Gold || !beads[0].Lit {
		t.Fatalf("saturated gold plus highlight should be available: %+v", beads)
	}
}
