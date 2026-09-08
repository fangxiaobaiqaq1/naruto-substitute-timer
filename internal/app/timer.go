package app

import (
	"fmt"
	"math"
	"time"
)

// SideClock 跟踪一侧替身冷却。
// 掉 1 颗豆 = 放替身；一次掉光（4 或 6）= 大招，不计时。
// 开钟时刻 = 第一次看到掉豆的那一帧 CapturedAt。
// 剩余 = cd − (now − CapturedAt)。确认只用来判定“是不是真掉豆”，不加进剩余。
type SideClock struct {
	lastReady       int
	hasPrev         bool
	ends            []time.Time
	pendingReady    int
	pendingAt       time.Time
	pendingHits     int
	observedAt      time.Time
	validAt         time.Time
	maxGap          time.Duration
	inheritedReturn bool
	lastEventAt     time.Time
	lastEventEnd    time.Time
	eventCount      uint64 // Confirmed events in this match; independent of active timers.
	eventSerial     uint64
}

func (c *SideClock) Observe(ready int, fighting bool, capturedAt time.Time, cd time.Duration, confirmNeed int) {
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	if confirmNeed < 1 {
		confirmNeed = 1
	}
	if !fighting {
		c.InvalidateObservation(capturedAt)
		return
	}
	// 同一张截图以及乱序结果都不能为事件再投一票。
	if !c.observedAt.IsZero() && !capturedAt.After(c.observedAt) {
		return
	}
	c.observedAt = capturedAt
	if c.maxGap > 0 && !c.validAt.IsZero() && capturedAt.Sub(c.validAt) > c.maxGap {
		c.hasPrev = false
		c.clearPending()
	}
	c.validAt = capturedAt
	c.expire(capturedAt)
	if !c.hasPrev {
		c.inheritedReturn = false
		c.lastReady = ready
		c.hasPrev = true
		c.clearPending()
		return
	}
	if ready == c.lastReady {
		c.inheritedReturn = false
		c.clearPending()
		return
	}
	// 恢复也需要连续的新观测，至少两帧才能排除单帧闪亮。
	// 不能因另一颗豆还在冷却就禁止恢复，否则恢复后再使用会漏计。
	if ready > c.lastReady && confirmNeed < 2 {
		confirmNeed = 2
	}
	if c.inheritedReturn && confirmNeed < 2 {
		confirmNeed = 2
	}
	if c.pendingHits == 0 || c.pendingReady != ready {
		c.pendingReady = ready
		c.pendingAt = capturedAt
		c.pendingHits = 1
	} else {
		c.pendingHits++
	}
	if c.pendingHits < confirmNeed {
		return
	}
	// A lower count on the far side of a character-swap animation establishes
	// what is visible NOW. Even repeated lower frames cannot prove a drop was
	// observed in this round. Preserve existing clocks/counts, not this inference.
	if c.lastReady-ready == 1 && !c.inheritedReturn {
		c.ends = append(c.ends, c.pendingAt.Add(cd))
		c.lastEventAt = c.pendingAt
		c.lastEventEnd = c.pendingAt.Add(cd)
		c.eventCount++
		c.eventSerial++
	}
	c.lastReady = ready
	c.inheritedReturn = false
	c.clearPending()
}

// ResumeInheritedObservation resumes a positively identified in-match swap.
// Beans belong to the side, not the newly visible character. Keep lastReady
// until it is seen again or a different return count is confirmed twice; that
// difference only calibrates the baseline, NEVER starts a new clock. This is
// an evidence boundary, not a timed ban on genuine post-baseline substitutes.
// Never overwrite lastReady with a single return flash. Only the first fresh observation is
// allowed across this explicit pause; subsequent observation gaps still obey
// maxGap. Geometry/topology changes already set hasPrev=false and take priority.
// This does NOT authorize continuity across capture failure or unknown scenes.
func (c *SideClock) ResumeInheritedObservation(at time.Time) bool {
	if !at.IsZero() && !c.observedAt.IsZero() && !at.After(c.observedAt) {
		return false
	}
	c.validAt = time.Time{}
	c.clearPending()
	c.inheritedReturn = c.hasPrev
	return true
}

// InvalidateObservation 中断待确认事件，保留已确认的豆数和正在走的计时。
// 看不清的帧可以让 UI 沿用显示，但不能沿用上次观测来凑确认帧数。
func (c *SideClock) InvalidateObservation(capturedAt time.Time) {
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	if c.observedAt.IsZero() || capturedAt.After(c.observedAt) {
		c.observedAt = capturedAt
	}
	c.clearPending()
}

func (c *SideClock) clearPending() {
	c.pendingReady = 0
	c.pendingAt = time.Time{}
	c.pendingHits = 0
}

