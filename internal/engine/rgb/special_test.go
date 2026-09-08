package rgb

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/ninja"
)

func TestReportedSpecialNinjaBeans(t *testing.T) {
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
			file, err := os.Open(filepath.Join("../../../inbox/regressions/special-ninjas-20260907", tc.file+".png"))
			if err != nil {
				t.Fatal(err)
			}
			source, _, err := image.Decode(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			img := image.NewRGBA(source.Bounds())
			draw.Draw(img, img.Bounds(), source, source.Bounds().Min, draw.Src)
			var positions []detect.BeadPosition
			for i := range 4 {
				x := tc.x + 15*i
				positions = append(positions, detect.BeadPosition{Bead: detect.Bead{Side: "left", Idx: i, LX: float64(x) * 2, LY: float64(tc.y) * 2}, X: x, Y: tc.y})
			}
			e := &Engine{names: ninja.NewReader()}
			area := detect.ContentArea{W: 960, H: 540}
			positions, names := e.specialPositions(img, positions, area)
			if names[0].Name != tc.name {
				t.Fatalf("name %+v want %s", names[0], tc.name)
			}
			if len(positions) != tc.slots {
				t.Fatalf("slots %d want %d", len(positions), tc.slots)
			}
			beads := sampleCalibratedSpecial(img, positions, area, config.Default().Vision, names)
			count := 0
			for _, b := range beads {
				if b.Unknown {
					t.Fatalf("unknown bead: %+v (all %+v)", b, beads)
				}
				if b.Lit || b.Gold {
					count++
				}
			}
			if count != tc.ready {
				t.Fatalf("read %d/%d want %d/%d: %+v", count, len(beads), tc.ready, tc.slots, beads)
			}
		})
	}
}

func TestSpecialPaletteNeedsNameAndRejectsBroadColoredWash(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{235, 43, 9, 255}), image.Point{}, draw.Src)
	var positions []detect.BeadPosition
	for i := range 6 {
		positions = append(positions, detect.BeadPosition{Bead: detect.Bead{Side: "left", Idx: i, LX: float64(80+i*15) * 2, LY: 100}, X: 80 + i*15, Y: 50})
	}
	for _, readout := range []ninja.Readout{{}, {Name: ninja.Hashirama, Palette: ninja.Warm, Slots: 6}} {
		beads := sampleCalibratedSpecial(img, positions, detect.ContentArea{W: 960, H: 540}, config.Default().Vision, [2]ninja.Readout{readout, {}})
		for _, b := range beads {
			if !b.Unknown {
				t.Fatalf("solid red effect became readable beans (name=%q): %+v", readout.Name, beads)
			}
		}
	}
}
