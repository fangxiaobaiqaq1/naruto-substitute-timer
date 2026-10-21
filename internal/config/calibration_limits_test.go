package config

import (
	"math"
	"strings"
	"testing"
)

// automaticBoxes are the boxes the engine reports for a w×h frame without
// letterbox bars (docs/区域校准.md 每帧几何): beads 4 pitches by 1 at the HUD
// scale min(W/1600, H/900), pitch 25; the portrait 68.5 at min(W/960, H/540).
func automaticBoxes(w, h float64) (beads, avatar NormalizedRect) {
	s := math.Min(w/1600, h/900)
	a := math.Min(w/960, h/540)
	return NormalizedRect{X: 0.1, Y: 0.1, Width: 4 * 25 * s / w, Height: 25 * s / h},
		NormalizedRect{X: 0.01, Y: 0.01, Width: 68.5 * a / w, Height: 68.5 * a / h}
}

// The rule does not depend on the frame: on every size and aspect the
// automatic box is k = 1, and a box k times as big is accepted exactly for
// 1/2 ≤ k ≤ 2, whatever the frame.
func TestCalibrationLimitsAreFrameIndependent(t *testing.T) {
	sizes := [][2]float64{{1280, 720}, {1920, 1080}, {2400, 1080}, {3200, 1440}, {2316, 904}, {3840, 1080}, {5120, 1440},
		{1024, 768}, {2048, 1536}, {2388, 1668}, {2560, 1600}, {2208, 1768}, {1840, 2208 * 0.95}, {1600, 1600}, {1024, 461}, {2880, 1800}}
	for _, size := range sizes {
		beads, avatar := automaticBoxes(size[0], size[1])
		for _, tc := range []struct {
			avatar bool
			box    NormalizedRect
		}{{false, beads}, {true, avatar}} {
			u, v := CalibrationBoxUnits(tc.avatar, tc.box)
			if k := math.Max(u, v); math.Abs(k-1) > 1e-12 {
				t.Fatalf("%v avatar=%v: automatic box k=%v (u=%v v=%v)", size, tc.avatar, k, u, v)
			}
			for _, f := range []float64{0.51, 0.75, 1, 1.25, 1.5, 1.99} {
				r := tc.box
				r.Width, r.Height = r.Width*f, r.Height*f
				if issue := CalibrationRectIssue(tc.avatar, r); issue != "" {
					t.Fatalf("%v avatar=%v ×%v rejected: %s", size, tc.avatar, f, issue)
				}
			}
			for f, want := range map[float64]string{2.01: "太大", 3: "太大", 16: "太大", 0.49: "太小", 0.1: "太小"} {
				r := tc.box
				r.Width, r.Height = r.Width*f, r.Height*f
				if issue := CalibrationRectIssue(tc.avatar, r); !strings.Contains(issue, want) {
					t.Fatalf("%v avatar=%v ×%v: %q, want %q", size, tc.avatar, f, issue, want)
				}
			}
		}
	}
}

func TestCalibrationLimitsShapeAndMessages(t *testing.T) {
	for _, tc := range []struct {
		avatar bool
		r      NormalizedRect
		want   string
	}{
		{false, NormalizedRect{X: 0.081, Y: 0.104, Width: 0.052, Height: 0.022}, ""},     // docs example
		{true, NormalizedRect{X: 0.924, Y: 0.006, Width: 0.072, Height: 0.128}, ""},      // docs example
		{false, NormalizedRect{X: 0.1, Y: 0.1, Width: 0.0625, Height: 0.006}, "豆框太扁或太窄"}, // a line through the bead centres: v = 0.216 < k/4
		{true, NormalizedRect{X: 0, Y: 0, Width: 1, Height: 1}, "头像框太大：是自动框的 14.01 倍"},   // whole frame
		{false, NormalizedRect{X: 0, Y: 0, Width: 0.95, Height: 0.95}, "豆框太大：是自动框的 34.20 倍"},
		{false, NormalizedRect{X: 0.1, Y: 0.1, Width: 0.01, Height: 0.005}, "豆框太小：只有自动框的 0.18 倍"},
		{true, NormalizedRect{X: 0.9, Y: 0, Width: 0.01, Height: 0.12}, "头像框太扁或太窄：宽高按自动框折算相差 6.8 倍"},
		{false, NormalizedRect{X: 0.1, Y: 0.1, Width: 0.004, Height: 0.03}, "豆框太扁或太窄"},
	} {
		got := CalibrationRectIssue(tc.avatar, tc.r)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%+v avatar=%v: %q, want %q", tc.r, tc.avatar, got, tc.want)
		}
	}
	// Exactly on a limit is accepted, one ulp past it is not (u = 16w is
	// exact): k = 2, k = 1/2, short side = k/4 (v = 36·5/128 = 1.40625 exactly).
	for _, tc := range []struct {
		r    NormalizedRect
		past float64 // the width one ulp past the limit
	}{
		{NormalizedRect{Width: 0.125, Height: 0.04}, math.Nextafter(0.125, 1)},
		{NormalizedRect{Width: 0.03125, Height: 0.01}, math.Nextafter(0.03125, 0)},
		{NormalizedRect{Width: 0.02197265625, Height: 0.0390625}, math.Nextafter(0.02197265625, 0)},
	} {
		if issue := CalibrationRectIssue(false, tc.r); issue != "" {
			t.Errorf("bead box on the limit %+v: %s", tc.r, issue)
		}
		past := tc.r
		past.Width = tc.past
		if CalibrationRectIssue(false, past) == "" {
			t.Errorf("bead box one ulp past the limit accepted: %+v", past)
		}
	}
}

