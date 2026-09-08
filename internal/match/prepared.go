package match

import (
	"image"
	"math"
)

// PreparedNCC owns a snapshot of one template and its binary mask. It is
// immutable and safe to share between matchers. Cache only the current geometry;
// preparing at every frame would waste work and memory after source resizes.
// No pixel is sampled approximately: all nonzero-mask pixels contribute exactly
// as in the scalar matcher.
type PreparedNCC struct {
	rect                 image.Rectangle
	runs                 []nccRun
	count, sum, variance float64
}

type nccRun struct {
	x, y   int
	pixels []byte
}

// PrepareNCC precomputes template statistics and contiguous mask runs. Mask
// origins/strides need not match the template; a smaller mask omits its missing
// pixels and all nonzero bytes are equally included (not weighted).
func PrepareNCC(templ, mask *image.Gray) *PreparedNCC {
	if templ == nil || templ.Bounds().Empty() {
		return nil
	}
	b := templ.Bounds()
	w, h := b.Dx(), b.Dy()
	p := &PreparedNCC{rect: b}
	data := make([]byte, w*h)
	var count, sum, squares int64
	p.runs = make([]nccRun, 0, h)
	for y := 0; y < h; y++ {
		row := data[y*w : (y+1)*w]
		copy(row, templ.Pix[y*templ.Stride:y*templ.Stride+w])
		if mask != nil && y >= mask.Bounds().Dy() {
			continue
		}
		end := w
		if mask != nil {
			end = min(end, mask.Bounds().Dx())
		}
		for x := 0; x < end; {
			if mask != nil && mask.Pix[y*mask.Stride+x] == 0 {
				x++
				continue
			}
			start := x
			for x < end && (mask == nil || mask.Pix[y*mask.Stride+x] != 0) {
				v := int64(row[x])
				count++
				sum += v
				squares += v * v
				x++
			}
			p.runs = append(p.runs, nccRun{x: start, y: y, pixels: row[start:x]})
		}
	}
	p.count, p.sum = float64(count), float64(sum)
	if count > 0 {
		p.variance = float64(squares) - p.sum*p.sum/p.count
	}
	return p
}

func (p *PreparedNCC) at(img *image.Gray, ox, oy int) float64 {
	if p.count < 8 {
		return 0
	}
	var sum, squares, product int64
	// Match bounds-checks the whole template footprint first. Slicing once per
	// run removes per-pixel GrayAt bounds/origin/mask checks from the hot loop.
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
