package match

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// color.Gray is the pixel type; image.Gray is the image.

func TestNCCFindsCopiedBlock(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 60))
	fill(img, img.Bounds(), color.RGBA{R: 220, G: 220, B: 220, A: 255})
	mark := image.Rect(20, 15, 44, 39)
	paintTextured(img, mark)
	templ := ToGray(CropRGBA(img, mark))

	score, err := NCC{}.Match(Query{
		Image:    img,
		ROI:      img.Bounds(),
		Template: templ,
	})
	if err != nil {
		t.Fatal(err)
	}
	if score.Value < 0.95 {
		t.Fatalf("self match should be high, got %.3f at %v", score.Value, score.Peak)
	}
	if dx := score.Peak.X - mark.Min.X; dx < -2 || dx > 2 {
		t.Fatalf("peak x = %d, want ~%d", score.Peak.X, mark.Min.X)
	}
}

func TestNCCRejectsUnrelatedRegion(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 60))
	fill(img, img.Bounds(), color.RGBA{R: 200, G: 200, B: 200, A: 255})
	fill(img, image.Rect(8, 8, 28, 28), color.RGBA{R: 10, G: 10, B: 10, A: 255})
	templ := image.NewGray(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if (x+y)%2 == 0 {
				templ.SetGray(x, y, color.Gray{Y: 250})
			}
		}
	}
	score, err := NCC{}.Match(Query{
		Image:    img,
		ROI:      image.Rect(40, 30, 80, 60),
		Template: templ,
	})
	if err != nil {
		t.Fatal(err)
	}
	if score.Value > 0.55 {
		t.Fatalf("unrelated checkerboard should be low, got %.3f", score.Value)
	}
}

func TestNCCRejectsFlatRegionWithSameMeanAsText(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(img, img.Bounds(), color.RGBA{128, 128, 128, 255})
	templ := image.NewGray(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			v := uint8(64)
			if (x+y)%2 == 0 {
				v = 192
			}
			templ.SetGray(x, y, color.Gray{Y: v})
		}
	}
	score, err := (NCC{}).Match(Query{Image: img, ROI: img.Bounds(), Template: templ})
	if err != nil || score.Value != 0 {
		t.Fatalf("flat background cannot establish text: %+v %v", score, err)
	}
}

func TestNCCMaskIgnoresVariablePixels(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 24))
	fill(img, img.Bounds(), color.RGBA{R: 30, G: 30, B: 30, A: 255})
	paintTextured(img, image.Rect(4, 4, 36, 20))
	fill(img, image.Rect(12, 8, 28, 16), color.RGBA{R: 240, G: 20, B: 20, A: 255})

	templImg := CropRGBA(img, image.Rect(4, 4, 36, 20))
	fill(templImg, image.Rect(8, 4, 24, 12), color.RGBA{R: 10, G: 10, B: 200, A: 255})
	templ := ToGray(templImg)

	mask := image.NewGray(templ.Bounds())
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			v := uint8(255)
			if x >= 8 && x < 24 && y >= 4 && y < 12 {
				v = 0
			}
			mask.SetGray(x, y, color.Gray{Y: v})
		}
	}

	score, err := NCC{}.Match(Query{
		Image:    img,
		ROI:      image.Rect(0, 0, 40, 24),
		Template: templ,
		Mask:     mask,
	})
	if err != nil {
		t.Fatal(err)
	}
	if score.Value < 0.90 {
		t.Fatalf("masked variable core should still match frame, got %.3f", score.Value)
	}
}

func TestScaleGrayKeepsConstant(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.SetGray(x, y, color.Gray{Y: 80})
		}
	}
	got := ScaleGray(src, 10, 6)
	if got.Bounds().Dx() != 10 || got.Bounds().Dy() != 6 {
		t.Fatalf("size = %v", got.Bounds())
	}
	if got.GrayAt(3, 2).Y != 80 {
		t.Fatalf("constant scale changed value: %d", got.GrayAt(3, 2).Y)
	}
}

