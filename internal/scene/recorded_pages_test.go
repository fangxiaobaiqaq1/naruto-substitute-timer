package scene

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
	"narutotimer/internal/engine"
)

// These fixtures cover recorded pages, not arbitrary menus. Each resize also
// exercises template-cache invalidation while the same catalog remains alive.
func TestRecordedOrdinaryPagesAtMultipleResolutions(t *testing.T) {
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cat, err := Load(cfg)
	if err != nil || cat == nil {
		t.Fatalf("load catalog: %v", err)
	}
	for _, tc := range []struct {
		path, scene string
		kind        engine.GateKind
	}{
		{"lobby/4a3013cc-c1bc-4c87-bb3e-ac87a90b6148.png", "lobby", engine.GateNotFight},
		{"lobby/c2cd9497-fb9d-4820-bb03-6e8f372ab40f.png", "lobby", engine.GateNotFight},
		{"pick/queue_search.png", "queue", engine.GateUncertain},
		{"pick/选忍者.png", "pick", engine.GateUncertain},
		{"pick/vs.png", "vs", engine.GateUncertain},
		{"pick/vs_load.png", "vs", engine.GateUncertain},
		{"result/失败.png", "result", engine.GateNotFight},
		{"result/胜利或完胜.png", "result", engine.GateNotFight},
		{"regressions/ban-selection-20260906.png", "ban", engine.GateUncertain},
		{"regressions/duel-lobby-20260906.png", "lobby", engine.GateNotFight},
	} {
		source := recordedPage(t, filepath.Join("../../inbox", tc.path))
		for _, width := range []int{640, 800, 1072, 1280, 1600, 1920, 960, 2560} {
			t.Run(fmt.Sprintf("%s/%dx%d", tc.path, width, width*9/16), func(t *testing.T) {
				img := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
				xdraw.BiLinear.Scale(img, img.Bounds(), source, source.Bounds(), draw.Src, nil)
				d := cat.Decide(img)
				if d.Kind != tc.kind || d.SceneID != tc.scene {
					t.Fatalf("want %s kind %v, got %+v; scores=%+v", tc.scene, tc.kind, d, cat.ScoreAll(img))
				}
			})
		}
	}
}

func recordedPage(t *testing.T, path string) *image.RGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(out, out.Bounds(), src, src.Bounds().Min, draw.Src)
	return out
}
