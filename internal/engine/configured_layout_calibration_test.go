package engine

import (
	"image"
	"math"
	"slices"
	"sync"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
)

func TestCalibrationUnsetKeepsAutomaticPositions(t *testing.T) {
	cfg := config.Default().Layout
	plain := NewConfiguredLayout(detect.ModeAuto, cfg)
	cleared := NewConfiguredLayout(detect.ModeAuto, cfg)
	cleared.SetCalibration(nil)
	invalid := cfg
	invalid.Calibration = &config.RegionCalibration{Profile: "lobby", LeftBeads: &config.NormalizedRect{X: .1, Y: .1, Width: .1, Height: .1}}
	ignored := NewConfiguredLayout(detect.ModeAuto, invalid)
	// Inside the frame but outside the size limits (config.CalibrationRectIssue,
	// docs/区域校准.md 框的大小限制): every box stays automatic, from the
	// config and from a live SetCalibration alike.
	oversized := &config.RegionCalibration{Profile: "duel",
		LeftBeads:   &config.NormalizedRect{X: 0, Y: 0, Width: 1, Height: 1},
		RightBeads:  &config.NormalizedRect{X: 0.8, Y: 0.1, Width: 0.004, Height: 0.003},
		LeftAvatar:  &config.NormalizedRect{X: 0, Y: 0, Width: 0.25, Height: 0.45},
		RightAvatar: &config.NormalizedRect{X: 0.9, Y: 0, Width: 0.01, Height: 0.12}}
	tooBig := cfg
	tooBig.Calibration = oversized
	rejected := NewConfiguredLayout(detect.ModeAuto, tooBig)
	rejectedLive := NewConfiguredLayout(detect.ModeAuto, cfg)
	rejectedLive.SetCalibration(oversized)
	for _, size := range []image.Point{{1280, 720}, {1920, 1080}, {2400, 1080}, {2388, 1668}} {
		img := image.NewRGBA(image.Rectangle{Max: size})
		for _, profile := range []string{"camp", "duel"} {
			area, _ := plain.ContentArea(img)
			want := plain.PositionsInImage(profile, img, area)
			for _, l := range []*ConfiguredLayout{cleared, ignored, plain.Pinned(), rejected, rejectedLive} {
				if got := l.PositionsInImage(profile, img, area); !slices.Equal(got, want) {
					t.Fatalf("%v %s: %+v != %+v", size, profile, got, want)
				}
				if got := l.PositionsFor(profile, size.X, size.Y); !slices.Equal(got, plain.PositionsFor(profile, size.X, size.Y)) {
					t.Fatalf("%v %s size-only positions changed", size, profile)
				}
			}
			for _, p := range want {
				if p.Cal != nil {
					t.Fatalf("automatic position carries calibration: %+v", p)
				}
			}
		}
	}
}

// docs/区域校准.md: bead i of the box sits at rx+(i+.5)·rw/4 (mirrored on the
// right); s = (rw·W/4)/pitch_P; the other profile uses the same transform.
func TestCalibrationBeadBoxTransformBothProfiles(t *testing.T) {
	cfg := config.Default().Layout
	const w, h = 2388, 1668
	left := &config.NormalizedRect{X: 300.0 / w, Y: 240.0 / h, Width: 4 * 44.0 / w, Height: 20.0 / h}
	right := &config.NormalizedRect{X: 1900.0 / w, Y: 260.0 / h, Width: 4 * 40.0 / w, Height: 22.0 / h}
	cfg.Calibration = &config.RegionCalibration{Profile: "duel", LeftBeads: left, RightBeads: right}
	l := NewConfiguredLayout(detect.ModeAuto, cfg)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	area, _ := l.ContentArea(img)
	duel := l.PositionsInImage("duel", img, area)
	// Duel bead 0 is exactly the outermost box centre; scale from the pitch.
	if duel[0].X != 322 || duel[0].Y != 250 || duel[4].X != 2040 || duel[4].Y != 271 {
		t.Fatalf("bead 0 not at the box centres: %+v / %+v", duel[0], duel[4])
	}
	sl, sr := 44/(77.0/3), 40/25.0
	if math.Abs(duel[0].Cal.Scale-sl) > 1e-9 || math.Abs(duel[4].Cal.Scale-sr) > 1e-9 {
		t.Fatalf("scales %v/%v, want %v/%v", duel[0].Cal.Scale, duel[4].Cal.Scale, sl, sr)
	}
	for _, tc := range []struct {
		profile   string
		positions []detect.BeadPosition
	}{{"duel", duel}, {"camp", l.PositionsInImage("camp", img, area)}} {
		ref := cfg.Profile(tc.profile)
		for _, p := range tc.positions {
			c, x0, y0, rx, s := ref.Left.NominalCenters[p.Idx], 322.0, 250.0, 172.0, sl
			if p.Side == "right" {
				c, x0, y0, rx, s = ref.Right.NominalCenters[p.Idx], 2040.0, 271.0, 1423.0, sr
			}
			want := image.Pt(int(math.Round(x0+s*(c.X*1600-rx))), int(math.Round(y0+s*(c.Y*900-103))))
			if image.Pt(p.X, p.Y) != want || p.Cal == nil {
				t.Fatalf("%s %s%d at (%d,%d), want %v", tc.profile, p.Side, p.Idx, p.X, p.Y, want)
			}
		}
	}
	// Size-only callers honour the same boxes.
	if got := l.PositionsFor("duel", w, h); got[0].X != 322 || got[4].X != 2040 {
		t.Fatalf("PositionsFor ignored calibration: %+v", got)
	}
}

