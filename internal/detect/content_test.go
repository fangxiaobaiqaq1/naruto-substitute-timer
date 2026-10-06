package detect

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestResolveContentAreaRequiresPixelEvidenceForBars(t *testing.T) {
	for _, tc := range []struct {
		name            string
		bounds, content image.Rectangle
	}{
		{"horizontal-bars", image.Rect(0, 0, 1024, 768), image.Rect(0, 96, 1024, 672)},
		{"vertical-bars", image.Rect(0, 0, 2560, 1080), image.Rect(320, 0, 2240, 1080)},
		{"nonzero-origin", image.Rect(50, 70, 1074, 838), image.Rect(50, 166, 1074, 742)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(tc.bounds)
			draw.Draw(img, tc.content, image.NewUniform(color.RGBA{50, 100, 150, 255}), image.Point{}, draw.Src)
			got, ok := ResolveContentArea(img, ModeAuto, 1920, 1080, .015)
			want := ContentArea{tc.content.Min.X, tc.content.Min.Y, tc.content.Dx(), tc.content.Dy()}
			if !ok || got != want {
				t.Fatalf("pixel bars: got %+v, %v; want %+v", got, ok, want)
			}
			// The same dimensions with game pixels all the way to the edges
			// are a native full-screen HUD, not evidence of centered bars.
			draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{50, 100, 150, 255}), image.Point{}, draw.Src)
			if got, ok := ResolveContentArea(img, ModeAuto, 1920, 1080, .015); !ok || got != (ContentArea{tc.bounds.Min.X, tc.bounds.Min.Y, tc.bounds.Dx(), tc.bounds.Dy()}) {
				t.Fatalf("native full surface rejected: %+v, %v", got, ok)
			}
		})
	}
}

func TestResolveContentAreaRespectsReferenceAndTolerance(t *testing.T) {
	img := image.NewRGBA(image.Rect(20, 30, 1620, 930))
	want := ContentArea{20, 30, 1600, 900}
	if got, ok := ResolveContentArea(img, ModeAuto, 1600, 900, .015); !ok || got != want {
		t.Fatalf("same aspect: %+v %v", got, ok)
	}
	// A reference of 4:3 is accepted only when explicitly calibrated that way.
	other := image.NewRGBA(image.Rect(0, 0, 1024, 768))
	draw.Draw(other, other.Bounds(), image.NewUniform(color.RGBA{50, 100, 150, 255}), image.Point{}, draw.Src)
	if got, ok := ResolveContentArea(other, ModeAuto, 1024, 768, .015); !ok || got.W != 1024 || got.H != 768 {
		t.Fatalf("reference ignored: %+v %v", got, ok)
	}
	near := image.NewRGBA(image.Rect(0, 0, 1600, 910))
	draw.Draw(near, near.Bounds(), image.NewUniform(color.RGBA{50, 100, 150, 255}), image.Point{}, draw.Src)
	if _, ok := ResolveContentArea(near, ModeAuto, 1920, 1080, .015); !ok {
		t.Fatal("configured tolerance ignored")
	}
	if got, ok := ResolveContentArea(near, ModeAuto, 1920, 1080, .001); !ok || got != (ContentArea{0, 0, 1600, 910}) {
		t.Fatalf("native surface should not depend on aspect tolerance: %+v, %v", got, ok)
	}
	if _, ok := ResolveContentArea(nil, ModeAuto, 1920, 1080, .015); ok {
		t.Fatal("nil accepted")
	}
}

func TestResolveContentAreaDoesNotTreatOneToolbarAsCenteredBars(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1000))
	draw.Draw(img, image.Rect(0, 50, 1600, 1000), image.NewUniform(color.RGBA{50, 100, 150, 255}), image.Point{}, draw.Src)
	if got, ok := ResolveContentArea(img, ModeAuto, 1920, 1080, .015); !ok || got != (ContentArea{0, 0, 1600, 1000}) {
		t.Fatalf("a top toolbar must keep the native surface: %+v, %v", got, ok)
	}
	if got, ok := ResolveContentArea(img, ModeStretch, 1920, 1080, .015); !ok || got.H != 1000 {
		t.Fatal("explicit stretch ignored")
	}
	if got, ok := ResolveContentArea(img, ModeLetterbox, 1920, 1080, .015); !ok || got.Y != 50 || got.H != 900 {
		t.Fatal("explicit letterbox ignored")
	}
}

func TestClassifyScreenAcceptsNarrowBilateralHUDRows(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1024, 576))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{31, 42, 55, 255}), image.Point{}, draw.Src)
	for _, p := range []image.Point{
		{188, 155}, {218, 155}, {248, 155}, {278, 155},
		{1674, 155}, {1644, 155}, {1614, 155}, {1584, 155},
	} {
		center := ComputeContentArea(1024, 576, ModeStretch).MapHUDPixel(float64(p.X), float64(p.Y), map[bool]string{true: "right", false: "left"}[p.X > 1000], LogicWidth, LogicHeight)
		draw.Draw(img, image.Rect(center.X-4, center.Y-4, center.X+4, center.Y+4), image.NewUniform(color.RGBA{20, 70, 140, 255}), image.Point{}, draw.Src)
	}
	if got := ClassifyScreen(img, ModeStretch); got != ScreenFighting {
		t.Fatalf("narrow bilateral HUD row classified as %v", got)
	}
}

func TestClassifyScreenUsesPixelBackedLetterboxTransform(t *testing.T) {
	bounds := image.Rect(0, 0, 1024, 768)
	content := image.Rect(0, 96, 1024, 672)
	img := image.NewRGBA(bounds)
	draw.Draw(img, content, image.NewUniform(color.RGBA{50, 100, 150, 255}), image.Point{}, draw.Src)
	// Put game-like bead colors at the normalized HUD positions inside the
	// centered 16:9 content. The old size-only transform sampled the wrong y.
	ca, ok := ResolveContentArea(img, ModeAuto, LogicWidth, LogicHeight, .015)
	if !ok {
		t.Fatal("content area not resolved")
	}
	for _, r := range [][4]float64{
		{.08, .07, .26, .06},
		{.80, .07, .16, .06},
	} {
		x0, y0 := ca.Map(r[0]*LogicWidth, r[1]*LogicHeight)
		x1, y1 := ca.Map((r[0]+r[2])*LogicWidth, (r[1]+r[3])*LogicHeight)
		draw.Draw(img, image.Rect(x0, y0, x1, y1), image.NewUniform(color.RGBA{30, 170, 220, 255}), image.Point{}, draw.Src)
	}
	if got := ClassifyScreen(img, ModeAuto); got != ScreenFighting {
		t.Fatalf("letterbox game HUD classified as %v", got)
	}
}
