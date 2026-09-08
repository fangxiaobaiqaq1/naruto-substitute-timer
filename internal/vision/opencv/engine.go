//go:build opencv

// Package opencv 用本机精简 OpenCV（core/imgproc）做替身豆检测。
// 金色视为已充能可用，亮蓝视为可用，暗青视为冷却。
// 场景门闩负责是否进战斗，并通过 Prefer 选择对应的豆位标定。
package opencv

import (
	"fmt"
	"image"
	"math"
	"sync"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/domain"
	"narutotimer/internal/engine"
	"narutotimer/internal/sequence"
	"narutotimer/internal/vision/cvbind"
)

type Engine struct {
	layout engine.LayoutProvider
	cfg    config.Config
	mu     sync.Mutex
	prefer string
}

func New(layout engine.LayoutProvider, cfg config.Config) *Engine {
	if cfg.SchemaVersion == 0 {
		cfg = config.Default()
	}
	return &Engine{layout: layout, cfg: cfg}
}

// Prefer keeps the last explicitly identified HUD profile until an end scene.
func (e *Engine) Prefer(kind string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if kind == "camp" || kind == "duel" {
		e.prefer = kind
	} else {
		e.prefer = ""
	}
}

func (e *Engine) positions(w, h int) ([]detect.BeadPosition, string) {
	prefer := e.prefer
	if p, ok := e.layout.(engine.ProfiledLayout); ok {
		if manual := p.PreferredProfile(); manual == "camp" || manual == "duel" {
			prefer = manual
		}
		if prefer == "" {
			prefer = "camp"
		}
		return p.PositionsFor(prefer, w, h), prefer
	}
	if prefer == "duel" {
		return detect.Layout(w, h, e.layout.Mode(), detect.DuelBeads()), prefer
	}
	return e.layout.Positions(w, h), "camp"
}

func (e *Engine) Analyze(img *image.RGBA) engine.Result {
	res := engine.Result{Name: "opencv"}
	if img == nil {
		return res
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	positions, profile := e.positions(w, h)
	area := detect.ComputeContentArea(w, h, e.layout.Mode())
	reference := image.Pt(detect.LogicWidth, detect.LogicHeight)
	if layout, ok := e.layout.(*engine.ConfiguredLayout); ok {
		var supported bool
		area, supported = layout.ContentArea(img)
		if !supported {
			res.Uncertain = true
			res.Scene = "unsupported-resolution"
			return res
		}
		positions = layout.PositionsIn(profile, area)
		reference = layout.ReferenceSize()
	}
	res.Name += "-" + profile
	res.LayoutProfile = profile
	if len(positions) == 0 {
		return res
	}
	xs := make([]int, len(positions))
	ys := make([]int, len(positions))
	for i, p := range positions {
		// The native matrix begins at Pix[0], regardless of Go image origin.
		xs[i] = p.X - img.Bounds().Min.X
		ys[i] = p.Y - img.Bounds().Min.Y
	}

	scaleX := float64(area.W) / float64(reference.X)
	scaleY := float64(area.H) / float64(reference.Y)
	sampleW := int(math.Round(e.cfg.Vision.SampleWidthReferencePX * scaleX))
	sampleH := int(math.Round(e.cfg.Vision.SampleHeightReferencePX * scaleY))
	if sampleW < 5 {
		sampleW = 5
	}
	if sampleH < 7 {
		sampleH = 7
	}

	samples, err := cvbind.AnalyzeBeads(img, xs, ys, sampleW, sampleH, 0, e.cfg.Vision)
	if err != nil {
		res.Name = "opencv-error"
		return res
	}

	type sidePack struct {
		side    engine.Side
		pos     []detect.BeadPosition
		obs     []domain.BeadObservation
		samples []cvbind.Sample
	}
	packs := []sidePack{{side: engine.Left}, {side: engine.Right}}
	for i, p := range positions {
		obs := classifySample(p.Idx, samples[i], e.cfg.Vision)
		if p.Side == "right" {
			packs[1].pos = append(packs[1].pos, p)
			packs[1].obs = append(packs[1].obs, obs)
			packs[1].samples = append(packs[1].samples, samples[i])
		} else {
			packs[0].pos = append(packs[0].pos, p)
			packs[0].obs = append(packs[0].obs, obs)
			packs[0].samples = append(packs[0].samples, samples[i])
		}
	}

	seqCfg := sequence.Config{
		Allowed:                  e.cfg.Sequence.Allowed,
		UnknownPenalty:           e.cfg.Sequence.UnknownPenalty,
		MinimumDecodedConfidence: e.cfg.Sequence.MinimumDecodedConfidence,
	}

	decoded := make([]sequence.Result, len(packs))
	for i, pack := range packs {
		if len(pack.obs) == 0 {
			continue
		}
		decoded[i] = sequence.Decode(pack.obs, seqCfg)
	}

	for pi, pack := range packs {
		if len(pack.pos) == 0 {
			continue
		}
		dec := decoded[pi]
		states := dec.States
		if !dec.Accepted || hasUnknownObs(pack.obs) {
			// A plausible prefix must not bypass the decoder's confidence gate.
			states = nil
		}
		for i, p := range pack.pos {
			state := domain.BeadUnknown
			if i < len(states) {
				state = states[i]
			}
			sample := pack.samples[i]
			gold := sample.Gold >= sample.Light && sample.Gold > sample.Dark &&
				state == domain.BeadLight
			res.Beads = append(res.Beads, engine.BeadInfo{
				X:       sample.X + img.Bounds().Min.X,
				Y:       sample.Y + img.Bounds().Min.Y,
				Label:   label(pack.side, p.Idx),
				Lit:     state == domain.BeadLight,
				Gold:    gold,
				Unknown: state == domain.BeadUnknown,
				Conf:    pack.obs[i].Confidence,
			})
		}
	}
	return res
}

func classifySample(idx int, s cvbind.Sample, cfg config.VisionConfig) domain.BeadObservation {
	total := float64(s.Light + s.Dark + s.Gold + s.Other)
	if total < 1 {
		total = 1
	}
	scores := domain.BeadScores{
		Light:   float64(s.Light+s.Gold) / total,
		Dark:    float64(s.Dark) / total,
		Invalid: float64(s.Other) / total,
	}
	charged := scores.Light
	best := scores.Dark
	state := domain.BeadDark
	if charged > best && charged-scores.Dark >= 0.18 && charged >= 0.55 {
		best = charged
		state = domain.BeadLight
	}
	if scores.Invalid > best {
		best = scores.Invalid
		state = domain.BeadUnknown
	}
	second := scores.Dark
	switch state {
	case domain.BeadDark:
		second = math.Max(charged, scores.Invalid)
	case domain.BeadLight:
		second = math.Max(scores.Dark, scores.Invalid)
	default:
		second = math.Max(charged, scores.Dark)
	}
	if best < cfg.UnknownBelow || best-second < cfg.MinimumMargin {
		state = domain.BeadUnknown
	}
	return domain.BeadObservation{
		Index:      idx,
		Center:     domain.Point{X: float64(s.X), Y: float64(s.Y)},
		State:      state,
		Confidence: best,
		Coverage:   (float64(s.Light+s.Dark+s.Gold) / total),
		Scores:     scores,
	}
}

func hasUnknownObs(obs []domain.BeadObservation) bool {
	for _, o := range obs {
		if o.State == domain.BeadUnknown {
			return true
		}
	}
	return false
}

func label(side engine.Side, idx int) string {
	prefix := "L"
	if side == engine.Right {
		prefix = "R"
	}
	return fmt.Sprintf("%s%d", prefix, idx+1)
}
