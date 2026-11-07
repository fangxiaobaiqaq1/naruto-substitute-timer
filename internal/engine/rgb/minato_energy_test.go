package rgb

import (
	"fmt"
	"image"
	"image/draw"
	_ "image/png"
	"os"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"

	xdraw "golang.org/x/image/draw"
)

func loadLocalRGBA(t *testing.T, path string) *image.RGBA {
	t.Helper()
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
	out := image.NewRGBA(src.Bounds())
	for y := out.Bounds().Min.Y; y < out.Bounds().Max.Y; y++ {
		for x := out.Bounds().Min.X; x < out.Bounds().Max.X; x++ {
			out.Set(x, y, src.At(x, y))
		}
	}
	return out
}

// The supplied duel frame is 2/4 on both sides. Hidden titles are synthetic
// occlusions to verify current portrait recognition, not additional gameplay.
func TestMinatoKyubiDuelScreenshot(t *testing.T) {
	src := loadLocalRGBA(t, "../../../inbox/regressions/duel-minato-kyubi-20261106/game.png")
	for _, width := range []int{960, 1280, 1600, 1920, 2560} {
		for _, hideTitles := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/hideTitles=%v", width, hideTitles), func(t *testing.T) {
				img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
				xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
				cfg := config.Default()
				layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
				if hideTitles {
					area, _ := layout.ContentArea(img)
					positions := layout.PositionsInImage("duel", img, area)
					for _, row := range [][]detect.BeadPosition{positions[:4], positions[4:]} {
						p := row[0]
						roi := ninja.NameRegion(image.Pt(p.X, p.Y), detect.RowHUDScale(row, area, 960, 540), p.Side == "left")
						draw.Draw(img, roi, image.Black, image.Point{}, draw.Src)
					}
				}
				e := NewConfigured(layout, cfg.Vision)
				e.Prefer("duel")
				for _, ms := range []int{0, 20, 100, 600} {
					got := e.AnalyzeAt(img, time.Unix(100, 0).Add(time.Duration(ms)*time.Millisecond))
					left, right := countReady(got.Beads)
					if got.LeftNinja != ninja.MinatoKyubi || got.RightNinja != ninja.MinatoKyubi || got.LeftSlots != 4 || got.RightSlots != 4 || left != 2 || right != 2 || knownCount(got.Beads) != 8 {
						t.Fatalf("frame %d: names=%q/%q want 2/2: %+v", ms, got.LeftNinja, got.RightNinja, got.Beads)
					}
				}
			})
		}
	}
}

// The training HUD visibly includes Minato's energy gauge. Only a currently
// verified complete title can move that side's sampling row. This capture
// verifies BOTH complete titles (the training partner also runs
// 波风水门[九喇嘛连结]); a row without a verified variant stays neutral, as
// covered by TestEnergyGaugeOffsetIsExactVariantBound (the guarded identity-free
// re-read is separate: energy_gauge_test.go).
func TestMinatoKyubiEnergyGaugeMovesOnlyVerifiedSideRow(t *testing.T) {
	img := loadLocalRGBA(t, "../../../tmp/user-frames/minato-energy-training.png")
	cfg, err := config.Load("../../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	e := NewConfigured(layout, cfg.Vision)
	e.Prefer("camp")
	got := e.AnalyzeAt(img, time.Unix(1700000000, 0))
	if got.LeftNinja != ninja.MinatoKyubi || got.RightNinja != ninja.MinatoKyubi || got.LeftSlots != 4 || got.RightSlots != 4 {
		t.Fatalf("identity/topology=%+v", got)
	}
	for _, bead := range got.Beads {
		if bead.Y != 100 {
			t.Fatalf("verified row did not shift uniformly: %+v", bead)
		}
	}
}

// The same geometry applies on either side, but only to the two exact
// energy-gauge variants. Edo and an absent/unverified title remain neutral.
func TestEnergyGaugeOffsetIsExactVariantBound(t *testing.T) {
	if ninja.EnergyGaugeRowOffset != 13 {
		t.Fatalf("unexpected reviewed offset %v", ninja.EnergyGaugeRowOffset)
	}
	positions := []detect.BeadPosition{
		{Bead: detect.Bead{Side: "left", Idx: 0, LX: 155, LY: 101}, X: 93, Y: 61},
		{Bead: detect.Bead{Side: "left", Idx: 1, LX: 180, LY: 101}, X: 108, Y: 61},
		{Bead: detect.Bead{Side: "right", Idx: 0, LX: 1391, LY: 101}, X: 835, Y: 61},
		{Bead: detect.Bead{Side: "right", Idx: 1, LX: 1366, LY: 101}, X: 820, Y: 61},
	}
	for _, tc := range []struct {
		name      string
		readouts  [2]ninja.Readout
		wantLeft  int
		wantRight int
	}{
		{"left Minato", [2]ninja.Readout{{Name: ninja.MinatoKyubi, RowOffsetY: ninja.EnergyGaugeRowOffset}, {}}, 63, 61},
		{"right Minato", [2]ninja.Readout{{}, {Name: ninja.MinatoKyubi, RowOffsetY: ninja.EnergyGaugeRowOffset}}, 61, 63},
		{"left Itachi", [2]ninja.Readout{{Name: ninja.ItachiHyakusen, RowOffsetY: ninja.EnergyGaugeRowOffset}, {}}, 63, 61},
		{"right Itachi", [2]ninja.Readout{{}, {Name: ninja.ItachiHyakusen, RowOffsetY: ninja.EnergyGaugeRowOffset}}, 61, 63},
		{"Edo remains neutral", [2]ninja.Readout{{Name: ninja.HashiramaEdo}, {Name: ninja.HashiramaEdo}}, 61, 61},
		{"unverified remains neutral", [2]ninja.Readout{{}, {}}, 61, 61},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shifted := applyVerifiedRowOffsets(positions, tc.readouts)
			for _, bead := range shifted {
				want := tc.wantLeft
				if bead.Side == "right" {
					want = tc.wantRight
				}
				if bead.Y != want {
					t.Fatalf("%s%d moved to y=%d, want %d: %+v", bead.Side, bead.Idx+1, bead.Y, want, shifted)
				}
			}
		})
	}
}

func applyVerifiedRowOffsets(in []detect.BeadPosition, readouts [2]ninja.Readout) []detect.BeadPosition {
	out := append([]detect.BeadPosition(nil), in...)
	for i := range out {
		index := 0
		if out[i].Side == "right" {
			index = 1
		}
		if offset := readouts[index].RowOffsetY; offset != 0 {
			out[i].LY += offset * detect.LogicHeight / 540
			out[i].Y = int(out[i].LY * 540 / detect.LogicHeight)
		}
	}
	return out
}
