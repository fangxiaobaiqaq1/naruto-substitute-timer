package app

import "time"

const (
	minObservationGap = 150 * time.Millisecond
	maxObservationGap = 8 * time.Second
)

// ObservationCadence turns the real capture cadence into SideClock's
// observation gap: the longest interval between two observations that still
// counts as continuous evidence. It must scale with the real cadence: engine
// work can push frame intervals far past the poll interval, and a fixed bound
// would then treat every frame as a discontinuity, so a real drop could never
// gather its confirmation frames.
//
// Feed it every delivered frame. A frame with an acquisition time (holds and
// duplicates included) is a cadence sample, so an occlusion is many short
// intervals rather than one long one, and the tolerance does not depend on
// earlier cut-ins. A frame without one (failed capture) breaks the chain: the
// interval across an outage is not a sample either. The live session and the
// timer-lab replay share this rule.
type ObservationCadence struct {
	last time.Time
	ema  time.Duration
}

// Next returns the gap for the frame captured at `at`, then folds the frame's
// interval into the cadence. The gap comes from the cadence BEFORE this frame,
// so a long interval cannot widen its own tolerance: it is 3x the smoothed
// cadence (at least 3x poll and 150 ms, at most 8 s).
func (c *ObservationCadence) Next(at time.Time, poll time.Duration) time.Duration {
	gap := min(max(3*poll, 3*c.ema, minObservationGap), maxObservationGap)
	if at.IsZero() {
		c.last = time.Time{}
		return gap
	}
	if c.last.IsZero() || at.After(c.last) {
		if !c.last.IsZero() {
			interval := at.Sub(c.last)
			if c.ema <= 0 {
				c.ema = interval
			} else {
				c.ema = c.ema*3/4 + interval/4
			}
		}
		c.last = at
	}
	return gap
}
