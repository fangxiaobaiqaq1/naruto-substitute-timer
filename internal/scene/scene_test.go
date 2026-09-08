package scene

import (
	"image"
	"image/color"
	"sync"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
)

func TestColorGateMapsClassifyScreen(t *testing.T) {
	g := ColorGate{Mode: detect.ModeStretch}
	blank := image.NewRGBA(image.Rect(0, 0, 80, 60))
	d := g.Decide(blank)
	if d.Kind != engine.GateBlank {
		t.Fatalf("black frame: got %v", d)
	}

	other := image.NewRGBA(image.Rect(0, 0, 80, 60))
	fill(other, other.Bounds(), color.RGBA{R: 90, G: 90, B: 90, A: 255})
	d = g.Decide(other)
	if d.Kind != engine.GateNotFight {
		t.Fatalf("non-bead frame: got %v", d)
	}
}

func TestCatalogFightWins(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 192, 108))
	fill(img, img.Bounds(), color.RGBA{R: 40, G: 40, B: 50, A: 255})
	mark := image.Rect(80, 8, 112, 28)
	paintMark(img, mark)

	cat := testCatalog(img, mark, "fight")
	d := cat.Decide(img)
	if d.Kind != engine.GateFight || d.SceneID != "fight" {
		t.Fatalf("want fight, got %+v", d)
	}
}

func TestCatalogOtherWins(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 192, 108))
	fill(img, img.Bounds(), color.RGBA{R: 40, G: 40, B: 50, A: 255})
	mark := image.Rect(70, 80, 122, 100)
	paintMark(img, mark)

	cat := testCatalog(img, mark, "lobby")
	d := cat.Decide(img)
	if d.Kind != engine.GateNotFight || d.SceneID != "lobby" {
		t.Fatalf("want not-fight lobby, got %+v", d)
	}
}

func TestCatalogHoldSceneIsUncertain(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 192, 108))
	fill(img, img.Bounds(), color.RGBA{R: 40, G: 40, B: 50, A: 255})
	mark := image.Rect(80, 70, 120, 100)
	paintMark(img, mark)
	cat := testCatalog(img, mark, "vs")
	d := cat.Decide(img)
	if d.Kind != engine.GateUncertain || d.SceneID != "vs" {
		t.Fatalf("vs/death-swap must hold, got %+v", d)
	}
}

func TestCatalogLowFightScoreWithoutOtherIsNotFight(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 192, 108))
	fill(img, img.Bounds(), color.RGBA{R: 90, G: 90, B: 100, A: 255})
	templImg := image.NewRGBA(image.Rect(0, 0, 32, 20))
	paintMark(templImg, templImg.Bounds())
	cat := &Catalog{
		cfg: config.SceneConfig{
			FightScenes:    []string{"fight"},
			MinFightScore:  0.82,
			MinOtherScore:  0.85,
			Margin:         0.05,
			UncertainBelow: 0.55,
		},
		mode:     detect.ModeStretch,
		matcher:  match.NCC{},
		fightSet: map[string]bool{"fight": true},
		templates: []template{{
			spec: TemplateSpec{
				ID: "fight-mark", Scene: "fight",
				ROI:       config.NormalizedRect{X: 0.4, Y: 0.05, Width: 0.2, Height: 0.2},
				Threshold: 0.82,
			},
			gray: match.ToGray(templImg),
			refW: 192, refH: 108,
		}},
	}
	d := cat.Decide(img)
	if d.Kind != engine.GateUncertain {
		t.Fatalf("no other templates + low fight score must hold, got %+v", d)
	}
}

