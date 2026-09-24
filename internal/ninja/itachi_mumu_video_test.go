package ninja

import (
	"image"
	"image/color"
	"image/draw"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in regression for the supplied MuMu recordings. It checks that the
// current pixels (not a retained tracker identity) match the video-specific
// full-title template at the actual right HUD anchor.
func TestItachiHyakusenMuMuVideoTitles(t *testing.T) {
	root := os.Getenv("NARUTO_VIDEO_FRAMES")
	if root == "" {
		t.Skip("set NARUTO_VIDEO_FRAMES to extracted supplied-video PNGs")
	}
	for _, tc := range []struct{ dir, frame string }{
		{"2026-09-23 16-33-17", "frame-03.png"},
		{"2026-09-23 16-33-25", "frame-15.png"},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			f, err := os.Open(filepath.Join(root, tc.dir, tc.frame))
			if err != nil {
				t.Fatal(err)
			}
			src, _, err := image.Decode(f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			img := image.NewRGBA(src.Bounds())
			draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
			scale := float64(img.Bounds().Dx()) / 960
			first := image.Pt(int(835*scale), int(61*scale))
			var tracker Tracker
			got := tracker.Read(NewReader(), img, NameRegion(first, scale, false), scale, time.Unix(1700000000, 0))
			if got.Name != ItachiHyakusen || got.Slots != 0 || got.Palette != "" || got.RowOffsetY != EnergyGaugeRowOffset || got.Unverified {
				t.Fatalf("current video title=%+v", got)
			}
			// A title alone is not enough for the MuMu path: erase the separate
			// character portrait ROI and require a true current-frame unknown.
			for y := int(7.5 * scale); y < int(78.5*scale); y++ {
				for x := int(847.5 * scale); x < int(918.75*scale); x++ {
					img.SetRGBA(x, y, color.RGBA{A: 255})
				}
			}
			var withoutPortrait Tracker
			if got := withoutPortrait.Read(NewReader(), img, NameRegion(first, scale, false), scale, time.Unix(1700000000, 0)); got != (Readout{}) {
				t.Fatalf("title without independent portrait enabled special offset: %+v", got)
			}
		})
	}
}
