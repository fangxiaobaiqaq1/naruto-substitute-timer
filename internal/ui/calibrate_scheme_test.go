package ui

import (
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"narutotimer/internal/config"
)

// docSchemeExample is the scheme file example of docs/区域校准.md.
func docSchemeExample(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile("../../docs/区域校准.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(doc), "### 方案文件")
	if !ok {
		t.Fatal("docs/区域校准.md has no 方案文件 section")
	}
	_, block, ok := strings.Cut(section, "```json\n")
	if !ok {
		t.Fatal("no JSON example in 方案文件")
	}
	block, _, _ = strings.Cut(block, "```")
	return block
}

// The file layout is the documented one, byte for byte: Android reads and
// writes the same files.
func TestCalibrationSchemeMatchesDocExample(t *testing.T) {
	scheme := calibrationScheme{FrameWidth: 2400, FrameHeight: 1080, Calibration: &config.RegionCalibration{Profile: "duel",
		LeftBeads:   &config.NormalizedRect{X: 0.081, Y: 0.104, Width: 0.052, Height: 0.022},
		RightAvatar: &config.NormalizedRect{X: 0.924, Y: 0.006, Width: 0.072, Height: 0.128}}}
	data, err := encodeCalibrationScheme(scheme)
	if err != nil {
		t.Fatal(err)
	}
	if want := docSchemeExample(t); string(data) != want {
		t.Fatalf("encoded\n%s\nwant (docs/区域校准.md)\n%s", data, want)
	}
	got, err := decodeCalibrationScheme([]byte(docSchemeExample(t)))
	if err != nil || !reflect.DeepEqual(got, scheme) {
		t.Fatalf("doc example decodes to %+v %+v, %v", got, got.Calibration, err)
	}
}

func TestCalibrationSchemeRoundTripsExactly(t *testing.T) {
	third := 1.0 / 3
	for _, c := range []*config.RegionCalibration{
		{Profile: "camp", LeftBeads: &config.NormalizedRect{X: 0.07125, Y: 0.09833333333333333, Width: 0.049999999999999975, Height: 0.027777777777777766}},
		{Profile: "duel",
			LeftBeads:   &config.NormalizedRect{X: third, Y: 0.1 + 0.2, Width: 1e-7, Height: third / 7},
			RightBeads:  &config.NormalizedRect{X: 0.8511666666666665, Y: 0.0981, Width: 0.05, Height: 0.028},
			LeftAvatar:  &config.NormalizedRect{X: 0, Y: 0, Width: 1, Height: 1},
			RightAvatar: &config.NormalizedRect{X: math.Nextafter(1, 0) - 0.25, Y: 0.5, Width: 0.25, Height: 0.5}},
		{Profile: "camp"}, // every box automatic
	} {
		in := calibrationScheme{Calibration: c, FrameWidth: 2388, FrameHeight: 1668}
		data, err := encodeCalibrationScheme(in)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		out, err := decodeCalibrationScheme(data)
		if err != nil || !reflect.DeepEqual(out, in) {
			t.Fatalf("round trip of\n%s\ngave %+v (%v)", data, out.Calibration, err)
		}
	}
	// Nothing is written that could not be read back.
	for _, bad := range []calibrationScheme{
		{FrameWidth: 1, FrameHeight: 1},
		{Calibration: &config.RegionCalibration{Profile: "lobby"}, FrameWidth: 1, FrameHeight: 1},
		{Calibration: &config.RegionCalibration{Profile: "duel"}},
		{Calibration: &config.RegionCalibration{Profile: "duel", RightBeads: &config.NormalizedRect{X: 0.9, Y: 0.1, Width: 0.2, Height: 0.1}}, FrameWidth: 1, FrameHeight: 1},
		{Calibration: &config.RegionCalibration{Profile: "duel", RightBeads: &config.NormalizedRect{X: math.NaN(), Y: 0.1, Width: 0.2, Height: 0.1}}, FrameWidth: 1, FrameHeight: 1},
	} {
		if data, err := encodeCalibrationScheme(bad); err == nil {
			t.Fatalf("encoded an invalid scheme: %s", data)
		}
	}
}

func TestCalibrationSchemeDecodingIsStrictAboutContentOnly(t *testing.T) {
	box := `{"x": 0.1, "y": 0.1, "width": 0.05, "height": 0.02}`
	for name, file := range map[string]string{
		"BOM, unknown keys, null box": "\xef\xbb\xbf" + `{"kind": "naruto-timer-calibration", "version": 1, "profile": "camp", "app": "android 0.24",
			"leftBeads": ` + box + `, "rightBeads": null, "frameWidth": 2400.0, "frameHeight": 1080}`,
		"no frame size": `{"kind": "naruto-timer-calibration", "version": 1.0, "profile": "camp", "leftBeads": ` + box + `}`,
	} {
		got, err := decodeCalibrationScheme([]byte(file))
		if err != nil || got.Calibration.Profile != "camp" || got.Calibration.LeftBeads == nil || got.Calibration.RightBeads != nil {
			t.Fatalf("%s: %+v %v", name, got.Calibration, err)
		}
		if name == "no frame size" && (got.FrameWidth != 0 || got.FrameHeight != 0) {
			t.Fatalf("%s: frame %dx%d", name, got.FrameWidth, got.FrameHeight)
		} else if name != "no frame size" && (got.FrameWidth != 2400 || got.FrameHeight != 1080) {
			t.Fatalf("%s: frame %dx%d", name, got.FrameWidth, got.FrameHeight)
		}
	}

	head := `"kind": "naruto-timer-calibration", "version": 1, "frameWidth": 2400, "frameHeight": 1080, "profile": "duel"`
	for _, tc := range []struct{ name, file, want string }{
		{"not JSON", `kind=naruto-timer-calibration`, "不是 JSON"},
		{"array", `[1, 2]`, "不是 JSON"},
		{"null", `null`, "不是 JSON"},
		{"wrong kind", `{"kind": "narutotimer-config", "version": 1, "profile": "duel"}`, "kind"},
		{"no kind", `{"version": 1, "profile": "duel"}`, "kind"},
		{"newer version", `{"kind": "naruto-timer-calibration", "version": 2, "profile": "duel"}`, "版本 2 不支持"},
		{"version as text", `{"kind": "naruto-timer-calibration", "version": "1", "profile": "duel"}`, "version"},
		{"no version", `{"kind": "naruto-timer-calibration", "profile": "duel"}`, "version"},
		{"bad profile", `{"kind": "naruto-timer-calibration", "version": 1, "profile": "lobby"}`, "profile"},
		{"no profile", `{"kind": "naruto-timer-calibration", "version": 1}`, "profile"},
		{"past the right edge", `{` + head + `, "rightBeads": {"x": 0.95, "y": 0.1, "width": 0.06, "height": 0.02}}`, "右侧豆（rightBeads）超出画面"},
		{"negative", `{` + head + `, "leftAvatar": {"x": -0.01, "y": 0, "width": 0.07, "height": 0.12}}`, "左侧头像（leftAvatar）超出画面"},
		{"zero height", `{` + head + `, "leftBeads": {"x": 0.1, "y": 0.1, "width": 0.05, "height": 0}}`, "宽高不大于 0"},
		{"missing width", `{` + head + `, "rightAvatar": {"x": 0.9, "y": 0, "height": 0.1}}`, "缺少数值 width"},
		{"text number", `{` + head + `, "rightAvatar": {"x": "0.9", "y": 0, "width": 0.07, "height": 0.1}}`, "缺少数值 x"},
		{"box as array", `{` + head + `, "leftBeads": [0.1, 0.1, 0.05, 0.02]}`, "应为"},
		{"huge", `{"kind": "naruto-timer-calibration", "pad": "` + strings.Repeat("x", calibrationSchemeMaxBytes) + `"}`, "太大"},
	} {
		got, err := decodeCalibrationScheme([]byte(tc.file))
		if !errors.Is(err, errCalibrationScheme) || !strings.Contains(err.Error(), tc.want) || got.Calibration != nil {
			t.Fatalf("%s: %+v, error %v (want %q)", tc.name, got, err, tc.want)
		}
	}
}

func TestCalibrationAspectChange(t *testing.T) {
	for _, tc := range []struct {
		w, h, cw, ch int
		warn         bool
	}{
		{2400, 1080, 2400, 1080, false},
		{1920, 1080, 1280, 720, false}, // same 16:9 at another size
		{2400, 1080, 1920, 1080, true}, // 20:9 vs 16:9, 25%
		{2400, 1080, 2340, 1080, true}, // 2.6%
		{2400, 1080, 2364, 1080, false},
		{2388, 1668, 2400, 1080, true},
		{0, 0, 2400, 1080, false}, // size unknown: nothing to compare
	} {
		if got := calibrationAspectChange(tc.w, tc.h, tc.cw, tc.ch) > calibrationAspectTolerance; got != tc.warn {
			t.Fatalf("%dx%d vs %dx%d: warn=%v (change %.4f)", tc.w, tc.h, tc.cw, tc.ch, got, calibrationAspectChange(tc.w, tc.h, tc.cw, tc.ch))
		}
	}
	if name := calibrationSchemeFileName(2400, 1080); name != "校准方案-2400x1080.json" {
		t.Fatal(name)
	}
}
