package hudtext

import (
	"image"
	"math"
	"strings"

	"narutotimer/internal/ocr"
)

type glyphSample struct {
	p     image.Point
	value float64
}
type glyphCell struct {
	samples       []glyphSample
	sum, variance float64
}

// Ephemeral visual evidence from OCR word coordinates, not a pre-authored
// ninja template. Each small glyph cell must match CURRENT pixels; a long
// account/version cannot hide a changed character in a whole-line average.
type glyphEvidence struct {
	bounds image.Rectangle
	cells  []glyphCell
}

func luma(img *image.RGBA, p image.Point) float64 {
	c := img.RGBAAt(p.X, p.Y)
	return float64((299*uint32(c.R) + 587*uint32(c.G) + 114*uint32(c.B) + 500) / 1000)
}

func newGlyphEvidence(img *image.RGBA, bounds image.Rectangle) *glyphEvidence {
	bounds = bounds.Intersect(img.Bounds())
	if bounds.Dx() < 8 || bounds.Dy() < 8 || bounds.Dx() > 1000 || bounds.Dy() > 120 {
		return nil
	}
	mask := map[image.Point]bool{}
	ink := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if min(c.R, c.G, c.B) < 170 || int(max(c.R, c.G, c.B))-int(min(c.R, c.G, c.B)) > 65 {
				continue
			}
			dark := false
			for _, p := range []image.Point{{x - 2, y}, {x + 2, y}, {x, y - 2}, {x, y + 2}} {
				if p.In(bounds) && luma(img, p) < 100 {
					dark = true
					break
				}
			}
			if !dark {
				continue
			}
			ink++
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					p := image.Pt(x+dx, y+dy)
					if p.In(bounds) {
						mask[p] = true
					}
				}
			}
		}
	}
	if ink < 12 {
		return nil
	}
	e := &glyphEvidence{bounds: bounds}
	step := max(4, bounds.Dy()/2)
	for x := bounds.Min.X; x < bounds.Max.X; x += step {
		cell := glyphCell{}
		squares := 0.
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for px := x; px < min(x+step, bounds.Max.X); px++ {
				p := image.Pt(px, y)
				if !mask[p] {
					continue
				}
				v := luma(img, p)
				cell.samples = append(cell.samples, glyphSample{p, v})
				cell.sum += v
				squares += v * v
			}
		}
		n := float64(len(cell.samples))
		if n < 12 {
			continue
		}
		cell.variance = squares - cell.sum*cell.sum/n
		if cell.variance/n < 400 {
			continue
		}
		e.cells = append(e.cells, cell)
	}
	if len(e.cells) < 2 {
		return nil
	}
	return e
}

func (e *glyphEvidence) matches(img *image.RGBA) bool {
	if e == nil || img == nil {
		return false
	}
	// One global alignment, never independently move each glyph to fit another.
	for _, dy := range [...]int{0, -1, 1} {
		for _, dx := range [...]int{0, -1, 1} {
			delta := image.Pt(dx, dy)
			if !e.bounds.Add(delta).In(img.Bounds()) {
				continue
			}
			all := true
			for _, cell := range e.cells {
				sum, squares, dot := 0., 0., 0.
				for _, sample := range cell.samples {
					v := luma(img, sample.p.Add(delta))
					sum += v
					squares += v * v
					dot += v * sample.value
				}
				n := float64(len(cell.samples))
				variance := squares - sum*sum/n
				if variance <= 0 || (dot-cell.sum*sum/n)/math.Sqrt(variance*cell.variance) < .92 {
					all = false
					break
				}
			}
			if all {
				return true
			}
		}
	}
	return false
}

// Recover only a field that the OCR parser actually read, using the word boxes
// in an agreeing treatment. Account evidence includes BOTH parentheses so a
// longer account cannot validate the shorter prefix. Coordinates map back to
// native pixels, including their crop origin; no fixed ninja-name x position.
func (s sheet) evidence(lines []ocr.Line, request job, side int, expected string) *glyphEvidence {
	if expected == "" || request.strips[side].img == nil {
		return nil
	}
	for _, row := range s.rows {
		if row.side != side {
			continue
		}
		var chars []rune
		var boxes []image.Rectangle
		for _, w := range wordsForRow(lines, row.rect) {
			text := []rune(compact(w.Text))
			if len(text) == 0 {
				continue
			}
			for i, r := range text {
				x0 := w.X + float64(i)*w.Width/float64(len(text))
				x1 := w.X + float64(i+1)*w.Width/float64(len(text))
				chars = append(chars, r)
				boxes = append(boxes, image.Rect(int(math.Floor(x0)), int(math.Floor(w.Y)), int(math.Ceil(x1)), int(math.Ceil(w.Y+w.Height))))
			}
		}
		joined := string(chars)
		start := strings.Index(joined, expected)
		if start < 0 || strings.Count(joined, expected) != 1 {
			continue
		}
		// Indices above are bytes, boxes are Unicode glyphs.
		first := len([]rune(joined[:start]))
		last := first + len([]rune(expected))
		var box image.Rectangle
		for _, r := range boxes[first:last] {
			box = box.Union(r)
		}
		roi := request.key.regions[side]
		sx, sy := float64(roi.Dx())/float64(row.rect.Dx()), float64(roi.Dy())/float64(row.rect.Dy())
		local := image.Rect(int(math.Floor(float64(box.Min.X-row.rect.Min.X)*sx)), int(math.Floor(float64(box.Min.Y-row.rect.Min.Y)*sy)), int(math.Ceil(float64(box.Max.X-row.rect.Min.X)*sx)), int(math.Ceil(float64(box.Max.Y-row.rect.Min.Y)*sy))).Inset(-1)
		strip := request.strips[side].img
		// CropRGBA owns zero-origin pixels. Store sample coordinates in the full frame.
		proof := newGlyphEvidence(strip, local)
		if proof == nil {
			continue
		}
		proof.bounds = proof.bounds.Add(roi.Min)
		for i := range proof.cells {
			for j := range proof.cells[i].samples {
				proof.cells[i].samples[j].p = proof.cells[i].samples[j].p.Add(roi.Min)
			}
		}
		return proof
	}
	return nil
}

func validWordBox(w ocr.Word, row image.Rectangle) bool {
	for _, v := range [...]float64{w.X, w.Y, w.Width, w.Height} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return w.Width > 0 && w.Height > 0 && w.X >= float64(row.Min.X) &&
		w.Y >= float64(row.Min.Y) && w.X+w.Width <= float64(row.Max.X) &&
		w.Y+w.Height <= float64(row.Max.Y)
}
