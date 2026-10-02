package ninja

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"testing"

	xdraw "golang.org/x/image/draw"

	"narutotimer/assets"
)

// perfPortraitFrame renders catalog portrait id at scale inside a padded
// frame, the same geometry TestAvatarRecognitionSurvivesScaledLeftAndRightROI
// uses, and returns the frame and its portrait ROI.
func perfPortraitFrame(tb testing.TB, c *avatarCatalog, id string, scale float64) (*image.RGBA, image.Rectangle) {
	tb.Helper()
	entry := c.byID[id]
	if entry == nil {
		tb.Fatalf("missing avatar %s", id)
	}
	portrait, _, err := image.Decode(bytes.NewReader(entry.data))
	if err != nil {
		tb.Fatal(err)
	}
	w, h := int(float64(portrait.Bounds().Dx())*scale+.5), int(float64(portrait.Bounds().Dy())*scale+.5)
	img := image.NewRGBA(image.Rect(0, 0, w+40, h+40))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{21, 47, 68, 255}), image.Point{}, draw.Src)
	roi := image.Rect(20, 20, 20+w, 20+h)
	xdraw.BiLinear.Scale(img, roi, portrait, portrait.Bounds(), draw.Over, nil)
	return img, roi
}

// perfItachiFrame is the canonical 960x540 百战鼬 title+portrait composition
// (TestItachiHyakusenTitleAndPortraitRequireBothCurrentSignals) resampled to
// scale, plus its right title ROI.
func perfItachiFrame(tb testing.TB, scale float64) (*image.RGBA, image.Rectangle) {
	tb.Helper()
	load := func(name string) image.Image {
		data, err := templates.ReadFile("templates/" + name)
		if err != nil {
			tb.Fatal(err)
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			tb.Fatal(err)
		}
		return img
	}
	title, portrait := load("itachi_hyakusen_mumu.png"), load("itachi_hyakusen_portrait.png")
	base := image.NewRGBA(image.Rect(0, 0, 960, 540))
	draw.Draw(base, base.Bounds(), image.NewUniform(color.RGBA{31, 40, 52, 255}), image.Point{}, draw.Src)
	roi := NameRegion(image.Pt(835, 61), 1, false)
	at := image.Pt(roi.Min.X+12, roi.Min.Y+7)
	draw.Draw(base, image.Rectangle{Min: at, Max: at.Add(title.Bounds().Size())}, title, title.Bounds().Min, draw.Src)
	draw.Draw(base, image.Rect(848, 8, 848+portrait.Bounds().Dx(), 8+portrait.Bounds().Dy()), portrait, portrait.Bounds().Min, draw.Src)
	if scale == 1 {
		return base, roi
	}
	img := image.NewRGBA(image.Rect(0, 0, int(960*scale+.5), int(540*scale+.5)))
	xdraw.BiLinear.Scale(img, img.Bounds(), base, base.Bounds(), draw.Src, nil)
	return img, NameRegion(image.Pt(int(835*scale+.5), int(61*scale+.5)), scale, false)
}

func BenchmarkAvatarMatchHUD(b *testing.B) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		b.Fatal(err)
	}
	const scale = 1280.0 / 960
	img, roi := perfPortraitFrame(b, c, "90511", scale)
	c.Match(img, roi, scale)
	b.ReportAllocs()
	for b.Loop() {
		c.Match(img, roi, scale)
	}
}

func BenchmarkAvatarMatchEntriesShortlist(b *testing.B) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		b.Fatal(err)
	}
	const scale = 1280.0 / 960
	img, roi := perfPortraitFrame(b, c, "90511", scale)
	full := c.Match(img, roi, scale)
	var entries []*avatarEntry
	for _, id := range full.ids[:min(6, len(full.ids))] {
		entries = append(entries, c.byID[id])
	}
	view := img.SubImage(roi).(*image.RGBA)
	b.ReportAllocs()
	for b.Loop() {
		c.matchEntries(view, nil, scale, entries)
	}
}

func BenchmarkReaderRead(b *testing.B) {
	r := NewReader()
	for _, tc := range []struct {
		name  string
		scale float64
	}{{"itachi_1280", 1280.0 / 960}, {"itachi_1440", 1.5}} {
		img, roi := perfItachiFrame(b, tc.scale)
		if got := r.read(img, roi, tc.scale); got.Name != ItachiHyakusen {
			b.Fatalf("%s fixture not recognized: %+v", tc.name, got.Readout)
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r.read(img, roi, tc.scale)
			}
		})
	}
}

func BenchmarkAvatarCatalogLoad(b *testing.B) {
	read := func(id string) ([]byte, error) { return assets.ASAvatars.ReadFile("avatars/" + id + ".png") }
	b.ReportAllocs()
	for b.Loop() {
		if _, err := loadAvatarCatalog(assets.ASAvatarIndex, read); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkItachiPortraitEvidence(b *testing.B) {
	const scale = 1280.0 / 960
	r := NewReader()
	img, roi := perfItachiFrame(b, scale)
	found := r.read(img, roi, scale)
	if found.portrait == nil {
		b.Fatalf("fixture has no portrait evidence: %+v", found.Readout)
	}
	b.ReportAllocs()
	for b.Loop() {
		if !itachiPortraitEvidence(img, scale, found.portrait, found.portraitSize) {
			b.Fatal("portrait evidence lost")
		}
	}
}
