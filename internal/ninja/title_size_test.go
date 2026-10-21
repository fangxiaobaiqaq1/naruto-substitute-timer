package ninja

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"

	"narutotimer/internal/match"
)

// A rendered title can be a percent or two larger or smaller than its strip
// times the bead-derived HUD scale. 漩涡鸣人[暴怒·第六尾] (naruto.png) fits the
// user's video best at +1%; scored at the nominal size only, it hovered around
// the .80 threshold and dropped out at many capture scales, so its red beads
// lost their palette and read Unknown. The fine stage now also scores
// fineFactors sizes; the coarse locator is unchanged.

// titleSizeScales include capture scales the nominal-only fine stage missed.
var titleSizeScales = []float64{.75, .96, .98, 1, 1.09, 1.11, 1.13, 1.16, 1.2, 1.23, 4.0 / 3, 1.5, 2, 2.5}

func TestSixTailsTitleAtEveryCaptureScale(t *testing.T) {
	// 900x200 top-left crops of a private 1920x1080 user video (gitignored),
	// first left bead at (186,121), i.e. (93,60.5) reference pixels.
	var crops []image.Image
	for _, name := range []string{"00000", "00720", "01080"} {
		file, err := os.Open(filepath.Join("../../inbox/regressions/six-tails-20261021", "six_tails_left_hud_"+name+".png"))
		if os.IsNotExist(err) {
			t.Skip("private user video crops are not distributed")
		}
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		crops = append(crops, img)
	}
	reader := NewReader()
	resized, reads := 0, 0
	for _, sampler := range []struct {
		name string
		kind xdraw.Interpolator
	}{{"CatmullRom", xdraw.CatmullRom}, {"ApproxBiLinear", xdraw.ApproxBiLinear}} {
		for step := 95; step <= 140; step++ {
			scale := float64(step) / 100
			roi := NameRegion(image.Pt(int(math.Round(93*scale)), int(math.Round(60.5*scale))), scale, true)
			var tracker Tracker
			at := time.Unix(1700000000, 0)
			for i, crop := range crops {
				img := image.NewRGBA(image.Rect(0, 0, int(math.Round(900*scale/2)), int(math.Round(200*scale/2))))
				sampler.kind.Scale(img, img.Bounds(), crop, crop.Bounds(), draw.Src, nil)
				label := fmt.Sprintf("%s scale=%.2f crop %d", sampler.name, scale, i)
				found := reader.read(img, roi, scale)
				reads++
				if found.Name != Naruto || found.Palette != Red || found.Slots != 4 {
					t.Errorf("%s: got %+v, want %s", label, found.Readout, Naruto)
					continue
				}
				size, nominal, ok := preparedSize(reader, found.template)
				if !ok || size != found.rect.Size() {
					t.Errorf("%s: evidence rect %v is not its template's size %v (%v)", label, found.rect, size, ok)
				}
				if size != nominal {
					resized++
				}
				// The first frame searches; later frames recheck the hint.
				if got := tracker.Read(reader, img, roi, scale, at.Add(time.Duration(i)*250*time.Millisecond)); got.Name != Naruto {
					t.Errorf("%s: tracker got %+v", label, got)
				}
			}
		}
	}
	t.Logf("%d of %d reads matched a non-nominal size", resized, reads)
}

