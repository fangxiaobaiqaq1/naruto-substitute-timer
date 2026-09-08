package ninja_test

import (
	"image"
	"image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/ninja"
	"os"
	"testing"
)

func TestStudentUserScreenshot(t *testing.T) {
	path := os.Getenv("TIMER_TEST_NARUTO_STUDENT_IMAGE")
	if path == "" {
		t.Skip("set user screenshot path for integration")
	}
	f, err := os.Open(path)
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
	cfg := config.Default()
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	got := eng.Analyze(img)
	if !got.Fighting || got.Uncertain || got.LayoutProfile != "duel" || got.LeftNinja != ninja.NarutoStudent || got.LeftSlots != 4 || got.RightSlots != 4 {
		t.Fatalf("result: %+v", got)
	}
	var left, right int
	for _, b := range got.Beads {
		if b.Unknown {
			t.Fatalf("unknown bead: %+v", b)
		}
		if b.Lit {
			if b.Label[0] == 'L' {
				left++
			} else {
				right++
			}
		}
	}
	if left != 4 || right != 3 {
		t.Fatalf("beads left=%d right=%d", left, right)
	}
	t.Logf("scene=%s ninja=%s left=%d right=%d", got.Scene, got.LeftNinja, left, right)
}
