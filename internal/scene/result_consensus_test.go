package scene

import (
	"image"
	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"testing"
)

func TestResultRequiresIndependentPageControls(t *testing.T) {
	for _, tc := range []struct {
		name    string
		second  bool
		overlap bool
		want    engine.GateKind
	}{
		{"one mark", false, false, engine.GateUncertain},
		{"duplicate mark", true, true, engine.GateUncertain},
		{"two separate controls", true, false, engine.GateNotFight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 192, 108))
			paintMark(img, img.Bounds())
			cat := testCatalog(img, image.Rect(10, 8, 42, 28), "result")
			cat.minimumRegions = map[string]int{"result": 2}
			values := []float64{.99}
			if tc.second {
				other := cat.templates[0]
				if !tc.overlap {
					other.spec.ROI = config.NormalizedRect{X: .6, Y: .01, Width: .3, Height: .25}
				}
				cat.templates = append(cat.templates, other)
				values = append(values, .97)
			}
			cat.matcher = &scoreSequence{values: values}
			d := cat.Decide(img)
			if d.Kind != tc.want || (tc.want == engine.GateUncertain && d.SceneID != "") {
				t.Fatalf("%+v", d)
			}
		})
	}
}