func TestNCCSharedGrayMatchesFallback(t *testing.T) {
	for _, origin := range []image.Point{image.Pt(0, 0), image.Pt(31, -17)} {
		t.Run(origin.String(), func(t *testing.T) {
			img := image.NewRGBA(image.Rectangle{Min: origin, Max: origin.Add(image.Pt(48, 36))})
			fill(img, img.Bounds(), color.RGBA{R: 250, G: 250, B: 250, A: 255})
			mark := image.Rect(15, 12, 27, 24).Add(origin)
			paintTextured(img, mark)
			templ := ToGray(CropRGBA(img, mark))
			mask := image.NewGray(image.Rect(7, 9, 19, 21))
			for y := mask.Rect.Min.Y; y < mask.Rect.Max.Y; y++ {
				for x := mask.Rect.Min.X; x < mask.Rect.Max.X; x++ {
					if (x+y)%3 != 0 {
						mask.SetGray(x, y, color.Gray{Y: 255})
					}
				}
			}
			gray := ToGray(img)
			// A shared frame can be a view with a nonzero origin and padded stride.
			backing := image.NewGray(image.Rect(-10, -10, 90, 80))
			view := backing.SubImage(image.Rect(9, 11, 57, 47)).(*image.Gray)
			draw.Draw(view, view.Bounds(), gray, gray.Bounds().Min, draw.Src)
			for _, m := range []*image.Gray{nil, mask} {
				q := Query{Image: img, ROI: img.Bounds().Inset(2), Template: templ, Mask: m}
				want, err := (NCC{}).Match(q)
				if err != nil {
					t.Fatal(err)
				}
				if want.Value < 0.999 || want.Peak != mark.Min {
					t.Fatalf("fallback did not locate copied block: %+v, want %v", want, mark.Min)
				}
				for _, shared := range []*image.Gray{gray, view} {
					q.Gray = shared
					got, err := (NCC{}).Match(q)
					if err != nil {
						t.Fatal(err)
					}
					if got != want {
						t.Fatalf("shared gray %v, mask=%t: got %+v, want %+v", shared.Bounds(), m != nil, got, want)
					}
				}
			}
		})
	}
}

func TestNCCRejectsGraySizeMismatch(t *testing.T) {
	_, err := (NCC{}).Match(Query{
		Image:    image.NewRGBA(image.Rect(0, 0, 40, 30)),
		Gray:     image.NewGray(image.Rect(0, 0, 39, 30)),
		ROI:      image.Rect(0, 0, 40, 30),
		Template: image.NewGray(image.Rect(0, 0, 4, 4)),
	})
	if err == nil {
		t.Fatal("expected an error for a gray frame of the wrong size")
	}
}

func TestToGrayRGBAEqualsGenericWithOriginAndStride(t *testing.T) {
	backing := image.NewRGBA(image.Rect(-20, -30, 90, 80))
	for y := backing.Rect.Min.Y; y < backing.Rect.Max.Y; y++ {
		for x := backing.Rect.Min.X; x < backing.Rect.Max.X; x++ {
			backing.SetRGBA(x, y, color.RGBA{R: uint8(x*13 + y), G: uint8(y*17 - x), B: uint8(x * y), A: uint8(x + y)})
		}
	}
	for _, img := range []*image.RGBA{backing, backing.SubImage(image.Rect(7, -9, 43, 52)).(*image.RGBA)} {
		// Wrapping hides the RGBA type and exercises the generic conversion.
		want := ToGray(struct{ image.Image }{img})
		got := ToGray(img)
		if got.Bounds() != want.Bounds() {
			t.Fatalf("bounds: got %v, want %v", got.Bounds(), want.Bounds())
		}
		for i, v := range got.Pix {
			if v != want.Pix[i] {
				t.Fatalf("pixel %d: got %d, want %d", i, v, want.Pix[i])
			}
		}
	}
}

// Model four small scene templates against the same 720p frame. Both modes
// include grayscale conversion per frame; shared mode performs it only once.
func BenchmarkNCCFrameGrayReuse(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	paintTextured(img, img.Bounds())
	queries := make([]Query, 4)
	for i := range queries {
		mark := image.Rect(100+i*200, 200, 116+i*200, 212)
		queries[i] = Query{Image: img, ROI: mark.Inset(-4), Template: ToGray(CropRGBA(img, mark))}
	}
	for _, shared := range []bool{false, true} {
		name := "per_template"
		if shared {
			name = "shared_frame"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var gray *image.Gray
				if shared {
					gray = ToGray(img)
				}
				for _, q := range queries {
					q.Gray = gray
					if _, err := (NCC{}).Match(q); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func paintTextured(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			v := uint8(40 + (x*13+y*7)%160)
			img.SetRGBA(x, y, color.RGBA{R: v / 2, G: v, B: 220 - v/3, A: 255})
		}
	}
}
