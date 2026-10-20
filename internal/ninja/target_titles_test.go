package ninja

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"math"
	"testing"
	"time"

	"narutotimer/internal/match"
)

// These are portable template regressions. They replay the reviewed native
// title strips, which stay confined to the embedded package, at several content
// widths and on both sides. They prove title recognition only, never a gameplay
// property inferred from the names.
func TestReviewedTargetTitlesRecognizeWithoutGameplayInference(t *testing.T) {
	for _, tc := range []struct {
		file, source string
		name         string
	}{
		{"itachi_hyakusen.png", "itachi_hyakusen.source.png", ItachiHyakusen},
		{"minato_kyubi.png", "minato_kyubi.source.png", MinatoKyubi},
		{"hashirama_edo.png", "hashirama_edo.source.png", HashiramaEdo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := templates.ReadFile("templates/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			template, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			sourceData, err := templates.ReadFile("templates/" + tc.source)
			if err != nil {
				t.Fatal(err)
			}
			native, _, err := image.Decode(bytes.NewReader(sourceData))
			if err != nil {
				t.Fatal(err)
			}

			// The native strip must reach the production NCC threshold after it
			// is normalized to the same 960-wide HUD reference as its template.
			normalizedNative := match.ScaleGray(match.ToGray(native), template.Bounds().Dx(), template.Bounds().Dy())
			nativeImage := image.NewRGBA(normalizedNative.Bounds())
			draw.Draw(nativeImage, nativeImage.Bounds(), normalizedNative, normalizedNative.Bounds().Min, draw.Src)
			if score, err := (match.NCC{}).Match(match.Query{Image: nativeImage, Gray: normalizedNative, ROI: normalizedNative.Bounds(), Prepared: match.PrepareNCC(match.ToGray(template), nil)}); err != nil || score.Value < .80 {
				t.Fatalf("reviewed native strip no longer matches template: score=%+v err=%v", score, err)
			}

			for _, width := range []int{800, 960, 1280, 1308, 1920} {
				for _, left := range []bool{true, false} {
					t.Run(fmt.Sprintf("%d/left=%v", width, left), func(t *testing.T) {
						scale := float64(width) / 960
						part := match.ScaleGray(match.ToGray(template),
							int(math.Round(float64(template.Bounds().Dx())*scale)),
							int(math.Round(float64(template.Bounds().Dy())*scale)))
						first := image.Pt(int(93*scale), int(61*scale))
						if !left {
							first.X = int(835 * scale)
						}
						roi := NameRegion(first, scale, left)
						img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
						at := image.Pt(roi.Min.X+int(12*scale), roi.Min.Y+int(7*scale))
						box := image.Rectangle{Min: at, Max: at.Add(part.Bounds().Size())}
						draw.Draw(img, box, part, image.Point{}, draw.Src)

						reader := NewReader()
						if tc.name == ItachiHyakusen {
							// The native title fixture has no full-frame portrait evidence.
							// A title alone must not enable the energy-row offset.
							if got := reader.Read(img, roi, scale); got != (Readout{}) {
								t.Fatalf("title without portrait enabled special offset: %+v", got)
							}
							return
						}
						var tracker Tracker
						got := tracker.Read(reader, img, roi, scale, time.Unix(100, 0))
						wantOffset, wantSlots := float64(0), 0
						if tc.name == MinatoKyubi {
							// Ordinary four slots/palette; only the energy row moves.
							wantOffset, wantSlots = EnergyGaugeRowOffset, 4
						}
						if got.Name != tc.name || got.Slots != wantSlots || got.Palette != "" || got.RowOffsetY != wantOffset || got.Unverified {
							t.Fatalf("readout=%+v, want offset=%v slots=%d", got, wantOffset, wantSlots)
						}
						if ShortLabel(got.Name) == got.Name {
							t.Fatalf("missing concise label for %q", got.Name)
						}

						// Destroying half the current lettering must not keep the identity.
						// The reader sees nothing; the tracker may only keep a verified
						// four-slot row as an unverified geometry hint for at most 1s,
						// exactly like the other four-slot specials (Minato's row).
						draw.Draw(img, image.Rect(box.Min.X+box.Dx()/2, box.Min.Y, box.Max.X, box.Max.Y), image.Black, image.Point{}, draw.Src)
						if got := reader.Read(img, roi, scale); got != (Readout{}) {
							t.Fatalf("partial title was read: %+v", got)
						}
						want := Readout{}
						if wantSlots > 0 {
							want = Readout{Slots: wantSlots, RowOffsetY: wantOffset, Unverified: true}
						}
						got = tracker.Read(reader, img, roi, scale, time.Unix(100, 0).Add(16*time.Millisecond))
						if got != want {
							t.Fatalf("partial title retained %+v, want %+v", got, want)
						}
						if got := tracker.Read(reader, img, roi, scale, time.Unix(100, 0).Add(1100*time.Millisecond)); got != (Readout{}) {
							t.Fatalf("geometry hint outlived expiry: %+v", got)
						}
					})
				}
			}
		})
	}
}
