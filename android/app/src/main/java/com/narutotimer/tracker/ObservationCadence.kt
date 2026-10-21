package com.narutotimer.tracker

// Port of: internal/app/cadence.go (ObservationCadence, audit GE-02).
// Times are System.nanoTime() nanoseconds, 0L == Go time.Time{} (IsZero), durations nanosecond
// Longs, exactly as in SideClock.kt. Go/Kotlin parity: android/tools/sessioncheck/run.sh.

/**
 * Turns the real capture cadence into SideClock's observation gap: the longest interval between
 * two observations that still counts as continuous evidence. It must scale with the real cadence:
 * analysis can push frame intervals far past the poll interval, and a fixed bound would then treat
 * every frame as a discontinuity, so a real drop could never gather its confirmation frames.
 *
 * Feed it every delivered frame. A frame with an acquisition time (holds and duplicates included)
 * is a cadence sample, so an occlusion is many short intervals rather than one long one, and the
 * tolerance does not depend on earlier cut-ins. A frame without one (failed capture) breaks the
 * chain: the interval across an outage is not a sample either. Not thread-safe: Session calls it
 * under its lock.
 */
class ObservationCadence {
    private var last = 0L
    private var ema = 0L

    /**
     * Returns the gap for the frame captured at [atNanos], then folds the frame's interval into the
     * cadence. The gap comes from the cadence BEFORE this frame, so a long interval cannot widen its
     * own tolerance: it is 3x the smoothed cadence (at least 3x [pollNanos] and 150 ms, at most 8 s).
     */
    fun next(atNanos: Long, pollNanos: Long): Long {
        val gap = minOf(maxOf(3 * pollNanos, 3 * ema, MIN_GAP_NANOS), MAX_GAP_NANOS)
        if (atNanos == 0L) {
            last = 0L
            return gap
        }
        if (last == 0L || atNanos > last) {
            if (last != 0L) {
                val interval = atNanos - last
                ema = if (ema <= 0L) interval else ema * 3 / 4 + interval / 4
            }
            last = atNanos
        }
        return gap
    }

    companion object {
        const val MIN_GAP_NANOS = 150_000_000L
        const val MAX_GAP_NANOS = 8_000_000_000L
    }
}
