package match

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// The SIMD kernels must return exactly the integers of nccSpansGo for every
// span length, alignment, buffer end (vector vs byte tails), stride and value
// range (32-bit lane widening). AVX2 runs only where the CPU has it; use
// `go test -c` + `qemu-x86_64 -cpu Haswell` to cover it on older machines.

func kernelNames(t testing.TB) []string {
	kernels := testKernels()
	if len(kernels) == 0 {
		t.Skip("no SIMD kernel in this build")
	}
	names := make([]string, 0, len(kernels))
	for name := range kernels {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func checkKernels(t *testing.T, label string, pix []byte, stride, base int, p *PreparedNCC) {
	t.Helper()
	if len(p.spans) == 0 {
		return
	}
	wantDot, wantSum, wantSq := nccSpansGo(pix[base:], stride, p.spans, p.pix)
	for name, kernel := range testKernels() {
		dot, sum, sq := kernel(&pix[base], stride, len(pix)-base, &p.spans[0], len(p.spans), &p.pix[0])
		if dot != wantDot || sum != wantSum || sq != wantSq {
			t.Fatalf("%s %s base=%d stride=%d: got dot=%d sum=%d sq=%d want %d %d %d", name, label, base, stride, dot, sum, sq, wantDot, wantSum, wantSq)
		}
	}
}

func TestKernelsRandomTemplatesAndMasks(t *testing.T) {
	t.Logf("kernels %v; Match uses %s", kernelNames(t), KernelName())
	r := rand.New(rand.NewPCG(20261021, 7))
	for iter := range 400 {
		tw, th := 1+r.IntN(72), 1+r.IntN(10)
		iw, ih := tw+r.IntN(40), th+r.IntN(5)
		stride := iw
		if r.IntN(2) == 0 {
			stride += r.IntN(24)
		}
		// Exactly sized buffer: windows near its end take the byte tail.
		pix := make([]byte, (ih-1)*stride+iw)
		saturated := iter%7 == 0
		for i := range pix {
			pix[i] = uint8(r.IntN(256))
			if saturated {
				pix[i] = 255
			}
		}
		templ := randomGray(r, tw, th)
		if saturated {
			for i := range templ.Pix {
				templ.Pix[i] = 255
			}
		}
		var mask *image.Gray
		switch iter % 4 {
		case 1:
			mask = randomMask(r, tw, th, 10+r.IntN(90))
		case 2:
			mask = randomMask(r, max(1, tw-r.IntN(4)), max(1, th-r.IntN(3)), 70)
		case 3:
			mask = diamondMask(tw, th)
		}
		p := PrepareNCC(templ, mask)
		for oy := 0; oy+th <= ih; oy++ {
			for ox := 0; ox+tw <= iw; ox++ {
				checkKernels(t, fmt.Sprintf("iter %d %dx%d", iter, tw, th), pix, stride, oy*stride+ox, p)
			}
		}
	}
}

// Every span length 1..100 at every offset of an exact buffer: covers full
// blocks, masked vector tails and byte tails at the very end of the buffer.
func TestKernelsSpanLengthsAndBufferEnd(t *testing.T) {
	kernelNames(t)
	r := rand.New(rand.NewPCG(5, 8))
	for n := 1; n <= 100; n++ {
		templ := randomGray(r, n, 1)
		p := PrepareNCC(templ, nil)
		for _, width := range []int{n, n + 1, n + 15, n + 16, n + 17, n + 40} {
			pix := make([]byte, width)
			for i := range pix {
				pix[i] = uint8(r.IntN(256))
			}
			for base := 0; base+n <= width; base++ {
				checkKernels(t, fmt.Sprintf("n=%d width=%d", n, width), pix, width, base, p)
			}
		}
	}
}

// Saturated pixels over very long and very many spans force the 32-bit lanes
// to be widened repeatedly; any overflow would change the sums.
func TestKernelsLaneWideningSaturated(t *testing.T) {
	kernelNames(t)
	// Rows longer than maxDotRun are split into several spans.
	long := image.NewGray(image.Rect(0, 0, maxDotRun+4000, 5))
	for i := range long.Pix {
		long.Pix[i] = 255
	}
	pix := make([]byte, len(long.Pix)+3*long.Stride)
	for i := range pix {
		pix[i] = 255
	}
	p := PrepareNCC(long, nil)
	if len(p.spans) != 10 {
		t.Fatalf("spans=%d", len(p.spans))
	}
	checkKernels(t, "long rows", pix, long.Stride, 0, p)
	checkKernels(t, "long rows offset", pix, long.Stride, 3*long.Stride, p)
	dot, _, _ := nccSpansGo(pix, long.Stride, p.spans, p.pix)
	if want := uint64(255*255) * uint64(len(long.Pix)); dot != want {
		t.Fatalf("oracle dot=%d want %d", dot, want)
	}
	// Many short spans (one block each) between widenings.
	many := image.NewGray(image.Rect(0, 0, 4096, 40))
	mask := image.NewGray(many.Bounds())
	for i := range many.Pix {
		many.Pix[i] = 255
		if i%16 != 15 {
			mask.Pix[i] = 1
		}
	}
	p = PrepareNCC(many, mask)
	img := make([]byte, len(many.Pix))
	for i := range img {
		img[i] = 255
	}
	checkKernels(t, "many spans", img, many.Stride, 0, p)
	p = PrepareNCC(many, nil)
	checkKernels(t, "wide rows", img, many.Stride, 0, p)
}

func diamondMask(w, h int) *image.Gray {
	m := image.NewGray(image.Rect(0, 0, w, h))
	cx, cy := float64(w-1)/2, float64(h-1)/2
	radius := float64(min(w, h)) * .43
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if math.Abs(float64(x)-cx)+math.Abs(float64(y)-cy) <= radius {
				m.Pix[y*w+x] = 255
			}
		}
	}
	return m
}

