package match

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand/v2"
	"sync"
	"testing"
)

// Frozen scalar implementation is the oracle: the optimized path must not
// change mask interpretation, NCC scores, scan positions or tie-breaking.

func scalarNCCAt(img, templ, mask *image.Gray, ox, oy int) float64 {
	tb := templ.Bounds()
	ib := img.Bounds()
	var sumI, sumT, sumII, sumTT, sumIT, n float64
	for y := tb.Min.Y; y < tb.Max.Y; y++ {
		ty := y - tb.Min.Y
		iy := ib.Min.Y + oy + ty
		for x := tb.Min.X; x < tb.Max.X; x++ {
			tx := x - tb.Min.X
			if mask != nil {
				mb := mask.Bounds()
				mx, my := mb.Min.X+tx, mb.Min.Y+ty
				if mx < mb.Min.X || my < mb.Min.Y || mx >= mb.Max.X || my >= mb.Max.Y {
					continue
				}
				if mask.GrayAt(mx, my).Y == 0 {
					continue
				}
			}
			ix := ib.Min.X + ox + tx
			if ix < ib.Min.X || iy < ib.Min.Y || ix >= ib.Max.X || iy >= ib.Max.Y {
				continue
			}
			iv := float64(img.GrayAt(ix, iy).Y)
			tv := float64(templ.GrayAt(x, y).Y)
			sumI += iv
			sumT += tv
			sumII += iv * iv
			sumTT += tv * tv
			sumIT += iv * tv
			n++
		}
	}
	if n < 8 {
		return 0
	}
	num := sumIT - sumI*sumT/n
	denI := sumII - sumI*sumI/n
	denT := sumTT - sumT*sumT/n
	if denI <= 1e-6 || denT <= 1e-6 {
		// Equal mean brightness does not make a flat background match text.
		if denI > 1e-6 || denT > 1e-6 {
			return 0
		}
		// 纯色模板没有方差，NCC 无定义：均值接近视为命中。
		meanI := sumI / n
		meanT := sumT / n
		diff := meanI - meanT
		if diff < 0 {
			diff = -diff
		}
		if diff <= 6 {
			return 1
		}
		return 0
	}
	v := num / math.Sqrt(denI*denT)
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func scalarMatch(q Query) Score {
	gray := q.Gray
	if gray == nil {
		gray = ToGray(q.Image)
	}
	roi := q.ROI.Intersect(q.Image.Bounds())
	tw, th := q.Template.Bounds().Dx(), q.Template.Bounds().Dy()
	best := Score{Peak: roi.Min}
	if roi.Dx() < tw || roi.Dy() < th {
		return best
	}
	step := 1
	if min(tw, th) >= 48 {
		step = max(2, min(tw, th)/8)
	}
	scan := func(x0, y0, x1, y1, st int) {
		for y := max(roi.Min.Y, y0); y <= min(roi.Max.Y-th, y1); y += st {
			for x := max(roi.Min.X, x0); x <= min(roi.Max.X-tw, x1); x += st {
				v := scalarNCCAt(gray, q.Template, q.Mask, x-q.Image.Bounds().Min.X, y-q.Image.Bounds().Min.Y)
				if v > best.Value {
					best = Score{Value: v, Peak: image.Pt(x, y)}
				}
			}
		}
	}
	scan(roi.Min.X, roi.Min.Y, roi.Max.X-tw, roi.Max.Y-th, step)
	if step > 1 {
		scan(best.Peak.X-step, best.Peak.Y-step, best.Peak.X+step, best.Peak.Y+step, 1)
	}
	return best
}

func TestPreparedNCCMatchesScalarOracle(t *testing.T) {
	rng := rand.New(rand.NewPCG(20260906, 731))
	for i := range 60 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			w, h := 42+rng.IntN(30), 39+rng.IntN(24)
			tw, th := 8+rng.IntN(16), 8+rng.IntN(16)
			if i >= 56 {
				w, h, tw, th = 78, 79, 52, 51
			}
			source := image.NewRGBA(image.Rect(-30, 25, w+30, h+65))
			img := source.SubImage(image.Rect(-17, 41, w-17, h+41)).(*image.RGBA)
			for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
				for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
					v := uint8(rng.IntN(256))
					img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
				}
			}
			backing := image.NewGray(image.Rect(-20, -40, w+50, h+60))
			gray := backing.SubImage(image.Rect(-7, 3, w-7, h+3)).(*image.Gray)
			draw.Draw(gray, gray.Bounds(), ToGray(img), image.Point{}, draw.Src)
			tb := image.NewGray(image.Rect(-50, -50, tw+20, th+30))
			templ := tb.SubImage(image.Rect(-17, 6, tw-17, th+6)).(*image.Gray)
			mark := img.Bounds().Min.Add(image.Pt(3+rng.IntN(w-tw-6), 3+rng.IntN(h-th-6)))
			draw.Draw(templ, templ.Bounds(), img, mark, draw.Src)
			var mask *image.Gray
			switch i % 6 {
			case 1, 2, 3, 4, 5:
				mw, mh := tw, th
				if i%6 == 2 {
					mw, mh = tw-3, th-4
				}
				if i%6 == 3 {
					mw, mh = tw+3, th+4
				}
				mb := image.NewGray(image.Rect(-20, -20, mw+20, mh+20))
				mask = mb.SubImage(image.Rect(1, 2, mw+1, mh+2)).(*image.Gray)
				for y := mask.Rect.Min.Y; y < mask.Rect.Max.Y; y++ {
					for x := mask.Rect.Min.X; x < mask.Rect.Max.X; x++ {
						if i%6 != 4 && rng.IntN(4) != 0 {
							mask.SetGray(x, y, color.Gray{uint8(1 + rng.IntN(255))})
						}
					}
				}
			}
			q := Query{Image: img, Gray: gray, ROI: img.Bounds().Inset(i%4 - 2), Template: templ, Mask: mask}
			want := scalarMatch(q)
			for _, prepared := range []bool{false, true} {
				if prepared {
					q.Prepared = PrepareNCC(templ, mask)
				}
				got, err := (NCC{}).Match(q)
				if err != nil {
					t.Fatal(err)
				}
				if got.Peak != want.Peak || math.Abs(got.Value-want.Value) > 1e-12 {
					t.Fatalf("prepared=%v got %+v want %+v", prepared, got, want)
				}
			}
		})
	}
}

