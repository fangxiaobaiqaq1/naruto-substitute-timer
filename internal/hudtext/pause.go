package hudtext

import (
	"image"
	"time"

	"narutotimer/internal/engine"
)

const textPauseTTL = 2 * time.Second

// A remembered fight with a short observation hold is not a new scene. Keep
// only ALREADY accepted fields whose current pixels still validate. No OCR
// fields are emitted during the hold, nor may pending/late responses confirm
// across it. Explicit pages, geometry, disable and long holds invalidate all.
// Called under e.mu; does not capture, encode, spawn or wait for a worker.
func (e *Engine) pauseText(img *image.RGBA, res engine.Result, at time.Time) bool {
	if e.closed || !e.enabled || !e.haveKey || img == nil || !res.Uncertain || res.Scene != "fight" {
		return false
	}
	if !e.pausedAt.IsZero() && at.Sub(e.pausedAt) > textPauseTTL {
		return false
	}
	regions, ok := nameRegions(img, e.cfg.Layout, res.LayoutProfile)
	if !ok || e.key != (frameKey{bounds: img.Bounds(), regions: regions, profile: res.LayoutProfile}) {
		return false
	}
	if e.pausedAt.IsZero() {
		e.pausedAt = at
		e.generation++ // In-flight work belongs to the pre-hold generation.
		for i := range e.sides {
			s := &e.sides[i]
			retainAccepted(&s.ninja, &s.ninjaProof)
			retainAccepted(&s.account, &s.accountProof)
			s.candidate = valueState{}
		}
	}
	e.validateTextPixels(img, regions)
	return true
}

func retainAccepted(state *valueState, proof **glyphEvidence) {
	if state.accepted == "" {
		*state, *proof = valueState{}, nil
		return
	}
	*state = valueState{candidate: state.accepted, accepted: state.accepted, hits: 2, at: state.at}
}

func (e *Engine) validateTextPixels(img *image.RGBA, regions [2]image.Rectangle) ([2][32]byte, [2]int) {
	var signatures [2][32]byte
	var white [2]int
	for i, roi := range regions {
		signatures[i], white[i] = lettering(img, roi)
		s := &e.sides[i]
		changed := signatures[i] != s.signature
		if (s.ninjaProof != nil && !s.ninjaProof.matches(img)) || (s.ninjaProof == nil && changed) {
			s.ninja = valueState{}
			s.ninjaProof = nil
		}
		if (s.accountProof != nil && !s.accountProof.matches(img)) || (s.accountProof == nil && changed) {
			s.account = valueState{}
			s.accountProof = nil
		}
		if changed {
			s.candidate = valueState{}
		}
		s.signature = signatures[i]
	}
	return signatures, white
}
