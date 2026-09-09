package identity

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"testing"

	xdraw "golang.org/x/image/draw"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
)

// Private capture fixtures show this account. Production defaults intentionally
// contain no account names, so fixture ownership must be explicit in tests.
func accountFixtureConfig() config.Config {
	cfg := config.Default()
	cfg.UI.PlayerNames = []string{"白方小"}
	return cfg
}

func TestGuesserReadsBattleAccountNames(t *testing.T) {
	root := findRepoRoot(t)
	t.Chdir(root)
	t.Cleanup(func() { SetMineNames(config.Default().UI.PlayerNames) })
	for _, tc := range []struct{ file, side string }{
		{"inbox/regressions/duel-player-left-20260906.png", "left"},
		{"inbox/fight/duel_20260821.png", "left"},
		{"inbox/regressions/duel-user-20260906.png", "right"},
		{"inbox/regressions/duel-second-round-20260906.png", "right"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			guess := Guesser(accountFixtureConfig())
			if guess == nil {
				t.Fatal("missing identity guesser")
			}
			got := guess(mustPNG(t, filepath.Join(root, tc.file)), "fight")
			if got.Side != tc.side || got.Mine != "白方小" {
				t.Fatalf("battle account: got %+v, want player %s (opponent %s)", got, tc.side, opposite(tc.side))
			}
		})
	}
}

func TestReadDoesNotTreatOpponentBookAsMine(t *testing.T) {
	root := findRepoRoot(t)
	img := mustPNG(t, filepath.Join(root, "inbox/reject/忍者加载.png"))
	name := mustPNG(t, filepath.Join(root, "assets/identity/宇智波无名.png"))
	got := Read(img, []Named{{Name: "宇智波无名", Image: name}}, detect.ModeAuto)
	if got.Side != "" {
		t.Fatalf("opponent template cannot establish my side: %+v", got)
	}
}

func TestGuesserForgetsOldAccountAfterSettingsChange(t *testing.T) {
	root := findRepoRoot(t)
	t.Chdir(root)
	t.Cleanup(func() { SetMineNames(config.Default().UI.PlayerNames) })
	guess := Guesser(accountFixtureConfig())
	img := mustPNG(t, filepath.Join(root, "inbox/reject/忍者加载.png"))
	if got := guess(img, "vs"); got.Side != "right" {
		t.Fatalf("setup: %+v", got)
	}
	SetMineNames([]string{"没有模板的新账号"})
	if got := guess(img, "vs"); got.Side != "" {
		t.Fatalf("old account remained marked Mine: %+v", got)
	}
}

func TestGuesserIgnoresUnconfirmedScenes(t *testing.T) {
	root := findRepoRoot(t)
	t.Chdir(root)
	t.Cleanup(func() { SetMineNames(config.Default().UI.PlayerNames) })
	for _, scene := range []string{"", "lobby", "result", "pick", "ban"} {
		guess := Guesser(accountFixtureConfig())
		// Even matching name pixels cannot prove a side outside VS or a battle.
		img := mustPNG(t, filepath.Join(root, "inbox/reject/忍者加载.png"))
		if got := guess(img, scene); got.Side != "" {
			t.Fatalf("scene %q: %+v", scene, got)
		}
	}
	// Blank images should never establish identity.
	guess := Guesser(accountFixtureConfig())
	if got := guess(image.NewRGBA(image.Rect(0, 0, 950, 534)), "fight"); got.Side != "" {
		t.Fatalf("blank: %+v", got)
	}
}

func TestBattleIdentityAcrossResolutionsAndContentOrigins(t *testing.T) {
	root := findRepoRoot(t)
	t.Chdir(root)
	book := LoadBook(accountFixtureConfig())
	src := mustPNG(t, filepath.Join(root, "inbox/regressions/duel-player-left-20260906.png"))
	for _, w := range []int{640, 800, 950, 960, 1280, 1600, 1920, 2560} {
		t.Run(fmt.Sprintf("width-%d", w), func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, w, w*9/16))
			xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
			if got := ReadFight(img, book, detect.ModeAuto); got.Side != "left" {
				t.Fatalf("%dpx: %+v", w, got)
			}
		})
	}
	for _, tc := range []struct {
		name            string
		bounds, content image.Rectangle
	}{
		{"horizontal-bars", image.Rect(0, 0, 1280, 960), image.Rect(0, 120, 1280, 840)},
		{"vertical-bars", image.Rect(0, 0, 1920, 900), image.Rect(160, 0, 1760, 900)},
		{"nonzero-origin", image.Rect(30, 50, 1630, 950), image.Rect(30, 50, 1630, 950)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(tc.bounds)
			xdraw.BiLinear.Scale(img, tc.content, src, src.Bounds(), draw.Src, nil)
			if got := ReadFight(img, book, detect.ModeAuto); got.Side != "left" {
				t.Fatalf("%s: %+v", tc.name, got)
			}
		})
	}
}