// A portrait box drawn on one profile moves with bead 0 on the other: by the
// calibrated transform, or by the automatic layout on an uncalibrated side.
func TestCalibrationPortraitFollowsBeadZero(t *testing.T) {
	cfg := config.Default().Layout
	const w, h = 1920, 1080
	box := &config.NormalizedRect{X: 60.0 / w, Y: 30.0 / h, Width: 140.0 / w, Height: 138.0 / h}
	cfg.Calibration = &config.RegionCalibration{Profile: "duel", LeftAvatar: box, RightAvatar: box,
		RightBeads: &config.NormalizedRect{X: 1500.0 / w, Y: 110.0 / h, Width: 4 * 36.0 / w, Height: 20.0 / h}}
	l := NewConfiguredLayout(detect.ModeAuto, cfg)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	area, _ := l.ContentArea(img)
	duel, camp := l.PositionsInImage("duel", img, area), l.PositionsInImage("camp", img, area)
	if a := duel[0].Cal.Avatar; a != (detect.PixelBox{X: 60, Y: 30, W: 140, H: 138}) || duel[0].Cal.Beads() {
		t.Fatalf("duel portrait box %+v", duel[0].Cal)
	}
	// Left beads are automatic: camp bead 0 is (155-172, 101-103) units away.
	auto := area.HUDScale(1600, 900)
	if a := camp[0].Cal.Avatar; math.Abs(a.X-(60+auto*(155-172))) > 1e-9 || math.Abs(a.Y-(30+auto*(101-103))) > 1e-9 || a.W != 140 {
		t.Fatalf("camp left portrait %+v", a)
	}
	s := 36 / 25.0
	if a := camp[4].Cal.Avatar; math.Abs(a.X-(60+s*(1391-1423))) > 1e-9 || math.Abs(a.Y-(30+s*(101-103))) > 1e-9 {
		t.Fatalf("camp right portrait %+v", a)
	}
}

func TestCalibrationLiveReplacementIsPinnedPerFrame(t *testing.T) {
	cfg := config.Default().Layout
	l := NewConfiguredLayout(detect.ModeAuto, cfg)
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	area, _ := l.ContentArea(img)
	auto := l.PositionsInImage("duel", img, area)
	frame := l.Pinned()
	c := &config.RegionCalibration{Profile: "duel", LeftBeads: &config.NormalizedRect{X: .2, Y: .2, Width: .1, Height: .02}}
	l.SetCalibration(c)
	c.LeftBeads.X = .5 // the caller's copy is not shared
	if l.CalibrationRevision() == 0 || frame.CalibrationRevision() != 0 {
		t.Fatal("revisions")
	}
	if got := frame.PositionsInImage("duel", img, area); !slices.Equal(got, auto) {
		t.Fatal("a pinned frame saw the replacement")
	}
	got := l.PositionsInImage("duel", img, area)
	if want := int(math.Round((.2 + .1/8) * 1920)); got[0].X != want {
		t.Fatalf("replacement not applied: %+v", got[0])
	}
	// Concurrent replacement while frames read it (run with -race).
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 50 {
				if i == 0 {
					l.SetCalibration(map[bool]*config.RegionCalibration{true: c, false: nil}[j%2 == 0])
				} else {
					l.Pinned().PositionsInImage("camp", img, area)
				}
			}
		})
	}
	wg.Wait()
}
