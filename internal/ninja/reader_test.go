package ninja

import (
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"testing"

	xdraw "golang.org/x/image/draw"

	"narutotimer/internal/match"
)

func TestUserSpecialNames(t *testing.T) {
	r := NewReader()
	for _, tc := range []struct {
		file, name string
		x, y       int
	}{
		{"hashirama-full", Hashirama, 88, 44}, {"hashirama-three", Hashirama, 81, 59}, {"hashirama-four", Hashirama, 83, 52},
		{"madara-full", Madara, 92, 61}, {"madara-four", Madara, 91, 58}, {"obito-full", Obito, 86, 55}, {"obito-two", Obito, 87, 56},
		{"naruto-full", Naruto, 93, 56}, {"naruto-two", Naruto, 87, 61},
	} {
		file, err := os.Open(filepath.Join("../../inbox/regressions/special-ninjas-20260907", tc.file+".png"))
		if err != nil {
			t.Fatal(err)
		}
		source, _, err := image.Decode(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, scale := range []float64{.75, 1, 1.5, 2} {
			b := image.Rect(0, 0, int(float64(source.Bounds().Dx())*scale), int(float64(source.Bounds().Dy())*scale))
			img := image.NewRGBA(b)
			xdraw.BiLinear.Scale(img, b, source, source.Bounds(), draw.Src, nil)
			got := r.Read(img, NameRegion(image.Pt(int(float64(tc.x)*scale), int(float64(tc.y)*scale)), scale, true), scale)
			if got.Name != tc.name {
				t.Errorf("%s scale=%v got=%+v want=%q", tc.file, scale, got, tc.name)
				for _, templ := range r.prepared {
					score, _ := (match.NCC{}).Match(match.Query{Image: img, ROI: NameRegion(image.Pt(int(float64(tc.x)*scale), int(float64(tc.y)*scale)), scale, true), Prepared: templ.ncc})
					t.Logf("candidate %s score=%+v", templ.readout.Name, score)
				}
			}
		}
	}
}

func TestNameReaderRejectsUnknownAndBlank(t *testing.T) {
	r := NewReader()
	img := image.NewRGBA(image.Rect(0, 0, 325, 100))
	if got := r.Read(img, img.Bounds(), 1); got.Name != "" {
		t.Fatalf("blank name: %+v", got)
	}
	f, err := os.Open("../../inbox/regressions/duel-user-20260906.png")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	frame := image.NewRGBA(source.Bounds())
	draw.Draw(frame, frame.Bounds(), source, source.Bounds().Min, draw.Src)
	if got := r.Read(frame, image.Rect(90, 10, 400, 45), 1); got.Name != "" {
		t.Fatalf("ordinary ninja became special: %+v", got)
	}
}