func TestCatalogUncertainWhenFightAndOtherBothMiss(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 192, 108))
	fill(img, img.Bounds(), color.RGBA{R: 90, G: 90, B: 100, A: 255})
	templImg := image.NewRGBA(image.Rect(0, 0, 32, 20))
	paintMark(templImg, templImg.Bounds())
	spec := func(id, scene string, y float64) template {
		return template{
			spec: TemplateSpec{
				ID: id, Scene: scene,
				ROI:       config.NormalizedRect{X: 0.4, Y: y, Width: 0.2, Height: 0.18},
				Threshold: 0.82,
			},
			gray: match.ToGray(templImg),
			refW: 192, refH: 108,
		}
	}
	cat := &Catalog{
		cfg: config.SceneConfig{
			FightScenes:    []string{"fight"},
			MinFightScore:  0.82,
			MinOtherScore:  0.85,
			Margin:         0.05,
			UncertainBelow: 0.55,
		},
		mode:     detect.ModeStretch,
		matcher:  match.NCC{},
		fightSet: map[string]bool{"fight": true},
		templates: []template{
			spec("fight-mark", "fight", 0.05),
			spec("lobby-mark", "lobby", 0.75),
		},
	}
	d := cat.Decide(img)
	if d.Kind != engine.GateUncertain {
		t.Fatalf("both templates present but missing must be uncertain, got %+v", d)
	}
}

func TestNewGateFallsBackWhenDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Scene.Enabled = false
	g, src, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if src != "color-fallback" {
		t.Fatalf("source = %s", src)
	}
	if _, ok := g.(ColorGate); !ok {
		t.Fatalf("got %T", g)
	}
}

type recordingMatcher struct{ queries []match.Query }

type scoreSequence struct {
	values []float64
	index  int
}

func (m *scoreSequence) Match(match.Query) (match.Score, error) {
	v := m.values[m.index]
	m.index++
	return match.Score{Value: v}, nil
}

func TestCatalogScoresStayBoundToAcceptedTemplate(t *testing.T) {
	for _, sceneID := range []string{"fight", "lobby"} {
		t.Run(sceneID, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 192, 108))
			paintMark(img, img.Bounds())
			cat := testCatalog(img, image.Rect(80, 8, 112, 28), sceneID)
			cat.cfg.MinFightScore = .85
			cat.cfg.MinOtherScore = .85
			cat.templates = append(cat.templates, cat.templates[0])
			cat.templates[1].spec.Threshold = .98
			cat.matcher = &scoreSequence{values: []float64{.80, .95}}
			got := cat.Decide(img)
			if got.Kind != engine.GateUncertain || got.SceneID != "" {
				t.Fatalf("candidate borrowed a rejected score: %+v", got)
			}
		})
	}
}

func TestCatalogDoesNotSkipCompetingHUD(t *testing.T) {
	for _, otherScene := range []string{"fight", "lobby"} {
		t.Run(otherScene, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 192, 108))
			paintMark(img, img.Bounds())
			cat := testCatalog(img, image.Rect(80, 8, 112, 28), "fight")
			cat.templates[0].spec.LayoutProfile = "camp"
			cat.templates = append(cat.templates, cat.templates[0])
			cat.templates[1].spec.Scene = otherScene
			cat.templates[1].spec.LayoutProfile = "duel"
			cat.matcher = &scoreSequence{values: []float64{.96, .95}}
			got := cat.Decide(img)
			if got.Kind != engine.GateUncertain {
				t.Fatalf("conflicting high confidence HUDs must hold: %+v", got)
			}
		})
	}
}

func (m *recordingMatcher) Match(q match.Query) (match.Score, error) {
	m.queries = append(m.queries, q)
	return match.Score{Value: 0.95}, nil
}

