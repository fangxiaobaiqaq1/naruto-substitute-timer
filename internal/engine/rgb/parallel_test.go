package rgb

import (
	"image"
	"image/color"
	"image/draw"
	_ "image/png"
	"math"
	"math/rand"
	"os"
	"reflect"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

func decodeRGBA(tb testing.TB, path string) *image.RGBA {
	tb.Helper()
	f, err := os.Open(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		tb.Fatal(err)
	}
	out := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(out, out.Bounds(), src, src.Bounds().Min, draw.Src)
	return out
}

// hudSide is one synthetic HUD side: a shipped title crop, an embedded avatar
// and a bean count. Empty paths leave that element out.
type hudSide struct {
	title, avatar string
	lit           int
}

// syntheticHUDFrame paints a textured pseudo frame with both sides' titles,
// portraits and blue cores at the calibrated profile slots. It is synthetic
// coverage for execution order, not a recognition claim.
func syntheticHUDFrame(tb testing.TB, w, h int, profile string, sides [2]hudSide, seed int64) *image.RGBA {
	tb.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(seed))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*img.Stride + x*4
			base := uint8(40 + (x*97/w+y*61/h)%90)
			n := uint8(rng.Intn(24))
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = base+n, base/2+n, base+20+n, 255
		}
	}
	layout := engine.NewConfiguredLayout(detect.ModeAuto, config.Default().Layout)
	area, ok := layout.ContentArea(img)
	if !ok {
		tb.Fatal("content area")
	}
	scale := float64(area.W) / 960
	for _, p := range layout.PositionsIn(profile, area) {
		index := 0
		if p.Side == "right" {
			index = 1
		}
		side := sides[index]
		if p.Idx == 0 {
			first := image.Pt(p.X, p.Y)
			if side.title != "" {
				name := decodeRGBA(tb, side.title)
				roi := ninja.NameRegion(first, scale, index == 0)
				at := roi.Min.Add(image.Pt(int(12*scale), int(7*scale)))
				box := image.Rectangle{Min: at, Max: at.Add(image.Pt(int(math.Round(float64(name.Bounds().Dx())*scale)), int(math.Round(float64(name.Bounds().Dy())*scale))))}
				xdraw.BiLinear.Scale(img, box, name, name.Bounds(), draw.Src, nil)
			}
			if side.avatar != "" {
				avatar := decodeRGBA(tb, side.avatar)
				roi := ninja.AvatarRegion(first, scale, index == 0)
				size := int(math.Round(float64(avatar.Bounds().Dx()) * scale * .6))
				center := roi.Min.Add(image.Pt(int(math.Round(35.5*scale)), int(math.Round(48*scale))))
				box := image.Rectangle{Min: center.Sub(image.Pt(size/2, size/2)), Max: center.Add(image.Pt(size-size/2, size-size/2))}
				xdraw.BiLinear.Scale(img, box, avatar, avatar.Bounds(), draw.Over, nil)
			}
		}
		c := color.RGBA{R: 30, G: 60, B: 110, A: 255}
		if p.Idx < side.lit {
			c = color.RGBA{R: 40, G: 200, B: 255, A: 255}
		}
		for dy := -8; dy <= 8; dy++ {
			for dx := -6; dx <= 6; dx++ {
				if detect.InBeadDiamond(0, 0, 12, 16, dx, dy) {
					img.SetRGBA(p.X+dx, p.Y+dy, c)
				}
			}
		}
	}
	return img
}

type timedFrame struct {
	img *image.RGBA
	at  time.Time
}

