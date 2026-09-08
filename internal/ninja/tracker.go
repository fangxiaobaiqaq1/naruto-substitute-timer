package ninja

import (
	"image"
	"sync"
	"time"

	"narutotimer/internal/match"
)

// Tracker keeps a name-search off the per-frame hot path. Once a name is found,
// every frame rechecks only those exact pixels (+/-1px), not the entire corner.
// A failed recheck immediately returns unknown. The old template LOCATION may
// remain a bounded search hint, never an identity vote: a returning name must
// pass the original current-pixel threshold again on that frame.
type Tracker struct {
	mu          sync.Mutex
	reader      *Reader
	hint        evidence
	bounds, roi image.Rectangle
	scale       float64
	retryAfter  time.Time
	lastAt      time.Time
	verifiedAt  time.Time
}

func (t *Tracker) Read(reader *Reader, img *image.RGBA, roi image.Rectangle, scale float64, now time.Time) Readout {
	if reader == nil || img == nil {
		return Readout{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := t.reader != reader || t.bounds != img.Bounds() || t.roi != roi || t.scale != scale || (!t.lastAt.IsZero() && now.Before(t.lastAt))
	t.lastAt = now
	if changed {
		t.hint = evidence{}
		t.retryAfter = time.Time{}
		t.verifiedAt = time.Time{}
	}
	t.reader, t.bounds, t.roi, t.scale = reader, img.Bounds(), roi, scale
	unavailable := func() Readout {
		if t.hint.Name != "" && t.hint.Slots > 0 && now.Sub(t.verifiedAt) <= time.Second {
			return Readout{Slots: t.hint.Slots, Unverified: true}
		}
		return Readout{}
	}
	if t.hint.Name != "" {
		region := t.hint.rect.Inset(-1).Intersect(roi).Intersect(img.Bounds())
		if !region.Empty() {
			view := img.SubImage(region).(*image.RGBA)
			score, err := (match.NCC{}).Match(match.Query{Image: view, ROI: region, Prepared: t.hint.template})
			if err == nil && score.Value >= .80 {
				t.hint.rect = t.hint.rect.Add(score.Peak.Sub(t.hint.rect.Min))
				t.hint.Score = score.Value
				t.verifiedAt = now
				return t.hint.Readout
			}
		}
		if now.Sub(t.verifiedAt) > time.Second {
			t.hint = evidence{}
		}
	}
	// Unknown characters are normal. Keep trying at a bounded rate without
	// delaying every bead sample. A stable hint is rechecked on every frame, but
	// repeated visible/occluded flicker must NOT trigger a full scan every time.
	if now.Before(t.retryAfter) {
		return unavailable()
	}
	t.retryAfter = now.Add(500 * time.Millisecond)
	found := reader.read(img, roi, scale)
	if found.Name != "" {
		t.hint = found
		t.verifiedAt = now
		return found.Readout
	}
	// In particular, never return the retained hint on this failed frame.
	return unavailable()
}
