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
	"time"
)

func TestReportedSpecialNinjaBeans(t *testing.T) {
	for _, tc := range []struct {
		file, name         string
		x, y, slots, ready int
	}{
		{"hashirama-full", ninja.Hashirama, 88, 44, 6, 6}, {"hashirama-three", ninja.Hashirama, 81, 59, 6, 3}, {"hashirama-four", ninja.Hashirama, 83, 52, 6, 4},
		{"madara-full", ninja.Madara, 92, 61, 4, 4}, {"madara-four", ninja.Madara, 91, 58, 4, 4},
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

// The canonical Madara title has four total slots. Exercise the normal
// four-position layout rather than truncating a generated six-slot row, and
// decide zero/partial/full states solely from pixels in each fresh frame.
func TestMadaraCanonicalTitleUsesNativeFourSlotPositionsAndCurrentPixels(t *testing.T) {
	area := detect.ContentArea{W: 960, H: 540}
	for _, side := range []string{"left", "right"} {
		t.Run(side, func(t *testing.T) {
			layout := engine.NewConfiguredLayout(detect.ModeAuto, config.Default().Layout)
			allPositions := layout.PositionsIn("camp", area)
			positions := make([]detect.BeadPosition, 0, 4)
			sideIndex := 0
			if side == "right" {
				sideIndex = 1
			}
			for _, p := range allPositions {
				if p.Side == side {
					positions = append(positions, p)
				}
			}
			if len(positions) != 4 {
				t.Fatalf("configured %s four-slot layout=%+v", side, positions)
			}
			first := positions[0]
			for _, tc := range []struct {
				name  string
				ready int
			}{{"zero", 0}, {"partial", 2}, {"full", 4}} {
				t.Run(tc.name, func(t *testing.T) {
					img := image.NewRGBA(image.Rect(0, 0, area.W, area.H))
					roi := ninja.NameRegion(image.Pt(first.X, first.Y), 1, side == "left")
					name := loadRGBA(t, "../../ninja/templates/madara.png")
					at := roi.Min.Add(image.Pt(12, 7))
					draw.Draw(img, image.Rectangle{Min: at, Max: at.Add(name.Bounds().Size())}, name, name.Bounds().Min, draw.Src)
					for i, p := range positions {
						c := color.RGBA{28, 54, 98, 255}
						if i < tc.ready {
							c = warmTestColor
						}
						draw.Draw(img, image.Rect(p.X-3, p.Y-4, p.X+4, p.Y+5), image.NewUniform(c), image.Point{}, draw.Src)
					}
					e := &Engine{names: ninja.NewReader()}
					allGenerated, names := e.specialPositionsForAt("camp", img, allPositions, area, time.Unix(1700000000, 0))
					if len(allGenerated) != 8 {
						t.Fatalf("canonical title expanded the two native four-slot rows: %d positions", len(allGenerated))
					}
					gotPositions := make([]detect.BeadPosition, 0, 4)
					for _, p := range allGenerated {
						if p.Side == side {
							gotPositions = append(gotPositions, p)
						}
					}
					if names[sideIndex].Name != ninja.Madara || names[sideIndex].Slots != 4 || names[sideIndex].Palette != ninja.Warm {
						t.Fatalf("canonical title readout=%+v", names[sideIndex])
					}
					if len(gotPositions) != 4 {
						t.Fatalf("generated positions=%d, want native four-slot row: %+v", len(gotPositions), gotPositions)
					}
					beads := sampleCalibratedSpecial(img, gotPositions, area, config.Default().Vision, names)
					ready := 0
					for _, bead := range beads {
						if bead.Unknown {
							t.Fatalf("%s state became unknown: %+v", tc.name, beads)
						}
						if bead.Lit {
							ready++
						}
					}
					if ready != tc.ready {
						t.Fatalf("current-frame %s=%d/4, want %d/4: %+v", tc.name, ready, tc.ready, beads)
					}
				})
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

func TestUnverifiedSpecialNameKeepsClearlyDarkBeansObservable(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{28, 42, 58, 255}), image.Point{}, draw.Src)
	positions := []detect.BeadPosition{{Bead: detect.Bead{Side: "right", Idx: 0}, X: 80, Y: 50}}
	readouts := [2]ninja.Readout{{}, {Slots: 6, Unverified: true, PaletteHint: ninja.Warm}}
	beads := sampleCalibratedSpecial(img, positions, detect.ContentArea{W: 960, H: 540}, config.Default().Vision, readouts)
	if len(beads) != 1 || beads[0].Unknown || beads[0].Lit || beads[0].Gold {
		t.Fatalf("dark bean became unobservable during name gap: %+v", beads)
	}
}

func TestUnverifiedPurpleNameUsesCurrentPurpleBody(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	purple := color.RGBA{255, 70, 255, 255}
	draw.Draw(img, image.Rect(75, 45, 86, 56), image.NewUniform(purple), image.Point{}, draw.Src)
	positions := []detect.BeadPosition{{Bead: detect.Bead{Side: "left", Idx: 0}, X: 80, Y: 50}}
	readouts := [2]ninja.Readout{{Slots: 4, Unverified: true, PaletteHint: ninja.Purple}, {}}
	beads := sampleCalibratedSpecial(img, positions, detect.ContentArea{W: 960, H: 540}, config.Default().Vision, readouts)
	if len(beads) != 1 || !beads[0].Lit || beads[0].Unknown {
		t.Fatalf("current purple body became unknown during name gap: %+v", beads)
	}
}

func TestUnverifiedPurpleNameRejectsBroadWash(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	purple := color.RGBA{255, 70, 255, 255}
	draw.Draw(img, img.Bounds(), image.NewUniform(purple), image.Point{}, draw.Src)
	positions := []detect.BeadPosition{{Bead: detect.Bead{Side: "left", Idx: 0}, X: 100, Y: 50}}
	readouts := [2]ninja.Readout{{Slots: 4, Unverified: true, PaletteHint: ninja.Purple}, {}}
	beads := sampleCalibratedSpecial(img, positions, detect.ContentArea{W: 960, H: 540}, config.Default().Vision, readouts)
	if len(beads) != 1 || !beads[0].Unknown || beads[0].Lit {
		t.Fatalf("broad purple wash supplied a bean vote: %+v", beads)
	}
}