func (c *SideClock) expire(now time.Time) {
	alive := c.ends[:0]
	for _, end := range c.ends {
		if end.After(now) {
			alive = append(alive, end)
		}
	}
	c.ends = alive
}

func (c *SideClock) Remaining(now time.Time) []float64 {
	out := make([]float64, 0, len(c.ends))
	for _, end := range c.ends {
		sec := end.Sub(now).Seconds()
		if sec > 0 {
			out = append(out, sec)
		}
	}
	return out
}

// LatestRemaining is the current substitute's clock. A previous clock with a
// small timing error must not hide the newly confirmed event. Keep the latest
// end separately: expiring it must never reveal an older, longer clock again.
func (c *SideClock) LatestRemaining(now time.Time) []float64 {
	if sec := c.lastEventEnd.Sub(now).Seconds(); sec > 0 && !c.lastEventEnd.IsZero() {
		return []float64{sec}
	}
	return nil
}

// LatestRemainingFor projects an alternative duration from the SAME confirmed
// substitute timestamp. It never creates a second event or revives a pre-reset
// event. The boolean distinguishes a completed countdown from no event at all.
func (c *SideClock) LatestRemainingFor(now time.Time, duration time.Duration) (float64, bool) {
	if c.lastEventEnd.IsZero() || duration <= 0 {
		return 0, false
	}
	return max(0, c.lastEventAt.Add(duration).Sub(now).Seconds()), true
}

// EventCount does not fall when a cooldown expires, a frame is obscured, or the
// source is resized. Reset starts numbering at zero for a new match.
func (c *SideClock) EventCount() uint64 { return c.eventCount }

func (c *SideClock) DisplayKey(now time.Time) int {
	secs := c.Remaining(now)
	if len(secs) == 0 {
		return 0
	}
	return DisplayTenths(secs[0]) + len(secs)*10000
}

func (c *SideClock) Active() bool {
	return len(c.ends) > 0
}

func (c *SideClock) LastReady() int { return c.lastReady }

// SetObservationGap bounds how far apart independent visual evidence may be.
// A longer gap resynchronizes the baseline instead of inventing an event time.
func (c *SideClock) SetObservationGap(d time.Duration) { c.maxGap = d }

// LastEvent returns the acquisition time of the first confirming observation.
// Serial remains monotonic across scene resets, allowing reliable diagnostics.
func (c *SideClock) LastEvent() (time.Time, uint64) { return c.lastEventAt, c.eventSerial }

func (c *SideClock) Reset() {
	c.inheritedReturn = false
	c.lastReady = 0
	c.hasPrev = false
	c.ends = c.ends[:0]
	c.observedAt = time.Time{}
	c.validAt = time.Time{}
	c.lastEventEnd = time.Time{}
	c.eventCount = 0
	c.clearPending()
}

// ResyncObservation forgets the old visual baseline after capture geometry or
// source changes. Existing cooldowns keep running; the next trusted frame only
// establishes the new baseline, so remapping pixels cannot invent a bean drop.
func (c *SideClock) ResyncObservation() {
	c.inheritedReturn = false
	c.hasPrev = false
	c.validAt = time.Time{}
	c.clearPending()
}

// SyncReady 切回对局时只校准豆数，不开新钟。
func (c *SideClock) SyncReady(ready int) {
	c.inheritedReturn = false
	c.lastReady = ready
	c.hasPrev = true
	c.clearPending()
}

// DisplayTenths 把剩余收成 0.1 秒一格：14.87 → 148，14.80 → 148，14.79 → 147。
func DisplayTenths(sec float64) int {
	if sec <= 0 {
		return 0
	}
	return int(math.Floor(sec*10 + 1e-9))
}

// FormatCD 按 0.1 秒跳动：15.0 → 14.9 → … → 0.1 → —。
// 内核仍用时间戳算剩余，界面只在这一格变化时换字。
func FormatCD(secs []float64) string {
	if len(secs) == 0 {
		return "—"
	}
	n := DisplayTenths(secs[0])
	if n <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", float64(n)/10)
}

// NextTenthDelay 等到下一格 0.1 再醒。没到点不刷。
func NextTenthDelay(sec float64) time.Duration {
	n := DisplayTenths(sec)
	if n <= 0 {
		return 80 * time.Millisecond
	}
	until := sec - float64(n)/10
	d := time.Duration(until*float64(time.Second)) + 3*time.Millisecond
	if d < 4*time.Millisecond {
		d = 4 * time.Millisecond
	}
	if d > 110*time.Millisecond {
		d = 110 * time.Millisecond
	}
	return d
}
