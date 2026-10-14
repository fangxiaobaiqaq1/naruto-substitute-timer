package scene

import (
	"image"
	"math"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
)

func TestControlSearchAreaUsesScreenButTemplateKeepsHUDScale(t *testing.T) {
	for _, size := range []image.Point{{1280, 720}, {1516, 948}, {2400, 1080}, {2880, 1800}} {
		ca := detect.ContentArea{X: 17, Y: 23, W: size.X, H: size.Y}
		templ := template{spec: TemplateSpec{Anchor: "screen", ROI: config.NormalizedRect{X: .8, Y: .7, Width: .2, Height: .3}}, gray: image.NewGray(image.Rect(0, 0, 98, 107)), refW: 1920, refH: 1080}
		p := prepareOne(ca, templ)
		wantMin := image.Pt(ca.X+int(.8*float64(ca.W)), ca.Y+int(.7*float64(ca.H)))
		if delta := p.roi.Min.Sub(wantMin); math.Abs(float64(delta.X)) > 1 || math.Abs(float64(delta.Y)) > 1 || p.roi.Max != image.Pt(ca.X+ca.W, ca.Y+ca.H) {
			t.Fatalf("%v: controls were searched in a cropped/stretched reference panel: %v", size, p.roi)
		}
		s := math.Min(float64(size.X)/1920, float64(size.Y)/1080)
		if got, want := p.gray.Bounds().Size(), image.Pt(int(math.Round(98*s)), int(math.Round(107*s))); got != want {
			t.Fatalf("%v: control art was distorted: got=%v want=%v", size, got, want)
		}
	}
}

func TestCoveredMarkerStartNeedsIndependentControlsAndNoPageConflict(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"two controls", true}, {"one control", false}, {"below threshold", false},
		{"same region twice", false}, {"result overlay", false}, {"competing profile", false},
		{"configured minimum", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 192, 108))
			paintMark(img, img.Bounds())
			cat := testCatalog(img, image.Rect(24, 76, 40, 92), "fight")
			cat.templates[0].spec.ContinuationOnly = true
			cat.templates[0].spec.Threshold = .85
			cat.templates = append(cat.templates, cat.templates[0])
			cat.templates[1].spec.ROI.X = .72
			scores := []float64{.95, .92}
			switch tc.name {
			case "one control":
				scores[1] = .3
			case "below threshold":
				scores[1] = .849
			case "same region twice":
				cat.templates[1].spec.ROI = cat.templates[0].spec.ROI
			case "result overlay", "competing profile":
				cat.templates = append(cat.templates, cat.templates[0])
				cat.templates[2].spec.ContinuationOnly = false
				if tc.name == "result overlay" {
					cat.templates[2].spec.Scene = "result"
				} else {
					cat.templates[2].spec.LayoutProfile = "camp"
				}
				scores = append(scores, .96)
			case "configured minimum":
				cat.cfg.MinFightScore = .94
			}
			cat.matcher = &scoreSequence{values: scores}
			d := cat.StartFight(img, "duel")
			if (d.Kind == engine.GateFight) != tc.want {
				t.Fatalf("%s: %+v", tc.name, d)
			}
			if tc.want && (d.LayoutProfile != "duel" || d.SceneID != "fight" || d.RoundOpening || d.Confidence != .92) {
				t.Fatalf("entry borrowed profile/opening/confidence: %+v", d)
			}
		})
	}
}
