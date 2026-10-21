package config

import "fmt"

// Upper (and missing lower) bounds for numbers that size per-frame work or
// become durations. Each was schema-valid before and broke the capture path
// with no message: vision.sampleHeightReferencePx 1e9 panicked the engine
// ("makeslice: len out of range") on the first fight frame, referenceHeight 1
// multiplied the bead samples 1080x (~16 s per frame), and pollIntervalMs 1e13
// overflowed time.Duration so capture busy-spun. Every value inside the bounds
// analyses a frame in well under 100 ms. Unused legacy fields are not bounded.
const (
	maxSampleReferencePX = 64
	minReferenceWidth    = 400 // The smallest capture the engine analyses (400x250).
	minReferenceHeight   = 250
	maxReferenceSide     = 16384
	maxIntervalMS        = 60_000
)

func validateRanges(c Config) error {
	for _, v := range []struct {
		name     string
		value    float64
		min, max float64
	}{
		{"vision.sampleWidthReferencePx", c.Vision.SampleWidthReferencePX, 0, maxSampleReferencePX},
		{"vision.sampleHeightReferencePx", c.Vision.SampleHeightReferencePX, 0, maxSampleReferencePX},
		{"vision.coreScale", c.Vision.CoreScale, 0, 1},
	} {
		if !(v.value > v.min && v.value <= v.max) {
			return fmt.Errorf("%s: must be in (%g,%g], got %g", v.name, v.min, v.max, v.value)
		}
	}
	for _, v := range []struct {
		name          string
		value, lo, hi int
	}{
		{"layout.referenceWidth", c.Layout.ReferenceWidth, minReferenceWidth, maxReferenceSide},
		{"layout.referenceHeight", c.Layout.ReferenceHeight, minReferenceHeight, maxReferenceSide},
		{"capture.timeoutMs", c.Capture.TimeoutMS, 1, maxIntervalMS},
		{"ui.pollIntervalMs", c.UI.PollIntervalMS, 1, maxIntervalMS},
		{"ui.idlePollIntervalMs", c.UI.IdlePollIntervalMS, 1, maxIntervalMS},
	} {
		if v.value < v.lo || v.value > v.hi {
			return fmt.Errorf("%s: must be in [%d,%d], got %d", v.name, v.lo, v.hi, v.value)
		}
	}
	return nil
}
