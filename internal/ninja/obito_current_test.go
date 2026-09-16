package ninja

import (
	"bytes"
	"image"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"testing"

	"narutotimer/internal/match"
)

func TestObitoCurrentTemplateRejectsPartialVersion(t *testing.T) {
	data, err := templates.ReadFile("templates/obito_current.png")
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 960, 540))
	box := src.Bounds().Add(image.Pt(100, 20))
	draw.Draw(img, box, src, src.Bounds().Min, draw.Src)
	roi := NameRegion(image.Pt(93, 61), 1, true)
	r := NewReader()
	if got := r.Read(img, roi, 1); got.Name != Obito || got.Slots != 4 || got.Palette != Purple || got.RowOffsetY != 0 {
		t.Fatalf("full version: %+v", got)
	}
	draw.Draw(img, image.Rect(box.Min.X+box.Dx()/2, box.Min.Y, box.Max.X, box.Max.Y), image.Black, image.Point{}, draw.Src)
	if got := r.Read(img, roi, 1); got != (Readout{}) {
		t.Fatalf("partial name enabled purple skin: %+v", got)
	}
}

func TestObitoCurrentTrainingNames(t *testing.T) {
	paths, _ := filepath.Glob("../../debug/obito-flicker-20260916-before/diagnostic-*.png")
	paths = append(paths, "../../inbox/regressions/camp-obito-flicker-20260916/game.png")
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := os.Open(path)
			if os.IsNotExist(err) {
				t.Skip("local user evidence is not distributed")
			}
			if err != nil {
				t.Fatal(err)
			}
			src, _, err := image.Decode(f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			img := image.NewRGBA(src.Bounds())
			draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
			scale := float64(img.Bounds().Dx()) / 960
			first := image.Pt(int(math.Round(float64(img.Bounds().Dx())*155/1600)), int(math.Round(float64(img.Bounds().Dy())*101/900)))
			roi := NameRegion(first, scale, true)
			r := NewReader()
			got := r.Read(img, roi, scale)
			if got.Name != Obito {
				for _, templ := range r.prepared {
					score, _ := (match.NCC{}).Match(match.Query{Image: img, ROI: roi, Prepared: templ.ncc})
					t.Logf("candidate %s score=%+v", templ.readout.Name, score)
				}
				t.Fatalf("name %+v", got)
			}
		})
	}
}
