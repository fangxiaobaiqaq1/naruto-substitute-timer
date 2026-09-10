package factory

import (
	"fmt"
	xdraw "golang.org/x/image/draw"
	"image"
	"image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/ninja"
	"os"
	"testing"
)

func TestExpandedTrainingToolbarFlowsThroughStandaloneGateAndBeans(t *testing.T) {
	const relative = "regressions/camp-expanded-toolbar-20260910.png"
	if _, err := os.Stat("../../../inbox/" + relative); os.IsNotExist(err) {
		t.Skip("local user screenshot is not distributed")
	}
	source := stabilityImage(t, relative)
	t.Chdir(t.TempDir())
	for _, width := range []int{1198, 950, 960, 1280, 1600, 1920, 2560} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			img := source
			if width != source.Bounds().Dx() {
				img = image.NewRGBA(image.Rect(0, 0, width, width*9/16))
				xdraw.BiLinear.Scale(img, img.Bounds(), source, source.Bounds(), draw.Src, nil)
			}
			e, err := New(FromApp(config.Default()))
			if err != nil {
				t.Fatal(err)
			}
			got := e.Analyze(img)
			if !got.Fighting || got.Uncertain || got.Scene != "fight" || got.LayoutProfile != "camp" {
				t.Fatalf("gate rejected training screenshot: %+v", got)
			}
			if got.RightNinja != ninja.Madara || got.LeftSlots != 4 || got.RightSlots != 6 {
				t.Fatalf("lost identity/topology: %+v", got)
			}
			if len(got.Beads) != 10 {
				t.Fatalf("wrong bean count: %+v", got)
			}
			for _, b := range got.Beads {
				if b.Unknown || !b.Lit {
					t.Fatalf("full bean rejected at %s: %+v", b.Label, b)
				}
			}
		})
	}
}