func TestBattleIdentityRejectsMissingOrAmbiguousName(t *testing.T) {
	root := findRepoRoot(t)
	t.Chdir(root)
	book := LoadBook(accountFixtureConfig())
	for _, ambiguous := range []bool{false, true} {
		img := mustPNG(t, filepath.Join(root, "inbox/regressions/duel-player-left-20260906.png"))
		ca, ok := detect.ResolveContentArea(img, detect.ModeAuto, detect.LogicWidth, detect.LogicHeight, 0.015)
		if !ok {
			t.Fatal("invalid fixture area")
		}
		left, right := mapRect(ca, FightROIs.Left), mapRect(ca, FightROIs.Right)
		if ambiguous {
			draw.Draw(img, right, img, left.Min, draw.Src)
		} else {
			draw.Draw(img, left, image.NewUniform(color.RGBA{20, 20, 20, 255}), image.Point{}, draw.Src)
		}
		if got := ReadFight(img, book, detect.ModeAuto); got.Side != "" {
			t.Fatalf("ambiguous=%v: %+v", ambiguous, got)
		}
	}
}

func TestGuesserLoadsTemplateAddedAfterStartup(t *testing.T) {
	root := findRepoRoot(t)
	img := mustPNG(t, filepath.Join(root, "inbox/reject/忍者加载.png"))
	data, err := os.ReadFile(filepath.Join(root, "assets/identity/白方小.png"))
	if err != nil {
		t.Fatal(err)
	}
	oppData, err := os.ReadFile(filepath.Join(root, "assets/identity/宇智波无名.png"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Cleanup(func() { SetMineNames(config.Default().UI.PlayerNames) })
	guess := Guesser(accountFixtureConfig())
	if guess == nil {
		t.Fatal("empty initial book must allow later setup")
	}
	if got := guess(img, "vs"); got.Side != "" {
		t.Fatalf("empty book: %+v", got)
	}
	if err := os.MkdirAll(AssetDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(AssetDir, "白方小.png"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(AssetDir, "宇智波无名.png"), oppData, 0600); err != nil {
		t.Fatal(err)
	}
	// Saving the account field, even without changing its spelling, reloads files.
	SetMineNames([]string{"白方小"})
	if got := guess(img, "vs"); got.Side != "right" {
		t.Fatalf("new template not loaded: %+v", got)
	}
}

func BenchmarkBattleIdentity(b *testing.B) {
	b.Chdir(filepath.Join("..", ".."))
	src, err := loadImage("inbox/regressions/duel-player-left-20260906.png")
	if err != nil {
		b.Fatal(err)
	}
	img := matchRGBA(src)
	book := LoadBook(accountFixtureConfig())
	b.ResetTimer()
	for b.Loop() {
		ReadFight(img, book, detect.ModeAuto)
	}
}

func TestGuesserUpdatesAcrossSceneAndAccountChanges(t *testing.T) {
	root := findRepoRoot(t)
	t.Chdir(root)
	t.Cleanup(func() { SetMineNames(config.Default().UI.PlayerNames) })
	guess := Guesser(accountFixtureConfig())
	vs := mustPNG(t, filepath.Join(root, "inbox/reject/忍者加载.png"))
	fight := mustPNG(t, filepath.Join(root, "inbox/regressions/duel-player-left-20260906.png"))
	if got := guess(vs, "vs"); got.Side != "right" {
		t.Fatalf("VS: %+v", got)
	}
	// A scene transition bypasses throttling: do not retain the old side.
	if got := guess(fight, "fight"); got.Side != "left" {
		t.Fatalf("battle after VS: %+v", got)
	}
	SetMineNames([]string{"宇智波无名"})
	if got := guess(vs, "vs"); got.Side != "left" || got.Mine != "宇智波无名" {
		t.Fatalf("updated account: %+v", got)
	}
	SetMineNames(nil)
	if got := guess(vs, "vs"); got.Side != "" {
		t.Fatalf("empty account list must disable auto detection: %+v", got)
	}
}
