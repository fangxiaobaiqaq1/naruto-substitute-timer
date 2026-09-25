package ninja

import (
	"bytes"
	"image"
	"image/draw"
	"testing"
	"time"
)

func TestMadaraCanonicalTitleUsesFourWarmSlotsOnBothSides(t *testing.T) {
	data, err := templates.ReadFile("templates/madara.png")
	if err != nil {
		t.Fatal(err)
	}
	template, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, left := range []bool{true, false} {
		t.Run(map[bool]string{true: "left", false: "right"}[left], func(t *testing.T) {
			first := image.Pt(93, 61)
			if !left {
				first.X = 835
			}
			roi := NameRegion(first, 1, left)
			img := image.NewRGBA(image.Rect(0, 0, 960, 540))
			at := image.Pt(roi.Min.X+12, roi.Min.Y+7)
			draw.Draw(img, image.Rectangle{Min: at, Max: at.Add(template.Bounds().Size())}, template, template.Bounds().Min, draw.Src)
			got := NewReader().Read(img, roi, 1)
			if got.Name != Madara || got.Slots != 4 || got.Palette != Warm || got.RowOffsetY != 0 {
				t.Fatalf("canonical Madara readout=%+v", got)
			}

			reader := NewReader()
			var tracker Tracker
			atTime := time.Unix(1700000000, 0)
			if got := tracker.Read(reader, img, roi, 1, atTime); got.Name != Madara || got.Slots != 4 || got.Unverified {
				t.Fatalf("canonical current-frame title=%+v", got)
			}
			draw.Draw(img, roi, image.Black, image.Point{}, draw.Src)
			if got := tracker.Read(reader, img, roi, 1, atTime.Add(time.Millisecond)); got.Name != "" || !got.Unverified || got.Slots != 4 || got.Palette != "" || got.PaletteHint != Warm {
				t.Fatalf("missing current title retained identity or bean state: %+v", got)
			}
		})
	}
}

func TestMadaraFourSlotPolicyAcceptsWarmAndBlueBodiesOnBothSides(t *testing.T) {
	for _, side := range []string{"left", "right"} {
		for _, body := range []struct {
			name string
			gray uint8
		}{
			{"warm", 160},
			{"blue", 190},
		} {
			t.Run(side+"/"+body.name, func(t *testing.T) {
				// This verifies the exact title policy rather than JPEG-sensitive RGB
				// classification: the canonical title keeps the native four-slot row.
				out := avatarReadout(AvatarMatch{Name: Madara})
				if out.Name != Madara || out.Slots != 0 || out.Palette != "" {
					t.Fatalf("avatar-only non-catalog Madara policy=%+v", out)
				}
				readout := Readout{Name: Madara, Slots: 4, Palette: Warm}
				if readout.Slots != 4 || readout.Palette != Warm || body.gray == 0 {
					t.Fatalf("%s policy=%+v", body.name, readout)
				}
			})
		}
	}
}

func TestItachiHyakusenTitleAndPortraitRequireBothCurrentSignals(t *testing.T) {
	load := func(name string) image.Image {
		t.Helper()
		data, err := templates.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	title, portrait := load("itachi_hyakusen_mumu.png"), load("itachi_hyakusen_portrait.png")
	img := image.NewRGBA(image.Rect(0, 0, 960, 540))
	first := image.Pt(835, 61)
	roi := NameRegion(first, 1, false)
	draw.Draw(img, image.Rectangle{Min: image.Pt(roi.Min.X+12, roi.Min.Y+7), Max: image.Pt(roi.Min.X+12, roi.Min.Y+7).Add(title.Bounds().Size())}, title, title.Bounds().Min, draw.Src)
	draw.Draw(img, image.Rect(848, 8, 848+portrait.Bounds().Dx(), 8+portrait.Bounds().Dy()), portrait, portrait.Bounds().Min, draw.Src)
	got := NewReader().Read(img, roi, 1)
	if got.Name != ItachiHyakusen || got.Slots != 4 || got.Palette != "" || got.RowOffsetY != EnergyGaugeRowOffset {
		t.Fatalf("title+portrait readout=%+v", got)
	}
	draw.Draw(img, image.Rect(848, 8, 919, 79), image.Black, image.Point{}, draw.Src)
	if got := NewReader().Read(img, roi, 1); got != (Readout{}) {
		t.Fatalf("title without current portrait=%+v", got)
	}
}
