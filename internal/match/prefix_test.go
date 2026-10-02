package match

import (
	"image"
	"math"
	"math/rand/v2"
	"testing"
)

// referencePreparedAt is a verbatim copy of PreparedNCC.at before prefix sums:
// per-pixel int64 accumulation of sum, sum² and the dot product.
func referencePreparedAt(p *PreparedNCC, img *image.Gray, ox, oy int) float64 {
	if p.count < 8 {
		return 0
	}
	var sum, squares, product int64
	for _, run := range p.runs {
		off := (oy+run.y)*img.Stride + ox + run.x
		row := img.Pix[off : off+len(run.pixels)]
		for x, t := range run.pixels {
			v := int64(row[x])
			sum += v
			squares += v * v
			product += v * int64(t)
		}
	}
	sumI := float64(sum)
	variance := float64(squares) - sumI*sumI/p.count
	if variance <= 1e-6 || p.variance <= 1e-6 {
		if variance > 1e-6 || p.variance > 1e-6 {
			return 0
		}
		if math.Abs(sumI/p.count-p.sum/p.count) <= 6 {
			return 1
		}
		return 0
	}
	score := (float64(product) - sumI*p.sum/p.count) / math.Sqrt(variance*p.variance)
	return min(1, max(0, score))
}

func randomGray(r *rand.Rand, w, h int) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, w, h))
	for i := range g.Pix {
		g.Pix[i] = uint8(r.IntN(256))
	}
	return g
}

// randomMask produces irregular runs, including isolated 1-pixel runs.
func randomMask(r *rand.Rand, w, h int, density int) *image.Gray {
	m := image.NewGray(image.Rect(0, 0, w, h))
	for i := range m.Pix {
		if r.IntN(100) < density {
			m.Pix[i] = uint8(1 + r.IntN(255))
		}
	}
	return m
}

func checkPreparedAtExact(t *testing.T, img, templ, mask *image.Gray, label string) {
	t.Helper()
	p := PrepareNCC(templ, mask)
	if p == nil {
		t.Fatalf("%s: nil prepared", label)
	}
	ib, tb := img.Bounds(), templ.Bounds()
	pre := newGrayPrefix(img, 0, 0, ib.Dx(), ib.Dy())
	defer pre.release()
	for oy := 0; oy+tb.Dy() <= ib.Dy(); oy++ {
		for ox := 0; ox+tb.Dx() <= ib.Dx(); ox++ {
			want := referencePreparedAt(p, img, ox, oy)
			if got := p.at(img, pre, ox, oy); got != want {
				t.Fatalf("%s at (%d,%d): got %v want %v", label, ox, oy, got, want)
			}
		}
	}
}

func TestPreparedAtBitIdenticalToReference(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	for _, size := range []struct{ iw, ih, tw, th int }{{20, 14, 9, 7}, {40, 30, 17, 13}, {64, 50, 33, 29}, {90, 70, 61, 52}, {16, 16, 16, 16}} {
		for _, density := range []int{100, 85, 50, 15} {
			img := randomGray(r, size.iw, size.ih)
			templ := randomGray(r, size.tw, size.th)
			mask := randomMask(r, size.tw, size.th, density)
			checkPreparedAtExact(t, img, templ, mask, "random")
			checkPreparedAtExact(t, img, templ, nil, "nomask")
			// Mask smaller than template and image with padded stride/origin.
			small := randomMask(r, size.tw-2, size.th-1, density)
			sub := randomGray(r, size.iw+5, size.ih+3).SubImage(image.Rect(3, 2, size.iw+3, size.ih+2)).(*image.Gray)
			checkPreparedAtExact(t, sub, templ, small, "subimage")
		}
	}
}