// Portable (no private data): the real strips stamped on a varied scene behind
// the HUD's translucent title band, 1-3% larger or smaller than nominal, still
// give exactly their own name, and the Tracker rechecks the size that matched.
func TestTitleStripsSurviveSizeMismatch(t *testing.T) {
	reader := NewReader()
	cases := []struct {
		file, name string
		left       bool
	}{
		{"naruto.png", Naruto, true}, {"naruto_right.png", Naruto, false},
		{"madara.png", Madara, true}, {"sasuke_xiayin.png", SasukeXiayin, false},
	}
	seed := uint64(0)
	resized, reads := 0, 0
	for _, scale := range titleSizeScales {
		for _, mismatch := range []float64{-.03, -.025, -.02, -.015, -.01, .01, .015, .02, .025, .03} {
			for _, tc := range cases {
				seed++
				img, roi := stampTitle(t, tc.file, scale, 1+mismatch, tc.left, seed)
				label := fmt.Sprintf("%s scale=%.3f size=%+.1f%%", tc.file, scale, mismatch*100)
				found := reader.read(img, roi, scale)
				reads++
				if found.Name != tc.name {
					t.Errorf("%s: got %+v, want %s", label, found.Readout, tc.name)
					continue
				}
				size, nominal, ok := preparedSize(reader, found.template)
				if !ok || size != found.rect.Size() {
					t.Errorf("%s: evidence rect %v is not its template's size %v (%v)", label, found.rect, size, ok)
				}
				if size != nominal {
					resized++
				}
				var tracker Tracker
				at := time.Unix(1700000000, 0)
				for frame := range 3 {
					if got := tracker.Read(reader, img, roi, scale, at.Add(time.Duration(frame)*100*time.Millisecond)); got.Name != tc.name {
						t.Errorf("%s frame %d: tracker got %+v", label, frame, got)
					}
				}
				if tracker.hint.template != found.template || tracker.hint.rect != found.rect {
					t.Errorf("%s: tracker rechecks %v, reader matched %v", label, tracker.hint.rect, found.rect)
				}
			}
		}
	}
	if resized < reads/2 {
		t.Fatalf("only %d of %d reads matched a non-nominal size", resized, reads)
	}
}

// The nearby sizes are prepared on first use inside concurrent searches:
// readers sharing one cached scale get the same prepared size (see -race).
func TestTitleSizesPreparedOnceForConcurrentReads(t *testing.T) {
	scale := 4.0 / 3
	img, roi := stampTitle(t, "naruto.png", scale, 1.01, true, 7)
	reader := NewReader()
	results := make([]evidence, 8)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = reader.read(img, roi, scale) })
	}
	wg.Wait()
	size, nominal, ok := preparedSize(reader, results[0].template)
	if !ok || size == nominal {
		t.Fatalf("matched %v (nominal %v, prepared %v), want a nearby size", size, nominal, ok)
	}
	for i, got := range results {
		if got.Name != Naruto || got.template != results[0].template || got.rect != results[0].rect {
			t.Fatalf("read %d: %+v %v, read 0: %+v %v", i, got.Readout, got.rect, results[0].Readout, results[0].rect)
		}
	}
}

// Negative controls for the extra sizes: no name on title-free backgrounds or
// untemplated ordinary titles, and the shared 漩涡鸣人[ prefix never turns
// 鸣人[忍者学员] into 鸣人[暴怒·第六尾] or back, at any scale and size mismatch.
func TestTitleSizesKeepNegativeControls(t *testing.T) {
	reader := NewReader()
	seed := uint64(1000)
	for _, scale := range append([]float64{.6, .85, 1.75, 2.25, 3}, titleSizeScales...) {
		for _, left := range []bool{true, false} {
			seed++
			for _, band := range []bool{true, false} {
				img, roi := stampTitle(t, "", scale, 1, left, seed)
				if !band {
					img = variedBackground(img.Bounds().Dx(), img.Bounds().Dy(), seed)
				}
				if got := reader.Read(img, roi, scale); got != (Readout{}) {
					t.Errorf("background scale=%.3f left=%v band=%v: got %+v", scale, left, band, got)
				}
			}
		}
		for _, mismatch := range []float64{-.03, -.02, -.01, 0, .01, .02, .03} {
			for _, tc := range []struct {
				file string
				name string // the only name it may give; at the nominal size it must
				left bool
			}{
				{"naruto_student.png", NarutoStudent, true}, {"naruto_student.png", NarutoStudent, false},
				{"naruto.png", Naruto, true}, {"naruto_right.png", Naruto, false},
				{"sakura.png", "", true}, {"shino.png", "", false}, {"tayuya.png", "", true},
			} {
				seed++
				img, roi := stampTitle(t, tc.file, scale, 1+mismatch, tc.left, seed)
				got := reader.Read(img, roi, scale)
				if got.Name != tc.name && (got.Name != "" || mismatch == 0) {
					t.Errorf("%s scale=%.3f size=%+.0f%% left=%v: got %+v, want %q", tc.file, scale, mismatch*100, tc.left, got, tc.name)
				}
			}
		}
	}
}

