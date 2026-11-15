package factory

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"os"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

// Received JPEG is 2000x1250 (the upload was labelled 2560x1600). Larger
// widths below are explicit resampling, not additional native captures.
func TestTsunadeYokaiLetterboxedCamp(t *testing.T) {
	f, err := os.Open("testdata/camp-tsunade-yokai.jpg")
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{960, 1280, 2000, 2560} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, width, width*5/8))
			xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
			cfg := config.Default()
			if cfg.Layout.ContentMode != "auto" {
				t.Fatal("default must use pixel-backed content detection")
			}
			layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
			area, ok := layout.ContentArea(img)
			if !ok || area.Y == 0 || area.H != width*9/16 {
				t.Fatalf("actual black bars ignored: %+v", area)
			}
			base := layout.PositionsInImage("camp", img, area)
			e, err := New(FromApp(cfg))
			if err != nil {
				t.Fatal(err)
			}
			check := func(got engine.Result, rightReady int) {
				t.Helper()
				if !got.Fighting || got.Uncertain || got.LayoutProfile != "camp" || got.LeftSlots != 4 || got.RightSlots != 4 ||
					got.LeftNinja != "药师兜[侠隐江湖]" || got.RightNinja != ninja.TsunadeYokai || len(got.Beads) != 8 {
					t.Fatalf("gate/identity/topology: %+v", got)
				}
				for i, b := range got.Beads {
					wantLit := i < 4 || i-4 < rightReady
					if b.Unknown || (b.Lit || b.Gold) != wantLit {
						t.Fatalf("want left4/right%d, got %+v", rightReady, got.Beads)
					}
					wantY := base[i].Y
					if i >= 4 {
						wantY = area.MapHUDPixel(base[i].LX, base[i].LY+9*detect.HUDReferenceHeight/540,
							"right", detect.HUDReferenceWidth, detect.HUDReferenceHeight).Y
					}
					if b.Y != wantY {
						t.Fatalf("%s sampled at y=%d, want %d (only Tsunade's row moves)", b.Label, b.Y, wantY)
					}
				}
			}
			var got engine.Result
			for _, ms := range []int{0, 20, 600} {
				got = e.(engine.TimedEngine).AnalyzeAt(img, time.Unix(100, 0).Add(time.Duration(ms)*time.Millisecond))
				check(got, 4)
			}
			// Saving the automatically suggested boxes must not double-apply
			// the black-bar origin or the energy-gauge row offset.
			cfg.Layout.Calibration = &config.RegionCalibration{Profile: "camp",
				LeftBeads: &got.Geometry[0].BeadBox, RightBeads: &got.Geometry[1].BeadBox,
				LeftAvatar: &got.Geometry[0].AvatarBox, RightAvatar: &got.Geometry[1].AvatarBox}
			calibrated, err := New(FromApp(cfg))
			if err != nil {
				t.Fatal(err)
			}
			check(calibrated.Analyze(img), 4)

			// Synthetic empty cores prove the red gauge is not counted as
			// available beans. This is not a measured gameplay/drop sequence.
			for ready := 3; ready >= 0; ready-- {
				p := got.Geometry[1].Beads[ready]
				sx, sy := max(2, width*4/960), max(2, width*5/960)
				draw.Draw(img, image.Rect(p.X-sx, p.Y-sy, p.X+sx+1, p.Y+sy+1),
					image.NewUniform(color.RGBA{28, 54, 98, 255}), image.Point{}, draw.Src)
				check(e.(engine.TimedEngine).AnalyzeAt(img, time.Unix(101, 0).Add(time.Duration(4-ready)*100*time.Millisecond)), ready)
			}
			// A red gauge alone must not move an unidentified row or become
			// four available beans; no cached portrait survives this fresh engine.
			draw.Draw(img, got.Geometry[1].NameROI, image.Black, image.Point{}, draw.Src)
			draw.Draw(img, got.Geometry[1].AvatarROI, image.Black, image.Point{}, draw.Src)
			fresh, err := New(FromApp(config.Default()))
			if err != nil {
				t.Fatal(err)
			}
			unknown := fresh.Analyze(img)
			if unknown.RightNinja != "" || len(unknown.Beads) != 8 {
				t.Fatalf("missing identity was guessed: %+v", unknown)
			}
			for i, b := range unknown.Beads[4:] {
				if b.Y != base[i+4].Y || !b.Unknown {
					t.Fatalf("unidentified gauge moved/read as a bean: %+v", b)
				}
			}
		})
	}
}
