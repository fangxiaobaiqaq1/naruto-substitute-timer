package factory

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
)

// Local user evidence is not redistributed. Run with the twelve 图片_*.png
// captures in the repository root; missing captures skip only their own case.
// 1280-wide copies also cover Android's initial MediaProjection capture size.
func TestUserResolutionSamples(t *testing.T) {
	for _, tc := range []struct {
		file, profile string
		left, right   int
	}{
		{"图片_001_01 (1).png", "duel", 2, 4},
		{"图片_002_01.png", "duel", 2, 2},
		{"图片_003_01.png", "duel", 2, 2},
		{"图片_004_01.png", "duel", 2, 2},
		{"图片_005_01.png", "duel", 2, 2},
		{"图片_006_01.png", "duel", 2, 2},
		{"图片_007_01.png", "duel", 1, 4},
		{"图片_008_01.png", "duel", 2, 2},
		{"图片_009_01.png", "duel", 1, 4},
		{"图片_010_01.png", "duel", 2, 4},
		{"图片_011_01.png", "duel", 2, 4},
		{"图片_013_01.png", "camp", 4, 2},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open(filepath.Join("../../..", tc.file))
			if os.IsNotExist(err) {
				t.Skip("local resolution sample unavailable")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			src, err := png.Decode(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, width := range []int{src.Bounds().Dx(), 1280} {
				for _, mode := range []string{"stretch", "auto"} {
					t.Run(fmt.Sprintf("%d/%s", width, mode), func(t *testing.T) {
						height := width * src.Bounds().Dy() / src.Bounds().Dx()
						img := image.NewRGBA(image.Rect(0, 0, width, height))
						xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
						cfg := config.Default()
						cfg.Layout.ContentMode = mode
						e, err := New(FromApp(cfg))
						if err != nil {
							t.Fatal(err)
						}
						r := e.Analyze(img)
						var left, right, known int
						for _, b := range r.Beads {
							if b.Unknown {
								continue
							}
							known++
							if b.Lit && b.Label[0] == 'L' {
								left++
							}
							if b.Lit && b.Label[0] == 'R' {
								right++
							}
						}
						if !r.Fighting || r.Uncertain || r.LayoutProfile != tc.profile || known != 8 || left != tc.left || right != tc.right {
							t.Fatalf("fight=%v uncertain=%v profile=%s known=%d beans=%d/%d; want %s 8 %d/%d; %+v", r.Fighting, r.Uncertain, r.LayoutProfile, known, left, right, tc.profile, tc.left, tc.right, r.Beads)
						}
					})
				}
			}
		})
	}
}