// stampTitle draws a strip from templates/ (none when file is empty) resized
// to factor times its nominal size at scale, with a different resampler than
// the reader's, into its NameRegion: a varied scene under the HUD's
// translucent title band.
func stampTitle(t *testing.T, file string, scale, factor float64, left bool, seed uint64) (*image.RGBA, image.Rectangle) {
	t.Helper()
	first := image.Pt(int(math.Round(93*scale)), int(math.Round(61*scale)))
	if !left {
		first.X = int(math.Round(835 * scale))
	}
	roi := NameRegion(first, scale, left)
	img := variedBackground(int(math.Round(960*scale)), int(math.Round(110*scale)), seed)
	band := roi.Intersect(img.Bounds())
	for y := band.Min.Y; y < band.Max.Y; y++ {
		for x := band.Min.X; x < band.Max.X; x++ {
			p := img.Pix[img.PixOffset(x, y):]
			p[0], p[1], p[2] = p[0]/2, p[1]/2, p[2]/2
		}
	}
	if file == "" {
		return img, roi
	}
	data, err := diskTemplates.ReadFile("templates/" + file)
	if err != nil {
		t.Fatal(err)
	}
	strip, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	size := image.Pt(int(math.Round(float64(strip.Bounds().Dx())*scale*factor)), int(math.Round(float64(strip.Bounds().Dy())*scale*factor)))
	at := image.Pt(roi.Min.X+int(math.Round(12*scale)), roi.Min.Y+int(math.Round(9*scale)))
	if !left {
		at.X = roi.Max.X - size.X - int(math.Round(12*scale))
	}
	xdraw.CatmullRom.Scale(img, image.Rectangle{Min: at, Max: at.Add(size)}, strip, strip.Bounds(), draw.Src, nil)
	return img, roi
}

// variedBackground is a deterministic game-like scene: a two-colour gradient,
// scattered blocks and bars, and pixel noise.
func variedBackground(w, h int, seed uint64) *image.RGBA {
	rng := rand.New(rand.NewPCG(seed, 0x6e617275746f))
	pick := func() color.RGBA {
		return color.RGBA{uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)), 255}
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c0, c1 := pick(), pick()
	for y := range h {
		for x := range w {
			a := (float64(x)/float64(w) + float64(y)/float64(h)) / 2
			p := img.Pix[y*img.Stride+4*x : y*img.Stride+4*x+4]
			p[0] = uint8(float64(c0.R)*(1-a) + float64(c1.R)*a)
			p[1] = uint8(float64(c0.G)*(1-a) + float64(c1.G)*a)
			p[2] = uint8(float64(c0.B)*(1-a) + float64(c1.B)*a)
			p[3] = 255
		}
	}
	for range 30 {
		x, y := rng.IntN(w), rng.IntN(h)
		block := image.Rect(x, y, x+4+rng.IntN(max(5, w/6)), y+2+rng.IntN(max(3, h/3)))
		draw.Draw(img, block, image.NewUniform(pick()), image.Point{}, draw.Src)
	}
	for i := 0; i < len(img.Pix); i += 4 {
		bits := rng.Uint64() // three noise bytes per pixel
		for c := range 3 {
			noise := int(bits>>(8*c)&0xff)%25 - 12
			img.Pix[i+c] = uint8(min(255, max(0, int(img.Pix[i+c])+noise)))
		}
	}
	return img
}

// preparedSize finds p among the most recent scale's templates: its size and
// the nominal size of its strip.
func preparedSize(r *Reader, p *match.PreparedNCC) (size, nominal image.Point, ok bool) {
	for _, t := range r.testPrepared() {
		if t.ncc == p {
			return t.size, t.size, true
		}
		for _, v := range t.variants.get() {
			if v.ncc == p {
				return v.size, t.size, true
			}
		}
	}
	return image.Point{}, image.Point{}, false
}