// hudSequence is a multi-frame sequence over one geometry: names and portraits
// on both sides, a brief title gap, a 6-slot title, an energy-row title, a
// duplicate frame and a backwards timestamp, so tracker hints, retries and the
// six-slot/row-offset branches all run.
func hudSequence(tb testing.TB, w, h int, profile string) []timedFrame {
	tb.Helper()
	const names, avatars = "../../ninja/templates/", "../../../assets/avatars/"
	steps := [][2]hudSide{
		{{names + "madara.png", avatars + "90009.png", 4}, {names + "naruto_right.png", avatars + "90010.png", 2}},
		{{names + "madara.png", avatars + "90009.png", 3}, {names + "naruto_right.png", avatars + "90010.png", 2}},
		{{"", avatars + "90009.png", 3}, {names + "naruto_right.png", "", 1}},
		{{names + "hashirama.png", avatars + "90011.png", 5}, {names + "sasuke_xiayin.png", avatars + "90012.png", 4}},
		{{names + "hashirama.png", avatars + "90011.png", 6}, {names + "minato_kyubi.png", avatars + "90013.png", 0}},
		{{names + "obito.png", "", 2}, {"", "", 4}},
	}
	at := time.Unix(1700000000, 0)
	var out []timedFrame
	for i, sides := range steps {
		img := syntheticHUDFrame(tb, w, h, profile, sides, int64(i+1))
		out = append(out, timedFrame{img, at}, timedFrame{img, at.Add(16 * time.Millisecond)})
		at = at.Add(400 * time.Millisecond)
	}
	// A backwards timestamp invalidates tracker hints.
	out = append(out, timedFrame{out[0].img, out[0].at})
	return out
}

func analyzeSequence(e *Engine, frames []timedFrame, parallel bool) []engine.Result {
	defer func(old bool) { parallelSides = old }(parallelSides)
	parallelSides = parallel
	out := make([]engine.Result, 0, len(frames))
	for _, f := range frames {
		out = append(out, e.AnalyzeAt(f.img, f.at))
	}
	return out
}

// Concurrent side and profile sampling must produce exactly the sequential
// results, frame by frame, including tracker state carried between frames.
func TestParallelSidesMatchSequential(t *testing.T) {
	cfg := config.Default()
	configured := func() engine.LayoutProvider { return engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout) }
	preferred := func(profile string) func() engine.LayoutProvider {
		return func() engine.LayoutProvider {
			c := cfg.Layout
			c.PreferredProfile = profile
			return engine.NewConfiguredLayout(detect.ModeAuto, c)
		}
	}
	plain := func() engine.LayoutProvider { return engine.NewDefaultLayout(detect.ModeAuto) }
	// Concatenated geometries also exercise scale-cache eviction.
	duel, camp := hudSequence(t, 1920, 1080, "duel"), hudSequence(t, 1920, 1080, "camp")
	frames := append(append(append([]timedFrame(nil), duel...), camp...), hudSequence(t, 1280, 720, "duel")...)
	// Synthetic Xiayin rows are outside any HUD geometry: unidentified sides.
	for i, counts := range [][2]int{{4, 2}, {3, 0}} {
		img, _ := xiayinTestRows(i == 0, counts)
		frames = append(frames, timedFrame{img, frames[len(frames)-1].at.Add(time.Second)})
	}
	for _, tc := range []struct {
		name   string
		layout func() engine.LayoutProvider
		prefer string
		frames []timedFrame
	}{
		{"auto", configured, "", frames},
		// Preferred profiles skip profile parallelism; one geometry bounds runtime.
		{"engine-prefer-duel", configured, "duel", duel},
		{"engine-prefer-camp", configured, "camp", camp},
		{"manual-duel", preferred("duel"), "", duel},
		{"manual-camp", preferred("camp"), "", camp},
		{"default-layout", plain, "", frames},
	} {
		if raceEnabled {
			// The detector is ~25x slower; one geometry still covers every branch
			// (TestParallelSidesRace is the dedicated race workload).
			if tc.prefer != "" || tc.name != "auto" && tc.name != "default-layout" {
				continue
			}
			tc.frames = duel
		}
		t.Run(tc.name, func(t *testing.T) {
			engines := [2]*Engine{NewConfigured(tc.layout(), cfg.Vision), NewConfigured(tc.layout(), cfg.Vision)}
			if tc.prefer != "" {
				engines[0].Prefer(tc.prefer)
				engines[1].Prefer(tc.prefer)
			}
			named := 0
			for i, f := range tc.frames {
				want := analyzeSequence(engines[0], []timedFrame{f}, false)[0]
				got := analyzeSequence(engines[1], []timedFrame{f}, true)[0]
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("frame %d: parallel %+v\nsequential %+v", i, got, want)
				}
				if got.LeftNinja != "" && got.RightNinja != "" {
					named++
				}
			}
			if _, profiled := tc.layout().(engine.ProfiledLayout); profiled && named == 0 {
				t.Fatal("sequence never identified both sides; equivalence is vacuous")
			}
		})
	}
	// Direct side split on the shared Xiayin rows, independent of AnalyzeAt.
	img, positions := xiayinTestRows(true, [2]int{4, 2})
	area := detect.ContentArea{W: 960, H: 540}
	split := func(parallel bool) ([]detect.BeadPosition, [2]ninja.Readout) {
		defer func(old bool) { parallelSides = old }(parallelSides)
		parallelSides = parallel
		return NewConfigured(configured(), cfg.Vision).specialPositionsForAt("camp", img, positions, area, time.Unix(100, 0))
	}
	wantPos, wantID := split(false)
	gotPos, gotID := split(true)
	if !reflect.DeepEqual(gotPos, wantPos) || gotID != wantID {
		t.Fatalf("side split differs: %+v %+v vs %+v %+v", gotPos, gotID, wantPos, wantID)
	}
}