// Match with each SIMD kernel must equal Match on the portable prefix path:
// same values bit for bit, same peaks.
func TestMatchKernelEqualsPortable(t *testing.T) {
	names := kernelNames(t)
	saved := nccKernel
	defer func() { nccKernel = saved }()
	rng := rand.New(rand.NewPCG(99, 1))
	for i := range 120 {
		w, h := 40+rng.IntN(60), 36+rng.IntN(50)
		tw, th := 6+rng.IntN(min(w, h)-6), 6+rng.IntN(min(w, h)-6)
		source := image.NewRGBA(image.Rect(-9, 4, w+11, h+20))
		img := source.SubImage(image.Rect(-5, 7, w-5, h+7)).(*image.RGBA)
		for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
			for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
				img.SetRGBA(x, y, color.RGBA{uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)), 255})
			}
		}
		gray := ToGray(img)
		// A slightly altered crop of the image, so real peaks exist.
		mx, my := rng.IntN(w-tw+1), rng.IntN(h-th+1)
		templ := image.NewGray(image.Rect(0, 0, tw, th))
		for y := range th {
			copy(templ.Pix[y*tw:(y+1)*tw], gray.Pix[(my+y)*gray.Stride+mx:])
		}
		for range tw * th / 8 {
			templ.Pix[rng.IntN(len(templ.Pix))] = uint8(rng.IntN(256))
		}
		var mask *image.Gray
		switch i % 3 {
		case 1:
			mask = randomMask(rng, tw, th, 30+rng.IntN(70))
		case 2:
			mask = diamondMask(tw, th)
		}
		prepared := PrepareNCC(templ, mask)
		for _, roi := range []image.Rectangle{img.Bounds(), img.Bounds().Inset(2), image.Rect(-20, 0, w/2+tw, h+40)} {
			q := Query{Image: img, Gray: gray, ROI: roi, Prepared: prepared}
			nccKernel = nil
			want, err := (NCC{}).Match(q)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range names {
				nccKernel = testKernels()[name]
				got, err := (NCC{}).Match(q)
				if err != nil {
					t.Fatal(err)
				}
				if got != want || math.Float64bits(got.Value) != math.Float64bits(want.Value) {
					t.Fatalf("case %d %s roi=%v: got %+v want %+v", i, name, roi, got, want)
				}
			}
		}
	}
}

func TestKernelFitsRejectsOutsidePixels(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 10, 4))
	if !kernelFits(g, image.Rect(0, 0, 10, 4)) || !kernelFits(g, image.Rect(3, 1, 7, 3)) {
		t.Fatal("inside ROI rejected")
	}
	short := &image.Gray{Pix: g.Pix[:39], Stride: 10, Rect: g.Rect}
	if kernelFits(short, image.Rect(0, 0, 10, 4)) || kernelFits(g, image.Rect(-1, 0, 5, 2)) || kernelFits(g, image.Rect(0, 0, 11, 2)) || kernelFits(g, image.Rectangle{}) {
		t.Fatal("outside ROI accepted")
	}
}

