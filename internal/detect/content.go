package detect

import (
	"image"
	"math"
)

// ResolveContentArea maps calibrated coordinates into an actual screenshot.
// Auto accepts a different aspect ratio only when the predicted surrounding bars
// are visibly black. A native wider/taller HUD is a different layout, not proof
// of letterboxing. The boolean is false when its geometry cannot be established.
func ResolveContentArea(img *image.RGBA, mode ContentMode, referenceWidth, referenceHeight int, aspectTolerance float64) (ContentArea, bool) {
	if img == nil || img.Bounds().Empty() || referenceWidth <= 0 || referenceHeight <= 0 {
		return ContentArea{}, false
	}
	b := img.Bounds()
	full := ContentArea{X: b.Min.X, Y: b.Min.Y, W: b.Dx(), H: b.Dy()}
	if mode == ModeStretch {
		return full, true
	}
	target := float64(referenceWidth) / float64(referenceHeight)
	ratio := float64(b.Dx()) / float64(b.Dy())
	if mode == ModeAuto && math.Abs(ratio/target-1) <= aspectTolerance {
		return full, true
	}
	scale := math.Min(float64(b.Dx())/float64(referenceWidth), float64(b.Dy())/float64(referenceHeight))
	w := int(math.Round(float64(referenceWidth) * scale))
	h := int(math.Round(float64(referenceHeight) * scale))
	content := image.Rect(b.Min.X+(b.Dx()-w)/2, b.Min.Y+(b.Dy()-h)/2, b.Min.X+(b.Dx()-w)/2+w, b.Min.Y+(b.Dy()-h)/2+h)
	area := ContentArea{X: content.Min.X, Y: content.Min.Y, W: content.Dx(), H: content.Dy()}
	if mode == ModeLetterbox {
		return area, true
	}
	if mode != ModeAuto {
		return ContentArea{}, false
	}
	// Examine every bar separately so a black toolbar on one edge cannot pass
	// as centered letterboxing. Skip the last two boundary pixels, where a
	// resize filter may blend content into a real bar.
	bars := []image.Rectangle{
		{Min: b.Min, Max: image.Pt(b.Max.X, content.Min.Y-2)},
		{Min: image.Pt(b.Min.X, content.Max.Y+2), Max: b.Max},
		{Min: image.Pt(b.Min.X, content.Min.Y), Max: image.Pt(content.Min.X-2, content.Max.Y)},
		{Min: image.Pt(content.Max.X+2, content.Min.Y), Max: image.Pt(b.Max.X, content.Max.Y)},
	}
	checked := 0
	for _, bar := range bars {
		if bar.Empty() {
			continue
		}
		checked++
		if !blackBar(img, bar) {
			return ContentArea{}, false
		}
	}
	if checked < 2 {
		return ContentArea{}, false
	}
	return area, true
}

func blackBar(img *image.RGBA, r image.Rectangle) bool {
	// A bounded grid makes this cheap even for a 4K capture. A real bar should
	// be almost entirely neutral black, unlike a dark blue HUD or desktop frame.
	sx, sy := max(1, r.Dx()/64), max(1, r.Dy()/12)
	total, black := 0, 0
	for y := r.Min.Y; y < r.Max.Y; y += sy {
		for x := r.Min.X; x < r.Max.X; x += sx {
			c := img.RGBAAt(x, y)
			total++
			if max(c.R, c.G, c.B) <= 12 {
				black++
			}
		}
	}
	return total > 0 && black*100 >= total*98
}
