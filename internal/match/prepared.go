package match

import (
	"image"
	"math"
	"sync"
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
			// Split very long runs so each run's dot product fits in uint32.
			for ; start < x; start += maxDotRun {
				p.runs = append(p.runs, nccRun{x: start, y: y, pixels: row[start:min(x, start+maxDotRun)]})
			}
		}
	}
	p.count, p.sum = float64(count), float64(sum)
	if count > 0 {
		p.variance = float64(squares) - p.sum*p.sum/p.count
	}
	return p
}

// grayPrefix holds per-row prefix sums of gray and gray² over one rectangle
// of a Gray image, in Pix offsets relative to Bounds().Min (the same offsets
// at uses). Row r has w+1 entries; entry k is the sum of the first k pixels.
// It is built once per Match call and used by a single goroutine only.
type grayPrefix struct {
	x0, y0, w   int
	sum, square []uint64
}

var prefixPool = sync.Pool{New: func() any { return new(grayPrefix) }}

// newGrayPrefix covers columns [x0,x0+w) and rows [y0,y0+h) of img.
func newGrayPrefix(img *image.Gray, x0, y0, w, h int) *grayPrefix {
	g := prefixPool.Get().(*grayPrefix)
	g.x0, g.y0, g.w = x0, y0, w
	n := h * (w + 1)
	if cap(g.sum) < n {
		g.sum, g.square = make([]uint64, n), make([]uint64, n)
	}
	g.sum, g.square = g.sum[:n], g.square[:n]
	for y := 0; y < h; y++ {
		off := (y0+y)*img.Stride + x0
		row := img.Pix[off : off+w]
		sums := g.sum[y*(w+1) : (y+1)*(w+1)]
		squares := g.square[y*(w+1) : (y+1)*(w+1)]
		var s, q uint64
		sums[0], squares[0] = 0, 0
		for x, v := range row {
			s += uint64(v)
			q += uint64(v) * uint64(v)
			sums[x+1], squares[x+1] = s, q
		}
	}
	return g
}

func (g *grayPrefix) release() { prefixPool.Put(g) }

// maxDotRun bounds one uint32 dot accumulation: 255*255*maxDotRun < 2^32.
const maxDotRun = 66051

// at scores the template at (ox, oy). pre must cover the template footprint.
// Sums are exact integers, so the float formula sees the same values (and
// performs the same operations in the same order) as the per-pixel scalar
// accumulation it replaced: scores are bit-identical.
func (p *PreparedNCC) at(img *image.Gray, pre *grayPrefix, ox, oy int) float64 {
	if p.count < 8 {
		return 0
	}
	var sum, squares, product uint64
	stride := pre.w + 1
	// Match bounds-checks the whole template footprint first. Slicing once per
	// run removes per-pixel GrayAt bounds/origin/mask checks from the hot loop.
	for _, run := range p.runs {
		t := run.pixels
		base := (oy+run.y-pre.y0)*stride + ox + run.x - pre.x0
		end := base + len(t)
		sum += pre.sum[end] - pre.sum[base]
		squares += pre.square[end] - pre.square[base]
		off := (oy+run.y)*img.Stride + ox + run.x
		row := img.Pix[off : off+len(t)]
		row = row[:len(t)]
		// PrepareNCC caps runs at maxDotRun, so uint32 cannot overflow.
		var a, b, c, d uint32
		i := 0
		for ; i+4 <= len(t); i += 4 {
			r, q := row[i:i+4:i+4], t[i:i+4:i+4]
			a += uint32(r[0]) * uint32(q[0])
			b += uint32(r[1]) * uint32(q[1])
			c += uint32(r[2]) * uint32(q[2])
			d += uint32(r[3]) * uint32(q[3])
		}
		for ; i < len(t); i++ {
			a += uint32(row[i]) * uint32(t[i])
		}
		product += uint64(a) + uint64(b) + uint64(c) + uint64(d)
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