// BenchmarkKernels scans an avatar-sized diamond template (the dominant live
// workload) and a wide unmasked title strip over every position of a ROI.
func BenchmarkKernels(b *testing.B) {
	r := rand.New(rand.NewPCG(9, 9))
	for _, tc := range []struct {
		name         string
		tw, th, w, h int
		mask         bool
	}{{"avatar102", 102, 102, 140, 200, true}, {"title300x40", 300, 40, 340, 60, false}} {
		img := randomGray(r, tc.w, tc.h)
		var mask *image.Gray
		if tc.mask {
			mask = diamondMask(tc.tw, tc.th)
		}
		p := PrepareNCC(randomGray(r, tc.tw, tc.th), mask)
		positions := (tc.w - tc.tw + 1) * (tc.h - tc.th + 1)
		b.Run(tc.name+"/go-prefix", func(b *testing.B) {
			for b.Loop() {
				pre := newGrayPrefix(img, 0, 0, tc.w, tc.h)
				for oy := 0; oy+tc.th <= tc.h; oy++ {
					for ox := 0; ox+tc.tw <= tc.w; ox++ {
						p.at(img, pre, ox, oy)
					}
				}
				pre.release()
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*positions), "ns/position")
		})
		for name, kernel := range testKernels() {
			b.Run(tc.name+"/"+name, func(b *testing.B) {
				for b.Loop() {
					for oy := 0; oy+tc.th <= tc.h; oy++ {
						for ox := 0; ox+tc.tw <= tc.w; ox++ {
							base := oy*img.Stride + ox
							dot, sum, sq := kernel(&img.Pix[base], img.Stride, len(img.Pix)-base, &p.spans[0], len(p.spans), &p.pix[0])
							p.score(dot, sum, sq)
						}
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*positions), "ns/position")
			})
		}
	}
}

// matchNoSkip is Match's coarse/mid/fine search without skipping grid points
// that an earlier pass already scored (the pre-optimisation loop).
func matchNoSkip(gray *image.Gray, roi image.Rectangle, p *PreparedNCC) Score {
	tw, th := p.rect.Dx(), p.rect.Dy()
	pre := newGrayPrefix(gray, roi.Min.X, roi.Min.Y, roi.Dx(), roi.Dy())
	defer pre.release()
	best := Score{Peak: roi.Min}
	scan := func(x0, y0, x1, y1, st int) {
		for y := max(y0, roi.Min.Y); y <= min(y1, roi.Max.Y-th); y += st {
			for x := max(x0, roi.Min.X); x <= min(x1, roi.Max.X-tw); x += st {
				if v := p.at(gray, pre, x, y); v > best.Value {
					best = Score{Value: v, Peak: image.Pt(x, y)}
				}
			}
		}
	}
	step := coarseStep(tw, th)
	scan(roi.Min.X, roi.Min.Y, roi.Max.X-tw, roi.Max.Y-th, step)
	if step > 1 {
		if mid := refineStep(step); mid > 1 {
			scan(best.Peak.X-step, best.Peak.Y-step, best.Peak.X+step, best.Peak.Y+step, mid)
			scan(best.Peak.X-mid, best.Peak.Y-mid, best.Peak.X+mid, best.Peak.Y+mid, 1)
		} else {
			scan(best.Peak.X-step, best.Peak.Y-step, best.Peak.X+step, best.Peak.Y+step, 1)
		}
	}
	return best
}

// Skipping already-scored grid points must not change any value, peak or
// tie: large templates exercise coarse, mid and fine passes, including peaks
// at the ROI border (clamped windows) and flat images (all-equal scores).
func TestMatchSkipsRescoredGridPointsExactly(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 41))
	for i := range 80 {
		tw, th := 48+rng.IntN(90), 48+rng.IntN(90)
		w, h := tw+rng.IntN(80), th+rng.IntN(80)
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for k := range img.Pix {
			img.Pix[k] = uint8(rng.IntN(256))
			if i%10 == 9 {
				img.Pix[k] = uint8(k % 3) // nearly flat: many equal scores
			}
		}
		gray := ToGray(img)
		mx, my := rng.IntN(w-tw+1), rng.IntN(h-th+1)
		if i%5 == 0 {
			mx, my = w-tw, h-th // peak at the far corner
		}
		templ := image.NewGray(image.Rect(0, 0, tw, th))
		for y := range th {
			copy(templ.Pix[y*tw:(y+1)*tw], gray.Pix[(y+my)*gray.Stride+mx:])
		}
		for range tw * th / 4 {
			templ.Pix[rng.IntN(len(templ.Pix))] = uint8(rng.IntN(256))
		}
		var mask *image.Gray
		if i%2 == 1 {
			mask = diamondMask(tw, th)
		}
		p := PrepareNCC(templ, mask)
		for _, roi := range []image.Rectangle{img.Bounds(), img.Bounds().Inset(1), image.Rect(mx/2, my/2, w, h)} {
			roi = roi.Intersect(img.Bounds())
			got, err := (NCC{}).Match(Query{Image: img, Gray: gray, ROI: roi, Prepared: p})
			if err != nil {
				t.Fatal(err)
			}
			if roi.Dx() < tw || roi.Dy() < th {
				continue
			}
			want := matchNoSkip(gray, roi, p)
			if got != want || math.Float64bits(got.Value) != math.Float64bits(want.Value) {
				t.Fatalf("case %d %dx%d roi=%v: got %+v want %+v", i, tw, th, roi, got, want)
			}
		}
	}
}