func TestPreparedAtEdgeCases(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 5))
	flat := image.NewGray(image.Rect(0, 0, 30, 20))
	for i := range flat.Pix {
		flat.Pix[i] = 117
	}
	flatT := image.NewGray(image.Rect(0, 0, 10, 8))
	for i := range flatT.Pix {
		flatT.Pix[i] = 120
	}
	checkPreparedAtExact(t, flat, flatT, nil, "constant both")
	checkPreparedAtExact(t, flat, randomGray(r, 10, 8), nil, "constant image")
	checkPreparedAtExact(t, randomGray(r, 30, 20), flatT, nil, "constant template")
	// Only 1-pixel runs (checkerboard) and a single 1-pixel run per row.
	checker := image.NewGray(image.Rect(0, 0, 12, 12))
	single := image.NewGray(image.Rect(0, 0, 12, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 12; x++ {
			if (x+y)%2 == 0 {
				checker.Pix[y*12+x] = 255
			}
		}
		single.Pix[y*12+(y*5)%12] = 255
	}
	img := randomGray(r, 40, 33)
	templ := randomGray(r, 12, 12)
	checkPreparedAtExact(t, img, templ, checker, "checker")
	checkPreparedAtExact(t, img, templ, single, "single")
	// Saturated pixels exercise the largest products.
	white := image.NewGray(image.Rect(0, 0, 70, 9))
	for i := range white.Pix {
		white.Pix[i] = 255
	}
	white.Pix[3] = 0
	checkPreparedAtExact(t, white, white.SubImage(image.Rect(0, 0, 67, 9)).(*image.Gray), nil, "saturated")
}

func TestCropThenGrayEqualsGrayThenCrop(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	img := image.NewRGBA(image.Rect(5, 7, 105, 87))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.IntN(256))
	}
	view := img.SubImage(image.Rect(10, 12, 90, 80)).(*image.RGBA)
	gray := ToGray(view)
	for _, crop := range []image.Rectangle{image.Rect(15, 20, 60, 70), image.Rect(0, 0, 200, 200), image.Rect(85, 75, 120, 120), image.Rect(200, 200, 210, 210)} {
		want := ToGray(CropRGBA(view, crop))
		got := gray.SubImage(crop.Intersect(view.Bounds()).Sub(view.Bounds().Min)).(*image.Gray)
		if got.Bounds().Size() != want.Bounds().Size() {
			t.Fatalf("%v: size %v want %v", crop, got.Bounds().Size(), want.Bounds().Size())
		}
		for y := 0; y < want.Bounds().Dy(); y++ {
			for x := 0; x < want.Bounds().Dx(); x++ {
				gb, wb := got.Bounds().Min, want.Bounds().Min
				if g, w := got.GrayAt(gb.X+x, gb.Y+y), want.GrayAt(wb.X+x, wb.Y+y); g != w {
					t.Fatalf("%v (%d,%d): %v want %v", crop, x, y, g, w)
				}
			}
		}
		if g, w := ScaleGray(got, 36, 36), ScaleGray(want, 36, 36); string(g.Pix) != string(w.Pix) {
			t.Fatalf("%v: scaled crop differs", crop)
		}
	}
}

func BenchmarkPreparedNCCAt(b *testing.B) {
	r := rand.New(rand.NewPCG(9, 9))
	img := randomGray(r, 120, 140)
	// A diamond mask like the avatar face mask: one long run per row.
	diamond := image.NewGray(image.Rect(0, 0, 51, 51))
	for y := 0; y < 51; y++ {
		for x := 0; x < 51; x++ {
			if math.Abs(float64(x)-25)+math.Abs(float64(y)-25) <= 22 {
				diamond.Pix[y*51+x] = 255
			}
		}
	}
	p := PrepareNCC(randomGray(r, 51, 51), diamond)
	pre := newGrayPrefix(img, 0, 0, 120, 140)
	defer pre.release()
	b.Run("reference", func(b *testing.B) {
		for b.Loop() {
			for oy := 0; oy < 80; oy += 3 {
				for ox := 0; ox < 60; ox += 3 {
					referencePreparedAt(p, img, ox, oy)
				}
			}
		}
	})
	b.Run("prefix", func(b *testing.B) {
		for b.Loop() {
			for oy := 0; oy < 80; oy += 3 {
				for ox := 0; ox < 60; ox += 3 {
					p.at(img, pre, ox, oy)
				}
			}
		}
	})
}

func BenchmarkNCCMatch(b *testing.B) {
	r := rand.New(rand.NewPCG(4, 4))
	img := image.NewRGBA(image.Rect(0, 0, 70, 100))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.IntN(256))
	}
	gray := ToGray(img)
	p := PrepareNCC(randomGray(r, 51, 51), nil)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := (NCC{}).Match(Query{Image: img, Gray: gray, ROI: img.Bounds(), Prepared: p}); err != nil {
			b.Fatal(err)
		}
	}
}
