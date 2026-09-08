package ninja

import (
	"image"
	"image/draw"
	"narutotimer/internal/match"
	"os"
	"testing"
	"time"
)

func TestOrdinaryNameSearchMovesWithTitleNotAccountLength(t *testing.T) {
	f, err := os.Open("../../inbox/regressions/duel-shino-name-20260907.png")
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		rect image.Rectangle
		name string
	}{
		{image.Rect(104, 18, 174, 40), "油女志乃"},
		{image.Rect(733, 18, 785, 40), "春野樱"},
	} {
		// Explicit fixture-only reader: production no longer registers these
		// per-character ordinary templates. Keep testing display-only hints.
		cropped := image.NewRGBA(image.Rect(0, 0, tc.rect.Dx(), tc.rect.Dy()))
		draw.Draw(cropped, cropped.Bounds(), src, tc.rect.Min, draw.Src)
		g := match.ToGray(cropped)
		r := &Reader{source: []nameTemplate{{readout: Readout{Name: tc.name}, gray: match.ScaleGray(g, int(float64(g.Bounds().Dx())*960/950+.5), int(float64(g.Bounds().Dy())*960/950+.5))}}}
		// Move only the actual lettering, not a full fixed-position HUD crop.
		// Both left- and right-side strips use the same left-to-right glyphs.
		for _, left := range []bool{true, false} {
			first := image.Pt(106, 61)
			if !left {
				first.X = 844
			}
			roi := NameRegion(first, 950.0/960, left)
			for _, offset := range []int{8, 90, 190} {
				frame := image.NewRGBA(image.Rect(0, 0, 950, 534))
				dst := image.Rectangle{Min: image.Pt(roi.Min.X+offset, 18), Max: image.Pt(roi.Min.X+offset, 18).Add(tc.rect.Size())}
				draw.Draw(frame, dst, src, tc.rect.Min, draw.Src)
				var tracker Tracker
				at := time.Unix(100, 0)
				got := tracker.Read(r, frame, roi, 950.0/960, at)
				if got.Name != tc.name || got.Slots != 0 || got.Palette != "" || got.Unverified {
					t.Fatalf("left=%v offset=%d: %+v want %s display only", left, offset, got, tc.name)
				}
				blank := image.NewRGBA(frame.Bounds())
				if got := tracker.Read(r, blank, roi, 950.0/960, at.Add(20*time.Millisecond)); got != (Readout{}) {
					t.Fatalf("display-only name gap created bean rule: %+v", got)
				}
				if got := tracker.Read(r, frame, roi, 950.0/960, at.Add(40*time.Millisecond)); got.Name != tc.name {
					t.Fatalf("name recovery delayed: %+v", got)
				}
				// A truncated glyph sequence must not inherit the full name.
				draw.Draw(frame, image.Rect(dst.Min.X+dst.Dx()/2, dst.Min.Y, dst.Max.X, dst.Max.Y), image.Black, image.Point{}, draw.Src)
				if got := r.Read(frame, roi, 950.0/960); got.Name != "" {
					t.Fatalf("partial name accepted: %+v", got)
				}
			}
		}
	}
}
