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

func TestSasukeXiayinNameAndBoundedGeometryHint(t *testing.T) {
	for _, file := range []string{"sasuke_xiayin.png", "sasuke_xiayin_duel.png"} {
		t.Run(file, func(t *testing.T) { testSasukeNameAndHint(t, file) })
	}
	if ShortLabel(SasukeXiayin) != "佐助·侠隐江湖" || DualCooldown(SasukeXiayin) {
		t.Fatal("display label or ordinary cooldown changed")
	}
}

func testSasukeNameAndHint(t *testing.T, file string) {
	t.Helper()
	data, err := templates.ReadFile("templates/" + file)
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{800, 822, 960, 1308, 1920, 2560} {
		for _, left := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d/left=%v", width, left), func(t *testing.T) {
				scale := float64(width) / 960
				gray := match.ToGray(src)
				part := match.ScaleGray(gray, int(math.Round(float64(gray.Bounds().Dx())*scale)), int(math.Round(float64(gray.Bounds().Dy())*scale)))
				first := image.Pt(int(93*scale), int(61*scale))
				if !left {
					first.X = int(835 * scale)
				}
				roi := NameRegion(first, scale, left)
				img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
				point := image.Pt(roi.Min.X+int(12*scale), roi.Min.Y+int(7*scale))
				box := image.Rectangle{Min: point, Max: point.Add(part.Bounds().Size())}
				draw.Draw(img, box, part, image.Point{}, draw.Src)
				r := NewReader()
				var tracker Tracker
				at := time.Unix(100, 0)
				got := tracker.Read(r, img, roi, scale, at)
				if got.Name != SasukeXiayin || got.Slots != 4 || got.Palette != Purple || got.RowOffsetY != 9 {
					t.Fatalf("full version: %+v", got)
				}
				// Half a name cannot identify this variant or shift a new row.
				draw.Draw(img, image.Rect(box.Min.X+box.Dx()/2, box.Min.Y, box.Max.X, box.Max.Y), image.Black, image.Point{}, draw.Src)
				if got := r.Read(img, roi, scale); got != (Readout{}) {
					t.Fatalf("partial version supplied special rules: %+v", got)
				}
				got = tracker.Read(r, img, roi, scale, at.Add(16*time.Millisecond))
				if got.Name != "" || got.Palette != "" || got.Score != 0 || !got.Unverified || got.Slots != 4 || got.RowOffsetY != 9 || got.PaletteHint != Purple {
					t.Fatalf("brief gap must retain geometry only: %+v", got)
				}
				for _, tc := range []struct {
					label string
					roi   image.Rectangle
					at    time.Time
				}{{"expired", roi, at.Add(1100 * time.Millisecond)}, {"geometry", roi.Add(image.Pt(0, 1)), at.Add(20 * time.Millisecond)}, {"seek", roi, at.Add(-time.Millisecond)}} {
					t.Run(tc.label, func(t *testing.T) {
						// Establish a fresh hint, then independently exercise invalidation.
						draw.Draw(img, box, part, image.Point{}, draw.Src)
						var tr Tracker
						tr.Read(r, img, roi, scale, at)
						draw.Draw(img, box, image.Black, image.Point{}, draw.Src)
						if got := tr.Read(r, img, tc.roi, scale, tc.at); got != (Readout{}) {
							t.Fatalf("stale geometry: %+v", got)
						}
					})
				}
			})
		}
	}
}
