package sequence

import (
	"math"
	"sort"

	"narutotimer/internal/domain"
)

type Config struct {
	Allowed                  [][]domain.BeadState
	UnknownPenalty           float64
	MinimumDecodedConfidence float64
}

type Result struct {
	States     []domain.BeadState
	Count      *int
	Confidence float64
	Accepted   bool
}

type candidate struct {
	states []domain.BeadState
	cost   float64
}

func Decode(observations []domain.BeadObservation, cfg Config) Result {
	if len(observations) == 0 || len(cfg.Allowed) == 0 {
		return Result{}
	}
	for _, o := range observations {
		if o.State == domain.BeadUnknown {
			// 有一颗没看清就别猜成合法前缀，否则 ready 会假掉 1。
			return Result{}
		}
	}
	candidates := make([]candidate, 0, len(cfg.Allowed))
	for _, allowed := range cfg.Allowed {
		if len(allowed) != len(observations) {
			continue
		}
		cost := 0.0
		for i, state := range allowed {
			probability := probabilityFor(observations[i], state)
			if observations[i].State == domain.BeadUnknown {
				cost += cfg.UnknownPenalty
			}
			cost -= math.Log(math.Max(probability, 1e-9))
		}
		states := append([]domain.BeadState(nil), allowed...)
		candidates = append(candidates, candidate{states: states, cost: cost})
	}
	if len(candidates) == 0 {
		return Result{}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].cost < candidates[j].cost })
	best := candidates[0]
	confidence := math.Exp(-best.cost / float64(len(observations)))
	if len(candidates) > 1 {
		margin := 1 - math.Exp(-(candidates[1].cost - best.cost))
		confidence *= margin
	}
	if confidence < cfg.MinimumDecodedConfidence {
		return Result{Confidence: confidence}
	}
	count := 0
	for _, state := range best.states {
		if state == domain.BeadLight {
			count++
		}
	}
	return Result{States: best.states, Count: &count, Confidence: confidence, Accepted: true}
}

func probabilityFor(observation domain.BeadObservation, state domain.BeadState) float64 {
	switch state {
	case domain.BeadLight:
		return observation.Scores.Light
	case domain.BeadDark:
		return observation.Scores.Dark
	case domain.BeadGone:
		return observation.Scores.Gone
	default:
		return observation.Scores.Invalid
	}
}
