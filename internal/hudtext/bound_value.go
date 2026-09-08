package hudtext

import (
	"image"
	"strings"
	"time"
)

// A blank/noisy OCR result from retrying the other side is not evidence that
// these CURRENT, still-matching glyphs changed. Preserve their verified value,
// not a clock-based cache. A real glyph/scene/geometry change clears it before
// this function is called. Initial acquisition still needs two OCR results.
func observeBound(state *valueState, proof **glyphEvidence, value string, incoming *glyphEvidence, img *image.RGBA, at time.Time, unchanged bool) {
	validProof := incoming != nil && incoming.matches(img)
	if *proof != nil && (*proof).matches(img) {
		if state.accepted != "" {
			// A stable base must not prevent later exact version recognition.
			// Upgrade only with two independently captured, currently matching
			// full-version results; never downgrade or guess a version.
			if strings.HasPrefix(value, state.accepted+"[") && validProof {
				accepted := state.accepted
				state.observe(value, at)
				if state.accepted == "" {
					state.accepted = accepted
				} else if state.accepted == value {
					*proof = incoming
				}
			}
			return
		}
		if value == "" {
			return
		}
	}
	if (incoming != nil && !validProof) || (incoming == nil && !unchanged) {
		return
	}
	keep := state.candidate == value && *proof != nil
	state.observe(value, at)
	if value == "" {
		*proof = nil
	} else if !keep {
		if validProof {
			*proof = incoming
		} else {
			*proof = nil
		}
	}
}
