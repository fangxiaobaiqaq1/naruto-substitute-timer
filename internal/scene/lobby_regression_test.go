package scene

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
)

func TestDuelLobbyControlRecognitionIgnoresAnimatedBackdrop(t *testing.T) {
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cat, err := Load(cfg)
	if err != nil || cat == nil {
		t.Fatalf("load: %v", err)
	}
	original := recordedPage(t, "../../inbox/regressions/duel-lobby-20260906.png")
	ca, ok := cat.contentArea(original)
	if !ok {
		t.Fatal("fixture geometry")
	}
	var controls []preparedTemplate
	for _, p := range cat.prepare(ca) {
		if p.spec.ID == "duel-lobby-ninjutsu" || p.spec.ID == "duel-lobby-ranked" {
			controls = append(controls, p)
		}
	}
	if len(controls) != 2 {
		t.Fatal("missing independent lobby controls")
	}
	for _, background := range []color.RGBA{{20, 25, 30, 255}, {180, 70, 20, 255}, {225, 225, 225, 255}} {
		img := image.NewRGBA(original.Bounds())
		draw.Draw(img, img.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
		for _, p := range controls {
			draw.Draw(img, p.roi, original, p.roi.Min, draw.Src)
		}
		if d := cat.Decide(img); d.SceneID != "lobby" || d.Kind != engine.GateNotFight {
			t.Fatalf("background %v: %+v", background, d)
		}
	}
	// An unrelated battle must not get a lobby label, even with bright HUD text.
	fight := recordedPage(t, "../../inbox/regressions/duel-player-left-20260906.png")
	if d := cat.Decide(fight); d.Kind != engine.GateFight || d.SceneID != "fight" {
		t.Fatalf("lobby templates polluted battle: %+v", d)
	}
	// Removing both named controls from this previously unrecognized page must
	// not let the dark illustration or yellow menu borders establish the scene.
	for _, p := range controls {
		draw.Draw(original, p.roi, image.NewUniform(color.RGBA{30, 35, 40, 255}), image.Point{}, draw.Src)
	}
	if d := cat.Decide(original); d.SceneID == "lobby" {
		t.Fatalf("no account/menu text, but background claimed lobby: %+v", d)
	}
}

func TestROIScorerMatchesFullFrameGrayscaleExactly(t *testing.T) {
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cat, err := Load(cfg)
	if err != nil || cat == nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range []string{"duel-lobby-20260906.png", "duel-player-left-20260906.png", "ban-selection-20260906.png"} {
		original := recordedPage(t, filepath.Join("../../inbox/regressions", name))
		backing := image.NewRGBA(image.Rect(-40, -50, original.Bounds().Dx()+60, original.Bounds().Dy()+80))
		shifted := backing.SubImage(image.Rectangle{Min: image.Pt(10, 20), Max: image.Pt(10, 20).Add(original.Bounds().Size())}).(*image.RGBA)
		draw.Draw(shifted, shifted.Bounds(), original, original.Bounds().Min, draw.Src)
		for _, img := range []*image.RGBA{original, shifted} {
			gray := match.ToGray(img)
			ca, ok := cat.contentArea(img)
			if !ok {
				t.Fatal("fixture area")
			}
			roiScore := cat.scorer(img)
			for _, templ := range cat.prepare(ca) {
				want := cat.scoreOne(img, gray, templ)
				got := roiScore(templ)
				if got != want {
					t.Fatalf("%s %v template=%s ROI conversion changed evidence: got=%+v want=%+v", name, img.Bounds(), templ.spec.ID, got, want)
				}
			}
		}
	}
}
