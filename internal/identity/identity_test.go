package identity

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"testing"

	"narutotimer/internal/detect"
	"narutotimer/internal/match"
)

func TestGuessPicksRightOnRealVS(t *testing.T) {
	root := findRepoRoot(t)
	img := mustPNG(t, filepath.Join(root, "inbox", "reject", "忍者加载.png"))
	name := mustPNG(t, filepath.Join(root, "assets", "identity", "白方小.png"))
	got := Guess(img, []image.Image{name}, detect.ModeAuto)
	if got != "right" {
		t.Fatalf("death-swap should be right (白方小), got %q", got)
	}
}

func TestGuessPicksRightOnSixPlayerVS(t *testing.T) {
	root := findRepoRoot(t)
	img := mustPNG(t, filepath.Join(root, "inbox", "reject", "加载.png"))
	name := mustPNG(t, filepath.Join(root, "assets", "identity", "白方小.png"))
	got := Guess(img, []image.Image{name}, detect.ModeAuto)
	if got != "right" {
		t.Fatalf("6p VS should be right, got %q", got)
	}
}

func TestReadFindsOpponentOnVS(t *testing.T) {
	root := findRepoRoot(t)
	img := mustPNG(t, filepath.Join(root, "inbox", "reject", "忍者加载.png"))
	book := []Named{
		{Name: "白方小", Image: mustPNG(t, filepath.Join(root, "assets", "identity", "白方小.png")), Mine: true},
		{Name: "宇智波无名", Image: mustPNG(t, filepath.Join(root, "assets", "identity", "宇智波无名.png"))},
	}
	got := Read(img, book, detect.ModeAuto)
	if got.Side != "right" || got.Mine != "白方小" || got.Opp != "宇智波无名" {
		t.Fatalf("got %+v", got)
	}
}

func TestReadUsesVisibleOpponentWhenMineNameIsObscured(t *testing.T) {
	root := findRepoRoot(t)
	t.Chdir(root)
	img := mustPNG(t, filepath.Join(root, "inbox", "reject", "忍者加载.png"))
	book := []Named{
		{Name: "白方小", Image: mustPNG(t, filepath.Join(root, "assets", "identity", "白方小.png")), Mine: true},
		{Name: "宇智波无名", Image: mustPNG(t, filepath.Join(root, "assets", "identity", "宇智波无名.png"))},
	}
	ca, ok := detect.ResolveContentArea(img, detect.ModeAuto, detect.LogicWidth, detect.LogicHeight, .015)
	if !ok {
		t.Fatal("invalid fixture area")
	}
	// This fixture places the configured player on the right. Hide only that
	// strip; the visible non-mine name on the left must still establish the side.
	draw.Draw(img, mapRect(ca, DefaultROIs.Right), image.NewUniform(color.RGBA{20, 20, 20, 255}), image.Point{}, draw.Src)
	got := Read(img, book, detect.ModeAuto)
	if got.Side != "right" || got.Opp != "宇智波无名" {
		t.Fatalf("visible opponent should infer our side, got %+v", got)
	}
}

func TestGuessEmptyWhenNameMissing(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 192, 108))
	name := image.NewRGBA(image.Rect(0, 0, 32, 16))
	if got := Guess(img, []image.Image{name}, detect.ModeStretch); got != "" {
		t.Fatalf("got %q", got)
	}
}

func mustPNG(t *testing.T, path string) *image.RGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	return match.CropRGBA(matchRGBA(img), img.Bounds())
}

func matchRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(x-b.Min.X, y-b.Min.Y, src.At(x, y))
		}
	}
	return out
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found")
	return ""
}
