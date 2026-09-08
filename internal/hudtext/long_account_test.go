package hudtext

import (
	"image"
	"narutotimer/internal/config"
	"testing"
)

// Tests ROI coverage, NOT successful OCR. Local Windows OCR still misreads
// the recorded right-hand glyphs despite the entire title being in the scan.
func TestLongAccountWholeTitlesAreInsideScan(t *testing.T) {
	img := realFrame(t, "../../inbox/regressions/duel-long-account-tayuya-20260907.png")
	regions, ok := nameRegions(img, config.Default().Layout, "duel")
	if !ok {
		t.Fatal("valid game frame rejected")
	}
	for i, r := range []image.Rectangle{image.Rect(107, 20, 216, 38), image.Rect(608, 20, 848, 38)} {
		if r.Intersect(regions[i]) != r {
			t.Fatalf("side%d title truncated: title=%v scan=%v", i, r, regions[i])
		}
	}
}
