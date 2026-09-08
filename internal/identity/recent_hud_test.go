package identity

import (
	xdraw "golang.org/x/image/draw"
	"image"
	"image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"testing"
)

func TestRecentHUDAccountDoesNotDependOnNinjaName(t *testing.T) {
	root := findRepoRoot(t)
	t.Chdir(root)
	book := LoadBook(config.Default())
	for _, file := range []string{"duel-shino-right-account-20260907.png", "duel-shino-name-20260907.png", "duel-long-account-tayuya-20260907.png", "duel-shikamaru-name-20260907.png"} {
		img := mustPNG(t, "inbox/regressions/"+file)
		ca, _ := detect.ResolveContentArea(img, detect.ModeAuto, detect.LogicWidth, detect.LogicHeight, .015)
		left, right := bestHit(img, ca, FightROIs.Left, mineOf(book), 960), bestHit(img, ca, FightROIs.Right, mineOf(book), 960)
		want := "left"
		if file == "duel-shino-name-20260907.png" {
			want = "right"
		}
		got := ReadFight(img, book, detect.ModeAuto)
		t.Logf("%s raw left=%+v right=%+v result=%+v", file, left, right, got)
		if got.Side != want {
			t.Errorf("%s want %s: %+v", file, want, got)
		}
		for _, width := range []int{800, 960, 1280, 1600, 1920, 2560} {
			scaled := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
			xdraw.BiLinear.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Src, nil)
			if got := ReadFight(scaled, book, detect.ModeAuto); got.Side != want {
				t.Errorf("%s width%d: %+v want %s", file, width, got, want)
			}
		}
	}
}
