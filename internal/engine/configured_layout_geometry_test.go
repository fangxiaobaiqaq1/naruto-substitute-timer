package engine

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
)

// paintHPBars draws each side's health bar with its sloping outer end shift
// pixels outward of the automatic bead 0, so wide frames get the HP-bar
// x-correction (see detect.HUDRowOffset).
func paintHPBars(img *image.RGBA, area detect.ContentArea, profile string, positions []detect.BeadPosition, shift int) {
	scale := area.HUDScale(detect.HUDReferenceWidth, detect.HUDReferenceHeight)
	step := max(1, int(math.Round(scale)))
	y := 74.0
	if profile == "camp" {
		y = 70
	}
	cy := area.Y + int(math.Round(y*scale))
	for _, first := range positions {
		if first.Idx != 0 {
			continue
		}
		direction := -1
		if first.Side == "right" {
			direction = 1
		}
		for row := -1; row <= 1; row++ {
			x := first.X - direction*shift + direction*row*step
			x0, x1 := x, x+int(200*scale)
			if direction == 1 {
				x0, x1 = x-int(200*scale)+1, x+1
			}
			py := cy + row*step
			draw.Draw(img, image.Rect(x0, py, x1, py+1), image.NewUniform(color.RGBA{240, 70, 10, 255}), image.Point{}, draw.Src)
		}
	}
}

type geometryInner struct{ stubInner }

func (s *geometryInner) Analyze(img *image.RGBA) Result {
	res := s.stubInner.Analyze(img)
	res.Geometry[1].NameROI = image.Rect(1, 2, 3, 4)
	return res
}

// Gated passes the inner geometry through; a page it does not sample has none.
func TestGatedPassesGeometry(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for kind, want := range map[GateKind]bool{GateFight: true, GateUncertain: true, GateNotFight: false} {
		g := NewGated(stubGate{d: GateDecision{Kind: kind, SceneID: "x"}}, &geometryInner{stubInner{name: "rgb", beads: 4}})
		if got := g.Analyze(img).Geometry[1].NameROI == image.Rect(1, 2, 3, 4); got != want {
			t.Fatalf("gate %v: geometry passed=%v", kind, got)
		}
	}
}

func sideRows(positions []detect.BeadPosition) [2][]detect.BeadPosition {
	var rows [2][]detect.BeadPosition
	for _, p := range positions {
		rows[sideIndex(p.Side)] = append(rows[sideIndex(p.Side)], p)
	}
	return rows
}

func float32Rect(r config.NormalizedRect) config.NormalizedRect {
	return config.NormalizedRect{X: float64(float32(r.X)), Y: float64(float32(r.Y)), Width: float64(float32(r.Width)), Height: float64(float32(r.Height))}
}

// affineRow reports whether one bead-0 point rounds every centre of row back
// at scale s (with the box margin). Automatic rows that round two half-pixel
// ties apart (camp left at scale 0.9 and 1.5) have none.
func affineRow(row []detect.BeadPosition, s float64) bool {
	xlo, xhi, ylo, yhi := math.Inf(-1), math.Inf(1), math.Inf(-1), math.Inf(1)
	for _, b := range row {
		dx, dy := s*(b.LX-row[0].LX), s*(b.LY-row[0].LY)
		xlo, xhi = max(xlo, float64(b.X)-.5-dx), min(xhi, float64(b.X)+.5-dx)
		ylo, yhi = max(ylo, float64(b.Y)-.5-dy), min(yhi, float64(b.Y)+.5-dy)
	}
	return xhi-xlo > 2*settleMargin && yhi-ylo > 2*settleMargin
}

