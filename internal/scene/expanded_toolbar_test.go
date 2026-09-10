package scene

import (
	"bytes"
	xdraw "golang.org/x/image/draw"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"narutotimer/assets"
	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
	"testing"
)

// Reuse the published key label in a controlled toolbar at its new position
// and size. This regression does not require distributing a user's screenshot.
func TestExpandedToolbarUsesEmbeddedScaledControlWithinBoundedROI(t *testing.T) {
	t.Chdir(t.TempDir())
	cat, err := Load(config.Default())
	if err != nil || cat == nil {
		t.Fatalf("load: %v", err)
	}
	data, err := assets.Templates.ReadFile("templates/raw_camp_key_text.png")
	if err != nil {
		t.Fatal(err)
	}
	label, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	control := match.ScaleGray(match.ToGray(label), 70, 32)
	for _, x := range []int{986, 400} {
		native := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
		draw.Draw(native, native.Bounds(), image.NewUniform(color.RGBA{48, 61, 70, 255}), image.Point{}, draw.Src)
		draw.Draw(native, image.Rect(x, 82, x+70, 114), control, image.Point{}, draw.Src)
		for _, width := range []int{800, 960, 1198, 1600, 1920, 2560} {
			img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
			xdraw.BiLinear.Scale(img, img.Bounds(), native, native.Bounds(), draw.Src, nil)
			got := cat.Decide(img)
			if x == 986 && (got.Kind != engine.GateFight || got.LayoutProfile != "camp") {
				t.Fatalf("%dpx toolbar missed: %+v", width, got)
			}
			if x == 400 && got.Kind == engine.GateFight {
				t.Fatalf("%dpx unrelated key text established a fight", width)
			}
		}
	}
}
