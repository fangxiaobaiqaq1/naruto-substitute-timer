package scene

import (
	"image"
	"image/color"
	"image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"path/filepath"
	"testing"
)

func TestExtremeBackdropDoesNotHideReadableBattleHUD(t *testing.T) {
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cat, err := Load(cfg)
	if err != nil || cat == nil {
		t.Fatalf("load: %v", err)
	}
	source := recordedPage(t, "../../inbox/fight/duel_20260821.png")
	ca, ok := cat.contentArea(source)
	if !ok {
		t.Fatal("area")
	}
	var landmark image.Rectangle
	for _, p := range cat.prepare(ca) {
		if p.spec.ID == "raw-duel-round" {
			landmark = p.roi
		}
	}
	if landmark.Empty() {
		t.Fatal("missing duel landmark")
	}
	for _, v := range []uint8{0, 10, 245, 255} {
		img := image.NewRGBA(source.Bounds())
		draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{v, v, v, 255}), image.Point{}, draw.Src)
		draw.Draw(img, landmark, source, landmark.Min, draw.Src)
		if d := cat.Decide(img); d.Kind != engine.GateFight || d.SceneID != "fight" || d.LayoutProfile != "duel" {
			t.Fatalf("background %d hid visible round marker: %+v", v, d)
		}
	}
}

func TestUniformExtremesAreStillBlank(t *testing.T) {
	for _, v := range []uint8{0, 12, 245, 255} {
		img := image.NewRGBA(image.Rect(0, 0, 640, 360))
		draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{v, v, v, 255}), image.Point{}, draw.Src)
		blank, kind := lumaBlank(img)
		if !blank || kind != engine.GateBlank {
			t.Fatalf("flat %d: blank=%v kind=%v", v, blank, kind)
		}
	}
}
