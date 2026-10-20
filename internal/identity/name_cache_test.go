package identity

import (
	"image"
	"image/color"
	"image/draw"
	"math/rand/v2"
	"testing"

	xdraw "golang.org/x/image/draw"

	"narutotimer/internal/detect"
)

// glyphCrop paints outlined white "glyphs" on a dark noisy strip, like a HUD
// account name (accountGlyphMask finds seeds in it).
func glyphCrop(r *rand.Rand, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		v := uint8(30 + r.IntN(60))
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v+10, 255
	}
	for x := 4; x+8 < w; x += 10 + r.IntN(6) {
		gh := h/2 + r.IntN(h/3)
		y0 := (h - gh) / 2
		draw.Draw(img, image.Rect(x-1, y0-1, x+6, y0+gh+1), image.NewUniform(color.RGBA{5, 5, 5, 255}), image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(x, y0, x+5, y0+gh), image.NewUniform(color.RGBA{240, 240, 235, 255}), image.Point{}, draw.Src)
	}
	return img
}

// Cached account-name templates give exactly the uncached hits, for the
// glyph-masked battle path (with its fallbacks and +-1 px refinement) and the
// unmasked VS path, on repeated reads.
func TestCachedNameTemplatesEqualUncached(t *testing.T) {
	r := rand.New(rand.NewPCG(77, 5))
	var book []Named
	for i := range 6 {
		book = append(book, Named{Name: string(rune('a' + i)), Image: glyphCrop(r, 90+r.IntN(80), 22+r.IntN(8)), Mine: i < 2, Scene: "fight"})
	}
	for _, size := range [][2]int{{1920, 1080}, {1280, 720}, {2560, 1440}} {
		frame := image.NewRGBA(image.Rect(0, 0, size[0], size[1]))
		for i := 0; i < len(frame.Pix); i += 4 {
			v := uint8(r.IntN(120))
			frame.Pix[i], frame.Pix[i+1], frame.Pix[i+2], frame.Pix[i+3] = v, v/2, v, 255
		}
		ca, ok := detect.ResolveContentArea(frame, detect.ModeStretch, detect.LogicWidth, detect.LogicHeight, 0.015)
		if !ok {
			t.Fatal("content area")
		}
		// Paste two book names (slightly rescaled) into both name strips.
		for i, side := range []string{"left", "right"} {
			roi := mapRect(ca, FightROIs.Left, side)
			if side == "right" {
				roi = mapRect(ca, FightROIs.Right, side)
			}
			src := book[i*3].Image
			s := ca.HUDScale(detect.LogicWidth, detect.LogicHeight) * (1 + float64(i)*.03)
			dst := image.Rect(0, 0, int(float64(src.Bounds().Dx())*s), int(float64(src.Bounds().Dy())*s)).Add(roi.Min.Add(image.Pt(10, 4)))
			xdraw.BiLinear.Scale(frame, dst, src, src.Bounds(), draw.Src, nil)
		}
		for _, side := range []string{"left", "right"} {
			roi := FightROIs.Left
			if side == "right" {
				roi = FightROIs.Right
			}
			for _, maxWidth := range []int{960, 0} {
				cacheNameTemplates = false
				want := bestHit(frame, ca, roi, book, maxWidth, side)
				cacheNameTemplates = true
				for range 2 {
					if got := bestHit(frame, ca, roi, book, maxWidth, side); got != want {
						t.Fatalf("%v %s max=%d: cached %+v, uncached %+v", size, side, maxWidth, got, want)
					}
				}
			}
		}
	}
	nameTemplates.Lock()
	cached := len(nameTemplates.m)
	nameTemplates.Unlock()
	if cached == 0 {
		t.Fatal("nothing cached")
	}
}
