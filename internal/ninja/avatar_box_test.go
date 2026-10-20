package ninja

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

// A box drawn exactly around the portrait implied by an automatic window
// converts back to that window and scale.
func TestAvatarRegionFromBoxInvertsAutomaticWindow(t *testing.T) {
	for _, scale := range []float64{1280.0 / 960, 1.5, 2, 3} {
		for _, left := range []bool{true, false} {
			first := image.Pt(int(400*scale), int(62*scale))
			want := AvatarRegion(first, scale, left)
			cx := avatarBoxLeftX
			if !left {
				cx = avatarBoxRightX
			}
			side := avatarBoxSide * scale
			x := float64(want.Min.X) + cx*scale - side/2
			y := float64(want.Min.Y) + avatarBoxCentreY*scale - side/2
			got, gotScale := AvatarRegionFromBox(x, y, side, side, left)
			d := got.Min.Sub(want.Min)
			if math.Abs(gotScale-scale) > 1e-9 || d.X < -1 || d.X > 1 || d.Y < -1 || d.Y > 1 || got.Dx()-want.Dx() > 1 || want.Dx()-got.Dx() > 1 {
				t.Fatalf("scale=%v left=%v: %v@%v, want %v@%v", scale, left, got, gotScale, want, scale)
			}
		}
	}
}

// Visible portrait boxes measured by hand on native captures (private
// screenshots, skipped when absent): the converted window/scale stays within
// 3 reference units / 6% of the automatic ones and identifies the same
// portrait. These measurements produced the avatarBox* constants.
func TestAvatarBoxMatchesAutomaticWindowOnRealFrames(t *testing.T) {
	boxes := []struct {
		file, profile string
		left          bool
		box           detect.PixelBox
	}{
		{"/tmp/ntlab/s1/frame-000001.png", "duel", true, detect.PixelBox{X: 60, Y: 29, W: 140, H: 139}},
		{"/tmp/ntlab/s1/frame-000001.png", "duel", false, detect.PixelBox{X: 1719, Y: 27, W: 144, H: 143}},
		{"../../图片_002_01.png", "duel", false, detect.PixelBox{X: 1598, Y: 28, W: 130, H: 128}},
		{"../../图片_005_01.png", "duel", true, detect.PixelBox{X: 97, Y: 50, W: 202, H: 198}},
		{"../../图片_006_01.png", "duel", true, detect.PixelBox{X: 163, Y: 33, W: 137, H: 133}},
		{"../../图片_008_01.png", "duel", true, detect.PixelBox{X: 52.5, Y: 26, W: 114.2, H: 113}},
		{"../../图片_009_01.png", "duel", true, detect.PixelBox{X: 61, Y: 30.4, W: 139, H: 137.2}},
		{"../../图片_009_01.png", "duel", false, detect.PixelBox{X: 1719, Y: 30, W: 141, H: 138}},
		{"../../图片_011_01.png", "duel", false, detect.PixelBox{X: 2101, Y: 30, W: 135, H: 137}},
		{"../../图片_001_01 (1).png", "duel", false, detect.PixelBox{X: 1146.4, Y: 23.4, W: 91.5, H: 86.6}},
		{"../../图片_003_01.png", "duel", false, detect.PixelBox{X: 1358.3, Y: 24.7, W: 108.4, H: 107}},
		{"../../图片_013_01.png", "camp", true, detect.PixelBox{X: 123.75, Y: 29, W: 135, H: 132}},
		{"../../图片_013_01.png", "camp", false, detect.PixelBox{X: 2143.75, Y: 28, W: 135, H: 134}},
	}
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	layout := engine.NewConfiguredLayout(detect.ModeAuto, config.Default().Layout)
	checked := 0
	for _, b := range boxes {
		if _, err := os.Stat(b.file); err != nil {
			continue
		}
		img := decodeFrame(t, b.file)
		area, _ := layout.ContentArea(img)
		positions := layout.PositionsInImage(b.profile, img, area)
		first := positions[0]
		if !b.left {
			first = positions[len(positions)/2]
		}
		scale := area.HUDScale(960, 540)
		auto := AvatarRegion(image.Pt(first.X, first.Y), scale, b.left)
		roi, boxScale := AvatarRegionFromBox(b.box.X, b.box.Y, b.box.W, b.box.H, b.left)
		d := roi.Min.Sub(auto.Min)
		if math.Abs(boxScale/scale-1) > .06 || math.Abs(float64(d.X))/scale > 3 || math.Abs(float64(d.Y))/scale > 3 {
			t.Fatalf("%s left=%v: box window %v@%.4f vs automatic %v@%.4f", filepath.Base(b.file), b.left, roi, boxScale, auto, scale)
		}
		if got, want := c.Match(img, roi, boxScale).Name, c.Match(img, auto, scale).Name; got != want {
			t.Fatalf("%s left=%v: box identifies %q, automatic %q", filepath.Base(b.file), b.left, got, want)
		}
		checked++
	}
	if checked == 0 {
		t.Skip("native captures are not distributed")
	}
}

// On a calibrated right side 百战鼬's portrait check follows the avatar window
// (here a HUD moved away from the 16:9 position); other sides keep the fixed
// frame-origin rect.
func TestCalibratedItachiPortraitFollowsAvatarWindow(t *testing.T) {
	const scale = 1.5
	base, roi := perfItachiFrame(t, scale)
	shift := image.Pt(-120, 90)
	img := image.NewRGBA(base.Bounds())
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{31, 40, 52, 255}), image.Point{}, draw.Src)
	draw.Draw(img, base.Bounds().Add(shift), base, image.Point{}, draw.Src)
	roi = roi.Add(shift)
	// The camp right window of this HUD (bead 0 at 835,61 @960).
	window := AvatarRegion(image.Pt(int(835*scale+.5), int(61*scale+.5)), scale, false).Add(shift)
	r := NewReader()
	now := time.Unix(1700000000, 0)
	var fixed Tracker
	if got := fixed.Read(r, img, roi, scale, now); got.Name == ItachiHyakusen {
		t.Fatal("the frame-origin rect matched a moved portrait")
	}
	var calibrated Tracker
	got := calibrated.ReadCalibrated(r, &AvatarTracker{}, img, roi, window, scale, scale, true, now)
	if got.TitleName != ItachiHyakusen || got.Name != ItachiHyakusen || got.RowOffsetY != EnergyGaugeRowOffset {
		t.Fatalf("calibrated right side: %+v", got)
	}
	// The fast path re-checks the same anchored portrait on the next frame.
	if got := calibrated.ReadCalibrated(r, &AvatarTracker{}, img, roi, window, scale, scale, true, now.Add(16*time.Millisecond)); got.TitleName != ItachiHyakusen {
		t.Fatalf("hint recheck lost the portrait: %+v", got)
	}
	var left Tracker
	if got := left.ReadCalibrated(r, &AvatarTracker{}, img, roi, window, scale, scale, false, now); got.TitleName == ItachiHyakusen {
		t.Fatalf("left side used the right portrait anchor: %+v", got)
	}
	// A window far from the portrait is not evidence.
	var far Tracker
	if got := far.ReadCalibrated(r, &AvatarTracker{}, img, roi, window.Add(image.Pt(40, 0)), scale, scale, true, now); got.TitleName == ItachiHyakusen {
		t.Fatalf("misplaced window accepted: %+v", got)
	}
}

func decodeFrame(t *testing.T, path string) *image.RGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img
}
