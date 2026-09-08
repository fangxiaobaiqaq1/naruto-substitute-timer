package detect

import (
	"image"
	"image/color"
	"testing"
)

func TestGuessTopChromeFindsDarkBar(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 480, 280))
	fill(img, img.Bounds(), color.RGBA{R: 30, G: 140, B: 200, A: 255})
	fill(img, image.Rect(0, 0, 480, 36), color.RGBA{R: 32, G: 32, B: 36, A: 255})
	got := GuessTopChrome(img)
	if got < 28 || got > 40 {
		t.Fatalf("chrome = %d", got)
	}
	stripped := StripChrome(img)
	if stripped.Bounds().Dy() != 280-got {
		t.Fatalf("stripped h = %d", stripped.Bounds().Dy())
	}
}

func TestGuessTopChromeLeavesFightAlone(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 480, 280))
	fill(img, img.Bounds(), color.RGBA{R: 40, G: 90, B: 160, A: 255})
	if got := GuessTopChrome(img); got != 0 {
		t.Fatalf("full-color frame should have no chrome, got %d", got)
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
