package sequence

import (
	"testing"

	"narutotimer/internal/domain"
)

func TestDecodeAcceptsClearLegalSequence(t *testing.T) {
	observations := []domain.BeadObservation{
		observation(0.98, 0.01),
		observation(0.97, 0.02),
		observation(0.01, 0.98),
		observation(0.02, 0.97),
	}
	result := Decode(observations, testConfig())
	if !result.Accepted || result.Count == nil || *result.Count != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDecodeRejectsIllegalSequenceInsteadOfReordering(t *testing.T) {
	observations := []domain.BeadObservation{
		observation(0.01, 0.98),
		observation(0.98, 0.01),
		observation(0.98, 0.01),
		observation(0.98, 0.01),
	}
	cfg := testConfig()
	cfg.MinimumDecodedConfidence = 0.70
	result := Decode(observations, cfg)
	if result.Accepted {
		t.Fatalf("illegal raw sequence must be rejected, got %+v", result)
	}
	if result.Count != nil || len(result.States) != 0 {
		t.Fatalf("rejected result must not invent a stable sequence: %+v", result)
	}
}

func TestDecodeRejectsAmbiguousUnknown(t *testing.T) {
	observations := []domain.BeadObservation{
		observation(0.50, 0.49),
		observation(0.50, 0.49),
		observation(0.49, 0.50),
		observation(0.49, 0.50),
	}
	for i := range observations {
		observations[i].State = domain.BeadUnknown
	}
	result := Decode(observations, testConfig())
	if result.Accepted {
		t.Fatalf("ambiguous observations must be rejected: %+v", result)
	}
}

func observation(light, dark float64) domain.BeadObservation {
	state := domain.BeadLight
	if dark > light {
		state = domain.BeadDark
	}
	return domain.BeadObservation{
		State:  state,
		Scores: domain.BeadScores{Light: light, Dark: dark, Invalid: 0.01},
	}
}

func testConfig() Config {
	return Config{
		Allowed: [][]domain.BeadState{
			{domain.BeadDark, domain.BeadDark, domain.BeadDark, domain.BeadDark},
			{domain.BeadLight, domain.BeadDark, domain.BeadDark, domain.BeadDark},
			{domain.BeadLight, domain.BeadLight, domain.BeadDark, domain.BeadDark},
			{domain.BeadLight, domain.BeadLight, domain.BeadLight, domain.BeadDark},
			{domain.BeadLight, domain.BeadLight, domain.BeadLight, domain.BeadLight},
		},
		UnknownPenalty:           0.35,
		MinimumDecodedConfidence: 0.70,
	}
}