// Run under -race: both sides and both profiles populated across many frames.
func TestParallelSidesRace(t *testing.T) {
	cfg := config.Default()
	frames := hudSequence(t, 1920, 1080, "duel")
	for _, prefer := range []string{"", "duel"} {
		e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
		if prefer != "" {
			e.Prefer(prefer)
		}
		results := analyzeSequence(e, frames, true)
		if results[0].LeftNinja == "" || results[0].RightNinja == "" {
			t.Fatalf("both sides should be populated: %+v", results[0])
		}
	}
}

var benchSides = [2]hudSide{
	{title: "../../ninja/templates/madara.png", avatar: "../../../assets/avatars/90009.png", lit: 4},
	{title: "../../ninja/templates/naruto_right.png", avatar: "../../../assets/avatars/90010.png", lit: 2},
}

func benchmarkAnalyze(b *testing.B, prefer string) {
	cfg := config.Default()
	img := syntheticHUDFrame(b, 1920, 1080, "duel", benchSides, 1)
	e := NewConfigured(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg.Vision)
	if prefer != "" {
		e.Prefer(prefer)
	}
	at := time.Unix(1700000000, 0)
	r := e.AnalyzeAt(img, at)
	b.Logf("profile=%s names=%q/%q slots=%d/%d beads=%d", r.LayoutProfile, r.LeftNinja, r.RightNinja, r.LeftSlots, r.RightSlots, len(r.Beads))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at = at.Add(16 * time.Millisecond)
		e.AnalyzeAt(img, at)
	}
}

func BenchmarkAnalyzeDuel1080NoPreferred(b *testing.B) { benchmarkAnalyze(b, "") }
func BenchmarkAnalyzeDuel1080Preferred(b *testing.B)   { benchmarkAnalyze(b, "duel") }

// The cold variant uses a fresh engine per frame, so every frame performs the
// bounded full name and avatar scans rather than a tracker's cached hint.
func benchmarkAnalyzeCold(b *testing.B, prefer string) {
	cfg := config.Default()
	img := syntheticHUDFrame(b, 1920, 1080, "duel", benchSides, 1)
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	NewConfigured(layout, cfg.Vision).AnalyzeAt(img, time.Unix(1700000000, 0))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := NewConfigured(layout, cfg.Vision)
		if prefer != "" {
			e.Prefer(prefer)
		}
		e.AnalyzeAt(img, time.Unix(1700000000, 0))
	}
}

func BenchmarkAnalyzeDuel1080ColdNoPreferred(b *testing.B) { benchmarkAnalyzeCold(b, "") }
func BenchmarkAnalyzeDuel1080ColdPreferred(b *testing.B)   { benchmarkAnalyzeCold(b, "duel") }