func TestPreparedNCCFlatAndSparseMasks(t *testing.T) {
	for _, mean := range []uint8{0, 64, 128, 255} {
		for _, delta := range []int{0, 5, 7} {
			img := image.NewRGBA(image.Rect(0, 0, 16, 16))
			fill(img, img.Bounds(), color.RGBA{mean, mean, mean, 255})
			templ := image.NewGray(image.Rect(0, 0, 8, 8))
			draw.Draw(templ, templ.Bounds(), image.NewUniform(color.Gray{uint8((int(mean) + delta) % 256)}), image.Point{}, draw.Src)
			for _, n := range []int{0, 7, 8, 64} {
				mask := image.NewGray(templ.Bounds())
				for i := range n {
					mask.Pix[i] = 255
				}
				q := Query{Image: img, Template: templ, Mask: mask, ROI: img.Bounds(), Prepared: PrepareNCC(templ, mask)}
				want := scalarMatch(q)
				got, err := (NCC{}).Match(q)
				if err != nil || got.Peak != want.Peak || got.Value != want.Value {
					t.Fatalf("mean=%d delta=%d count=%d got=%+v want=%+v err=%v", mean, delta, n, got, want, err)
				}
			}
		}
	}
}

func TestPreparedNCCIsOwnedImmutableAndShareable(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 60))
	paintTextured(img, img.Bounds())
	templ := ToGray(CropRGBA(img, image.Rect(13, 17, 31, 39)))
	mask := image.NewGray(templ.Bounds())
	draw.Draw(mask, mask.Bounds(), image.White, image.Point{}, draw.Src)
	plan := PrepareNCC(templ, mask)
	q := Query{Image: img, Gray: ToGray(img), Template: templ, Mask: mask, ROI: img.Bounds(), Prepared: plan}
	want, err := (NCC{}).Match(q)
	if err != nil {
		t.Fatal(err)
	}
	clear(templ.Pix)
	clear(mask.Pix)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got, err := (NCC{}).Match(q)
			if err != nil || got != want {
				t.Errorf("prepared template changed after input mutation: got=%+v want=%+v err=%v", got, want, err)
			}
		})
	}
	wg.Wait()
}
