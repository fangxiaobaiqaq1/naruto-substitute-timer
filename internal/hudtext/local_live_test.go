package hudtext_test

import (
	"image"
	"image/draw"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
)

func TestLocalOCRDuelNamesWithoutNameTemplates(t *testing.T) {
	dir := os.Getenv("TIMER_TEST_REVIEW_FRAMES")
	if dir == "" {
		t.Skip("set TIMER_TEST_REVIEW_FRAMES to the user's local regression directory")
	}
	for _, tc := range []struct{ file, name string }{{"1.png", "宇智波带土"}, {"2.png", "神秘面具男"}, {"3.png", "神秘面具男"}} {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open(filepath.Join(dir, tc.file))
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
			cfg.UI.AutoTextRecognition = true
			live, err := factory.NewLive(factory.FromApp(cfg))
			if err != nil {
				t.Fatal(err)
			}
			defer live.Close()
			start := time.Now()
			confirmed := false
			for time.Since(start) < 6*time.Second {
				got := live.AnalyzeAt(img, time.Now())
				if got.RightNinja != "" {
					if got.RightNinja != tc.name {
						t.Fatalf("wrong identity: %+v", got)
					}
					confirmed = true
					break
				}
				if got.TextError != "" {
					t.Log(got.TextError)
				}
				time.Sleep(30 * time.Millisecond)
			}
			if !confirmed {
				t.Fatalf("%s never confirmed", tc.name)
			}
			// Occlude the entire right title without erasing the bean row.
			blank := image.NewRGBA(img.Bounds())
			draw.Draw(blank, blank.Bounds(), img, img.Bounds().Min, draw.Src)
			r := image.Rect(img.Bounds().Dx()/2, 0, img.Bounds().Dx(), img.Bounds().Dy()*8/100)
			draw.Draw(blank, r, image.Black, image.Point{}, draw.Src)
			if got := live.AnalyzeAt(blank, time.Now()); got.RightNinja != "" {
				t.Fatalf("old identity survived occlusion: %s", got.RightNinja)
			}
		})
	}
}

func TestNativeTrainingScreenshots(t *testing.T) {
	dir := os.Getenv("TIMER_TEST_REVIEW_FRAMES")
	if dir == "" {
		t.Skip("set TIMER_TEST_REVIEW_FRAMES to the user's local regression directory")
	}
	for _, tc := range []struct {
		file         string
		ready, slots int
	}{
		{"4.png", 2, 4}, {"5.png", 4, 4}, {"6.png", 6, 6}, {"7.png", 2, 6},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open(filepath.Join(dir, tc.file))
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
			// Native SDK frames have no emulator title bar. Use the production
			// gate/layout and embedded resources without adding a crop or offset.
			eng, err := factory.New(factory.FromApp(config.Default()))
			if err != nil {
				t.Fatal(err)
			}
			got := eng.Analyze(img)
			if !got.Fighting || got.Uncertain || got.LayoutProfile != "camp" || got.LeftSlots != 4 || got.RightSlots != tc.slots {
				t.Fatalf("invalid training layout: %+v", got)
			}
			left, right, count := 0, 0, 0
			for _, bean := range got.Beads {
				if bean.Unknown {
					t.Fatalf("unread native bean: %+v", bean)
				}
				count++
				if bean.Lit {
					if bean.Label[0] == 'L' {
						left++
					} else {
						right++
					}
				}
			}
			if count != 4+tc.slots || left != 4 || right != tc.ready {
				t.Fatalf("got L%d/R%d (%d slots), want L4/R%d (%d slots)", left, right, count, tc.ready, 4+tc.slots)
			}
		})
	}
}