// docs/区域校准.md 每帧几何: a default bead box saved unchanged as this
// profile's calibration reproduces the automatic (or calibrated) centres and
// scale exactly; also from Android's float32 storage. Includes half-pixel
// ties (1440x900, 1680x1050, 2400x1350), the HP-bar x-correction (wide
// frames) and a letterboxed frame with a non-zero origin.
func TestDefaultBeadBoxReproducesRows(t *testing.T) {
	cfg := config.Default().Layout
	existing := &config.RegionCalibration{Profile: "duel",
		LeftBeads:  &config.NormalizedRect{X: .127, Y: .143, Width: .081, Height: .019},
		RightBeads: &config.NormalizedRect{X: .79, Y: .151, Width: .07, Height: .02}}
	corrected := 0
	var inconsistent []string
	for _, bounds := range []image.Rectangle{
		image.Rect(0, 0, 1280, 720), image.Rect(0, 0, 1440, 900), image.Rect(0, 0, 1600, 900), image.Rect(0, 0, 1680, 1050), image.Rect(0, 0, 1920, 1080),
		image.Rect(0, 0, 2160, 1080), image.Rect(0, 0, 2400, 1080), image.Rect(0, 0, 2400, 1350), image.Rect(0, 0, 2388, 1668),
		image.Rect(0, 0, 2880, 1800), image.Rect(0, 0, 3440, 1440), image.Rect(17, 23, 17+2340, 23+1080),
	} {
		for _, profile := range []string{"camp", "duel"} {
			for _, calibration := range []*config.RegionCalibration{nil, existing} {
				name := fmt.Sprintf("%v/%s/calibrated=%v", bounds, profile, calibration != nil)
				layout := cfg
				layout.Calibration = calibration
				l := NewConfiguredLayout(detect.ModeAuto, layout)
				img := image.NewRGBA(bounds)
				if bounds.Min == (image.Point{}) {
					draw.Draw(img, bounds, image.NewUniform(color.RGBA{32, 32, 32, 255}), image.Point{}, draw.Src)
				}
				area, _ := l.ContentArea(img)
				if (bounds.Min != image.Point{}) == (area.X == bounds.Min.X) {
					t.Fatalf("%s: content area %+v", name, area)
				}
				plain := l.PositionsIn(profile, area)
				paintHPBars(img, area, profile, plain, int(math.Round(14*area.HUDScale(detect.HUDReferenceWidth, detect.HUDReferenceHeight))))
				want := l.PositionsInImage(profile, img, area)
				if calibration == nil && want[0].X != plain[0].X {
					corrected++
				}
				rows := sideRows(want)
				var boxes [2]config.NormalizedRect
				for side, row := range rows {
					box, ok := l.DefaultBeadBox(profile, row, img.Bounds(), area)
					w, h := float64(bounds.Dx()), float64(bounds.Dy())
					x0, x1 := float64(bounds.Min.X)+box.X*w, float64(bounds.Min.X)+(box.X+box.Width)*w
					cy := float64(bounds.Min.Y) + (box.Y+box.Height/2)*h
					pitch := box.Width * w / 4
					if !ok || box.X < 0 || box.Y < 0 || box.X+box.Width > 1 || box.Y+box.Height > 1 || math.Abs(box.Height*h-pitch) > 1e-9 ||
						math.Abs(cy-float64(row[0].Y)) > .5 || float64(min(row[0].X, row[3].X)) < x0+pitch/2-.5 || float64(max(row[0].X, row[3].X)) > x1-pitch/2+.5 {
						t.Fatalf("%s side %d: box %+v (ok=%v) does not enclose %+v", name, side, box, ok, row)
					}
					boxes[side] = box
				}
				var exact [2]bool
				for side, row := range rows {
					if exact[side] = affineRow(row, detect.RowHUDScale(row, area, detect.HUDReferenceWidth, detect.HUDReferenceHeight)); !exact[side] {
						inconsistent = append(inconsistent, fmt.Sprintf("%dx%d/%s/%s/%v", bounds.Dx(), bounds.Dy(), profile, row[0].Side, calibration != nil))
					}
				}
				for _, store := range []struct {
					name      string
					tolerance float64 // relative scale error
					convert   func(config.NormalizedRect) config.NormalizedRect
				}{{"float64", 1e-12, func(r config.NormalizedRect) config.NormalizedRect { return r }}, {"float32", 1e-6, float32Rect}} {
					left, right := store.convert(boxes[0]), store.convert(boxes[1])
					saved := cfg
					saved.Calibration = &config.RegionCalibration{Profile: profile, LeftBeads: &left, RightBeads: &right}
					got := NewConfiguredLayout(detect.ModeAuto, saved).PositionsInImage(profile, img, area)
					if len(got) != len(want) {
						t.Fatalf("%s: %d positions, want %d", name, len(got), len(want))
					}
					for i := range got {
						d := image.Pt(got[i].X, got[i].Y).Sub(image.Pt(want[i].X, want[i].Y))
						// No exact box: bead 0 (the name/portrait anchor) stays exact.
						loose := !exact[sideIndex(got[i].Side)] && got[i].Idx > 0 && max(d.X, -d.X, d.Y, -d.Y) <= 1
						if (d != image.Point{} && !loose) || !got[i].Cal.Beads() {
							t.Fatalf("%s %s: %s%d at (%d,%d), want (%d,%d)", name, store.name, got[i].Side, got[i].Idx, got[i].X, got[i].Y, want[i].X, want[i].Y)
						}
					}
					gotRows := sideRows(got)
					for side := range rows {
						for _, ref := range [][2]float64{{1600, 900}, {1920, 1080}, {960, 540}} {
							a, b := detect.RowHUDScale(rows[side], area, ref[0], ref[1]), detect.RowHUDScale(gotRows[side], area, ref[0], ref[1])
							if math.Abs(a-b) > store.tolerance*a {
								t.Fatalf("%s %s side %d: scale@%v %v, want %v", name, store.name, side, ref, b, a)
							}
						}
					}
				}
			}
		}
	}
	if corrected != 6 {
		t.Fatalf("HP-bar x-correction applied on %d frames, want every wide one", corrected)
	}
	if want := "[1440x900/camp/left/false 2400x1350/camp/left/false]"; fmt.Sprint(inconsistent) != want {
		t.Fatalf("rows without an exact box: %v, want %s", inconsistent, want)
	}
}
