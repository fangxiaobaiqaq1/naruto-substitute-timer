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
			return Readout{Slots: t.hint.Slots, RowOffsetY: t.hint.RowOffsetY, Unverified: true, PaletteHint: t.hint.Palette}
		}
		return Readout{}
	}
	if t.hint.Name != "" {
		region := t.hint.rect.Inset(-1).Intersect(roi).Intersect(img.Bounds())
		if !region.Empty() {
			view := img.SubImage(region).(*image.RGBA)
			score, err := (match.NCC{}).Match(match.Query{Image: view, ROI: region, Prepared: t.hint.template})
			if err == nil && score.Value >= .80 {
				// A special template stays double-gated: the stored pixels AND
				// the fixed portrait must both be current. A kept title alone
				// may never keep an energy-gauge geometry alive.
				if t.hint.portrait == nil || itachiPortraitEvidence(img, scale, t.hint.portrait, t.hint.portraitSize) {
					t.hint.rect = t.hint.rect.Add(score.Peak.Sub(t.hint.rect.Min))
					t.hint.Score = score.Value
					t.verifiedAt = now
					// A verified title clears any old failed-search backoff so a
					// subsequent occlusion still gets its bounded full-ROI search.
					t.retryAfter = time.Time{}
					return t.hint.Readout
				}
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
	// A verified title clears any old failed-search backoff. Therefore the
	// first failed current-pixel recheck performs one bounded full-ROI search:
	// a title that moved with the current HUD may still be recognized on this
	// frame. A failed search starts the backoff; later failed rechecks cannot
	// turn rapid occlusion/effect flicker into repeated scans.
	t.retryAfter = now.Add(500 * time.Millisecond)
	found := reader.read(img, roi, scale)
	if found.Name != "" {
		t.hint = found
		t.verifiedAt = now
		t.retryAfter = time.Time{}
		return found.Readout
	}
	// In particular, never return the retained hint on this failed frame.
	return unavailable()
}

// ReadWithAvatar keeps title and portrait evidence independent until a current
// frame resolves them. It deliberately does not reuse title/portrait identity
// from prior frames; Tracker still provides only the existing bounded geometry
// hint and never a name.
func (t *Tracker) ReadWithAvatar(reader *Reader, avatar *AvatarTracker, img *image.RGBA, titleROI, avatarROI image.Rectangle, scale float64, now time.Time) Readout {
	title := t.Read(reader, img, titleROI, scale, now)
	return reader.ResolveEvidence(img, titleROI, avatarROI, scale, title, avatar, now)
}
