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
			// are not evidence that the old HUD calibration still applies.
			draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{50, 100, 150, 255}), image.Point{}, draw.Src)
			if _, ok := ResolveContentArea(img, ModeAuto, 1920, 1080, .015); ok {
				t.Fatal("native different aspect ratio accepted as letterbox")
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
	if _, ok := ResolveContentArea(near, ModeAuto, 1920, 1080, .001); ok {
		t.Fatal("strict configured tolerance ignored")
	}
	if _, ok := ResolveContentArea(nil, ModeAuto, 1920, 1080, .015); ok {
		t.Fatal("nil accepted")
	}
}

func TestResolveContentAreaDoesNotTreatOneToolbarAsCenteredBars(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1000))
	draw.Draw(img, image.Rect(0, 50, 1600, 1000), image.NewUniform(color.RGBA{50, 100, 150, 255}), image.Point{}, draw.Src)
	if _, ok := ResolveContentArea(img, ModeAuto, 1920, 1080, .015); ok {
		t.Fatal("a top toolbar is not centered letterboxing")
	}
	if got, ok := ResolveContentArea(img, ModeStretch, 1920, 1080, .015); !ok || got.H != 1000 {
		t.Fatal("explicit stretch ignored")
	}
	if got, ok := ResolveContentArea(img, ModeLetterbox, 1920, 1080, .015); !ok || got.Y != 50 || got.H != 900 {
		t.Fatal("explicit letterbox ignored")
	}
}