func TestValidateRejectsCalibrationBoxesOutsideSizeLimits(t *testing.T) {
	c := Default()
	c.Layout.Calibration = &RegionCalibration{Profile: "duel",
		LeftBeads:   &NormalizedRect{X: 0.081, Y: 0.104, Width: 0.052, Height: 0.022},
		RightAvatar: &NormalizedRect{X: 0.924, Y: 0.006, Width: 0.072, Height: 0.128}}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	c.Layout.Calibration.LeftAvatar = &NormalizedRect{X: 0, Y: 0, Width: 0.25, Height: 0.45}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "layout.calibration.leftAvatar") || !strings.Contains(err.Error(), "太大") {
		t.Fatalf("Validate = %v", err)
	}
	c.Layout.Calibration.LeftAvatar = nil
	c.Layout.Calibration.RightBeads = &NormalizedRect{X: 0.9, Y: 0.1, Width: 0.004, Height: 0.003}
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "layout.calibration.rightBeads") || !strings.Contains(err.Error(), "太小") {
		t.Fatalf("Validate = %v", err)
	}
}

// splitmix64 is the vector generator shared with the C++ host test
// (android/app/src/main/cpp/tests/native_fixes_test.cpp) and Kotlin
// (android/tools/limitscheck): + - * / only, so every language draws the same
// boxes bit for bit.
type splitmix64 uint64

func (s *splitmix64) next() uint64 {
	*s += 0x9E3779B97F4A7C15
	z := uint64(*s)
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (s *splitmix64) unit() float64 { return float64(s.next()>>11) * 0x1p-53 }

// calibrationDecision is the engine's decision for one box: 0 usable,
// 1 outside the frame, 2 too large, 3 too small, 4 bad shape.
func calibrationDecision(avatar bool, r NormalizedRect) byte {
	if !(r.X >= 0 && r.Y >= 0 && r.Width > 0 && r.Height > 0 && r.X+r.Width <= 1 && r.Y+r.Height <= 1) {
		return 1
	}
	switch issue := CalibrationRectIssue(avatar, r); {
	case issue == "":
		return 0
	case strings.Contains(issue, "太大"):
		return 2
	case strings.Contains(issue, "太小"):
		return 3
	}
	return 4
}

// calibrationDecisionDigest decides n generated boxes (as float64 and, like
// Android's stored boxes, rounded through float32) and digests the decisions
// with FNV-1a. CalibrationVectorsDigest pins the result in every language.
func calibrationDecisionDigest(n int, float32Inputs bool) (uint64, [5]int) {
	g := splitmix64(20261021)
	digest := uint64(14695981039346656037)
	var counts [5]int
	for i := range n {
		avatar := i%2 == 1
		wn, hn := 0.0625, 1.0/36
		if avatar {
			wn, hn = 68.5/960, 68.5/540
		}
		k := 0.2 + 2.2*g.unit() // overall size, then each side ×0.5…1.5
		w := wn * k * (0.5 + g.unit())
		h := hn * k * (0.5 + g.unit())
		switch g.next() % 8 { // exactly on a limit, as far as rounding goes
		case 0:
			w = wn * 2
		case 1:
			h = hn * 0.5
		case 2:
			w, h = wn*2, hn*0.5
		}
		x := (1 - w) * g.unit()
		y := (1 - h) * g.unit()
		switch g.next() % 16 {
		case 0:
			x = 1 - w // on the right edge (or one rounding past it)
		case 1:
			y = 1 - h + 1e-9 // past the bottom edge
		}
		r := NormalizedRect{X: x, Y: y, Width: w, Height: h}
		if float32Inputs {
			r = NormalizedRect{X: float64(float32(x)), Y: float64(float32(y)), Width: float64(float32(w)), Height: float64(float32(h))}
		}
		d := calibrationDecision(avatar, r)
		counts[d]++
		digest = (digest ^ uint64(d)) * 1099511628211
	}
	return digest, counts
}

// CalibrationVectorsDigest: the same decisions in Go, C++ and Kotlin.
const (
	calibrationVectors        = 200000
	calibrationVectorsDigest  = 0xe9e1f0fa7bdcdb2d
	calibrationVectorsDigestF = 0x5c07b936a6c3ad61
)

func TestCalibrationLimitsMatchTheOtherLanguages(t *testing.T) {
	for _, f32 := range []bool{false, true} {
		digest, counts := calibrationDecisionDigest(calibrationVectors, f32)
		want := uint64(calibrationVectorsDigest)
		if f32 {
			want = calibrationVectorsDigestF
		}
		t.Logf("float32=%v digest=%#x counts=%v", f32, digest, counts)
		if digest != want {
			t.Errorf("float32=%v: digest %#x, want %#x (update the C++ and Kotlin checks too)", f32, digest, want)
		}
		for d, n := range counts {
			if n < calibrationVectors/100 {
				t.Errorf("decision %d only %d times: the vectors do not cover it", d, n)
			}
		}
	}
}
