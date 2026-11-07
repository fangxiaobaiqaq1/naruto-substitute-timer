package scene

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"os"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/engine"
)

func TestVSDeathSwapTemplateIsHoldOnly(t *testing.T) {
	c, err := Load(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range c.templates {
		if tpl.spec.ID == "vs-death-swap" {
			if tpl.spec.Scene != "vs" || tpl.spec.RoundOpening || tpl.spec.ContinuationOnly || tpl.spec.Threshold < .88 {
				t.Fatalf("swap logo must only establish a hold: %+v", tpl.spec)
			}
			return
		}
	}
	t.Fatal("missing swap-logo template")
}

func TestVSOpeningAndSwapScreenshots(t *testing.T) {
	for _, name := range []string{"opening", "swap"} {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open("../../inbox/regressions/vs-account-side-20261107/" + name + ".png")
			if os.IsNotExist(err) {
				t.Skip("private user screenshot not distributed")
			}
			if err != nil {
				t.Fatal(err)
			}
			src, _, err := image.Decode(f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			for _, width := range []int{800, 960, 1268, 1600, 1920, 2560} {
				t.Run(fmt.Sprint(width), func(t *testing.T) {
					height := int(math.Round(float64(width) * float64(src.Bounds().Dy()) / float64(src.Bounds().Dx())))
					img := image.NewRGBA(image.Rect(0, 0, width, height))
					xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
					c, err := Load(config.Default())
					if err != nil {
						t.Fatal(err)
					}
					got := c.DecideAt(img, time.Unix(100, 0))
					if got.Kind != engine.GateUncertain || got.SceneID != "vs" || got.RoundOpening {
						t.Fatalf("want VS hold without round opening: %+v", got)
					}
				})
			}
		})
	}
}
