package identity

import (
	"image"
	"image/color"
)

// HUD name patches include animated scenery outside the white text's dark
// outline. Match the glyph and its nearby outline, not that old scenery. This
// is derived from every configured account template, never from a ninja name.
func accountGlyphMask(templ *image.Gray) *image.Gray {
	b := templ.Bounds()
	mask := image.NewGray(b)
	seeds := 0
	radius := max(1, b.Dy()/11)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if templ.GrayAt(x, y).Y < 180 {
				continue
			}
			outlined := false
			for _, p := range []image.Point{{x - radius, y}, {x + radius, y}, {x, y - radius}, {x, y + radius}} {
				if p.In(b) && templ.GrayAt(p.X, p.Y).Y < 90 {
					outlined = true
					break
				}
			}
			if !outlined {
				continue
			}
			seeds++
			for dy := -radius; dy <= radius; dy++ {
				for dx := -radius; dx <= radius; dx++ {
					if (image.Pt(x+dx, y+dy)).In(b) {
						mask.SetGray(x+dx, y+dy, color.Gray{Y: 255})
					}
				}
			}
		}
	}
	if seeds < 12 {
		return nil
	}
	return mask
}
