package scene

import (
	"image"
	"image/draw"
	"path/filepath"
	"testing"

	xdraw "golang.org/x/image/draw"
	"narutotimer/internal/config"
)

func TestBattleControlEvidenceAcrossRecordedHUDs(t *testing.T) {
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cat, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"fight/duel_live_20260821.png", "fight/duel_20260821.png", "regressions/duel-user-20260906.png", "regressions/duel-second-round-20260906.png"} {
		src := recordedPage(t, "../../inbox/"+path)
		for _, w := range []int{800, 960, 1280, 1600, 1920} {
			img := image.NewRGBA(image.Rect(0, 0, w, w*9/16))
			xdraw.BiLinear.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
			if !cat.SupportsFight(img, "duel") {
				t.Fatalf("missing control in %s at %d; scores=%+v", path, w, cat.ScoreAll(img))
			}
		}
	}
	for _, path := range []string{"lobby/4a3013cc-c1bc-4c87-bb3e-ac87a90b6148.png", "pick/选忍者.png", "pick/vs.png", "result/失败.png", "result/胜利或完胜.png"} {
		if cat.SupportsFight(recordedPage(t, "../../inbox/"+path), "duel") {
			t.Fatalf("ordinary page passed battle control: %s", path)
		}
	}
}
