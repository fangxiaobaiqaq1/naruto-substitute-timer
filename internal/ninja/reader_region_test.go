package ninja

import (
	"image"
	"testing"
)

func TestNameRegionIncludesCompactTopAlignedTitle(t *testing.T) {
	// The reviewed Edo title starts at y=2 in a 1280x721 native duel capture.
	// Its normalised first right-bead center is (1138, 83); the region must keep
	// that title wholly searchable without changing any bead coordinate.
	first := image.Pt(1138, 83)
	roi := NameRegion(first, 1280.0/960, false)
	if roi.Min.Y > 2 || roi.Max.Y < 31 {
		t.Fatalf("compact top title fell outside name region: %v", roi)
	}
}