func TestCatalogReusesFrameGrayAndScaledTemplates(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 192, 108))
	paintMark(img, img.Bounds())
	cat := testCatalog(img, image.Rect(80, 8, 112, 28), "fight")
	cat.templates[0].mask = image.NewGray(cat.templates[0].gray.Bounds())
	cat.templates = append(cat.templates, cat.templates[0])
	recorder := &recordingMatcher{}
	cat.matcher = recorder
	cat.ScoreAll(img)
	cat.ScoreAll(img)
	q := recorder.queries
	if len(q) != 4 || q[0].Gray == nil || q[0].Gray != q[1].Gray || q[2].Gray != q[3].Gray {
		t.Fatalf("templates must share one gray image per frame: %+v", q)
	}
	if q[0].Gray == q[2].Gray {
		t.Fatal("gray image was reused across independent frames")
	}
	if q[0].Template != q[2].Template || q[0].Mask != q[2].Mask {
		t.Fatal("same-size frame rebuilt the scaled template or mask")
	}
	resized := image.NewRGBA(image.Rect(0, 0, 384, 216))
	cat.ScoreAll(resized)
	if recorder.queries[4].Template == q[0].Template || recorder.queries[4].Mask == q[0].Mask {
		t.Fatal("resolution change reused stale scaled images")
	}
	if got := recorder.queries[4].Template.Bounds().Size(); got != image.Pt(64, 40) {
		t.Fatalf("resized template dimensions = %v", got)
	}
	// Previously returned images remain valid while another frame replaces cache.
	if got := q[0].Template.Bounds().Size(); got != image.Pt(32, 20) {
		t.Fatalf("old template mutated: %v", got)
	}
}

func TestCatalogConcurrentSizesPreserveDecision(t *testing.T) {
	small := image.NewRGBA(image.Rect(0, 0, 192, 108))
	large := image.NewRGBA(image.Rect(0, 0, 384, 216))
	for _, img := range []*image.RGBA{small, large} {
		fill(img, img.Bounds(), color.RGBA{R: 100, G: 120, B: 140, A: 255})
	}
	cat := testCatalog(small, image.Rect(80, 8, 112, 28), "fight")
	cat.templates[0].spec.LayoutProfile = "camp"
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				img := []*image.RGBA{small, large}[(i+j)%2]
				got := cat.Decide(img)
				if got.Kind != engine.GateFight || got.LayoutProfile != "camp" || got.Confidence != 1 {
					t.Errorf("concurrent frame %v: %+v", img.Bounds(), got)
				}
			}
		}(i)
	}
	wg.Wait()
}

func testCatalog(src *image.RGBA, mark image.Rectangle, sceneID string) *Catalog {
	crop := match.CropRGBA(src, mark)
	roi := config.NormalizedRect{
		X:      float64(mark.Min.X) / 192,
		Y:      float64(mark.Min.Y) / 108,
		Width:  float64(mark.Dx()) / 192,
		Height: float64(mark.Dy()) / 108,
	}
	// 搜索框略大于模板，避免缩放误差。
	pad := 0.04
	roi.X -= pad
	roi.Y -= pad
	roi.Width += pad * 2
	roi.Height += pad * 2
	if roi.X < 0 {
		roi.X = 0
	}
	if roi.Y < 0 {
		roi.Y = 0
	}
	return &Catalog{
		cfg: config.SceneConfig{
			FightScenes:   []string{"fight"},
			EndScenes:     []string{"lobby", "result"},
			HoldScenes:    []string{"vs", "queue", "pick"},
			MinFightScore: 0.70,
			MinOtherScore: 0.70,
			Margin:        0.02,
		},
		mode:     detect.ModeStretch,
		matcher:  match.NCC{},
		fightSet: map[string]bool{"fight": true},
		endSet:   map[string]bool{"lobby": true, "result": true},
		holdSet:  map[string]bool{"vs": true, "queue": true, "pick": true},
		templates: []template{{
			spec: TemplateSpec{
				ID: sceneID + "-mark", Scene: sceneID, ROI: roi, Threshold: 0.70,
			},
			gray: match.ToGray(crop),
			refW: 192, refH: 108,
		}},
	}
}

func paintMark(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			v := uint8(50 + (x*11+y*5)%140)
			img.SetRGBA(x, y, color.RGBA{R: 20, G: v, B: 200 - v/4, A: 255})
		}
	}
	for x := r.Min.X; x < r.Max.X; x++ {
		img.SetRGBA(x, r.Min.Y, color.RGBA{R: 250, G: 200, B: 40, A: 255})
		img.SetRGBA(x, r.Max.Y-1, color.RGBA{R: 250, G: 200, B: 40, A: 255})
	}
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}
