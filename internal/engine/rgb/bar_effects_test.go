package rgb

import (
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/ninja"
	"testing"
)

// Original user crop, not a full 889px-wide game frame. The 15px slot pitch
// gives a 960-wide HUD reference; centers below are local crop annotations.
func TestUserBothNarutoBarEffectsCrop(t *testing.T) {
	img := loadRGBA(t, "../../../inbox/regressions/both-naruto-bar-effects-crop-20260907.png")
	var positions []detect.BeadPosition
	for _, side := range []struct {
		name    string
		x, step int
	}{{"left", 82, 15}, {"right", 819, -15}} {
		for i := 0; i < 4; i++ {
			positions = append(positions, detect.BeadPosition{Bead: detect.Bead{Side: side.name, Idx: i, LX: float64((side.x + i*side.step) * 2), LY: 116}, X: side.x + i*side.step, Y: 58})
		}
	}
	e := &Engine{names: ninja.NewReader()}
	area := detect.ContentArea{W: 960, H: 540}
	pos, names := e.specialPositions(img, positions, area)
	beads := sampleCalibratedSpecial(img, pos, area, config.Default().Vision, names)
	l, r := countReady(beads)
	if names[0].Name != ninja.Naruto || names[1].Name != ninja.Naruto || l != 4 || r != 2 || knownCount(beads) != 8 {
		t.Fatalf("names=%+v counts=%d/%d beads=%+v", names, l, r, beads)
	}
}
