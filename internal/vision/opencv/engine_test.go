//go:build opencv

package opencv

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/domain"
	"narutotimer/internal/engine"
	"narutotimer/internal/sequence"
	"narutotimer/internal/vision/cvbind"
)

func TestGoldDecodesAsAvailable(t *testing.T) {
	cfg := config.Default()
	obs := make([]domain.BeadObservation, 4)
	for i := range obs {
		obs[i] = classifySample(i, cvbind.Sample{Gold: 90, Dark: 5, Other: 5}, cfg.Vision)
		if obs[i].State != domain.BeadLight || obs[i].Scores.Gone != 0 {
			t.Fatalf("gold observation must be available, not gone: %+v", obs[i])
		}
	}
	decoded := sequence.Decode(obs, sequence.Config{
		Allowed: cfg.Sequence.Allowed, UnknownPenalty: cfg.Sequence.UnknownPenalty,
		MinimumDecodedConfidence: cfg.Sequence.MinimumDecodedConfidence,
	})
	if !decoded.Accepted || decoded.Count == nil || *decoded.Count != 4 {
		t.Fatalf("four gold beads must decode as four ready: %+v", decoded)
	}
}

func TestSceneSelectsOnlyItsCalibratedProfile(t *testing.T) {
	cfg := config.Default()
	profile := cfg.Layout.Profile("camp")
	duel := cfg.Layout.Profile("duel")
	profile.Left.NominalCenters[0].X = .20
	duel.Left.NominalCenters[0].X = .80
	cfg.Layout.Profiles = map[string]config.LayoutProfile{"camp": profile, "duel": duel}
	cfg.Layout.PreferredProfile = "auto"
	e := New(engine.NewConfiguredLayout(detect.ModeStretch, cfg.Layout), cfg)
	for _, want := range []struct {
		name string
		x    int
	}{{"camp", 320}, {"duel", 1280}, {"camp", 320}} {
		e.Prefer(want.name)
		positions, name := e.positions(1600, 900)
		if name != want.name || len(positions) != 8 || positions[0].X != want.x {
			t.Fatalf("profile %s selected %s positions %+v", want.name, name, positions)
		}
	}
	cfg.Layout.PreferredProfile = "camp"
	e = New(engine.NewConfiguredLayout(detect.ModeStretch, cfg.Layout), cfg)
	e.Prefer("duel")
	positions, name := e.positions(1600, 900)
	if name != "camp" || positions[0].X != 320 {
		t.Fatalf("manual calibration must override scene preference: %s %+v", name, positions)
	}
}

func TestNativeGoldImageAndConfidenceGate(t *testing.T) {
	cfg := config.Default()
	layout := engine.NewConfiguredLayout(detect.ModeStretch, cfg.Layout)
	img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 255, G: 190, B: 0, A: 255}), image.Point{}, draw.Src)
	e := New(layout, cfg)
	e.Prefer("camp")
	result := e.Analyze(img)
	if len(result.Beads) != 8 || result.Name != "opencv-camp" {
		t.Fatalf("native analysis did not return camp beads: %+v", result)
	}
	for _, bead := range result.Beads {
		if !bead.Lit || !bead.Gold || bead.Unknown {
			t.Fatalf("solid gold image should yield available gold beads: %+v", bead)
		}
	}
	// Force rejection of even a visually plausible prefix. It must remain unknown.
	cfg.Sequence.MinimumDecodedConfidence = 1.1
	e = New(layout, cfg)
	result = e.Analyze(img)
	for _, bead := range result.Beads {
		if !bead.Unknown || bead.Lit {
			t.Fatalf("rejected sequence bypassed confidence gate: %+v", bead)
		}
	}
}

func TestNativeUsesSharedContentGeometry(t *testing.T) {
	cfg := config.Default()
	for _, bounds := range []image.Rectangle{image.Rect(0, 0, 1920, 900), image.Rect(30, 50, 1630, 950)} {
		img := image.NewRGBA(bounds)
		content := image.Rect(bounds.Min.X+(bounds.Dx()-1600)/2, bounds.Min.Y, bounds.Min.X+(bounds.Dx()-1600)/2+1600, bounds.Max.Y)
		draw.Draw(img, content, image.NewUniform(color.RGBA{R: 255, G: 190, B: 0, A: 255}), image.Point{}, draw.Src)
		layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
		e := New(layout, cfg)
		e.Prefer("duel")
		result := e.Analyze(img)
		area, ok := layout.ContentArea(img)
		if !ok || len(result.Beads) != 8 {
			t.Fatalf("shared geometry rejected: %+v", result)
		}
		positions := layout.PositionsIn("duel", area)
		for i, bead := range result.Beads {
			if bead.X != positions[i].X || bead.Y != positions[i].Y || bead.Unknown || !bead.Gold {
				t.Fatalf("native coordinates differ: %+v expected %+v", bead, positions[i])
			}
		}
	}
	wide := image.NewRGBA(image.Rect(0, 0, 1920, 900))
	draw.Draw(wide, wide.Bounds(), image.NewUniform(color.RGBA{R: 255, G: 190, B: 0, A: 255}), image.Point{}, draw.Src)
	e := New(engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout), cfg)
	result := e.Analyze(wide)
	if !result.Uncertain || result.Scene != "unsupported-resolution" || len(result.Beads) != 0 {
		t.Fatalf("native aspect supplied guessed coordinates: %+v", result)
	}
}
