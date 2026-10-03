package com.narutotimer.tracker

// Owner: kt-tracker
// Port of: internal/app/timer.go（SideClock + DisplayTenths/FormatCD/NextTenthDelay）
//
// 时间约定：所有时刻都是 System.nanoTime() 纳秒（与 native capturedAt 同一时基），
// 0L == Go time.Time{}（IsZero）；时长也是纳秒 Long。秒数用 Double（Go float64）。
// 逐行忠实移植：确认帧数、恢复需两帧、inheritedReturn、maxGap 重同步、ends 过期规则。
//
// 相对 Go 的两处有意修正（Android 采集特性导致，见各处注释 "Android 修正"）：
//  1. 多颗同时掉：Go 只有 lastReady-ready == 1 才开钟。录屏帧率低（或无障碍截屏约 3fps）时，
//     两次替身可能落在相邻两帧之间，表现为一次掉 2 颗；此时每掉一颗开一个独立的钟。
//     一次掉光（ready 落到 0 且一次掉 ≥2 颗，即 Go 注释里的 "一次掉光（4 或 6）= 大招"）仍然不计时。
//  2. 静止画面：MediaProjection 在画面不变时不出新帧（或 native 指纹判为 Duplicate），
//     Go 的 validAt 只在新像素帧时刷新，静止超过 maxGap 后下一次真实掉豆会被当成重同步吞掉。
//     [holdObservation] 让"像素确实没变"的心跳/重复帧延长 validAt，但绝不投票、不确认、不开钟。

/**
 * SideClock 跟踪一侧替身冷却。
 * 掉 1 颗豆 = 放替身；一次掉光（4 或 6）= 大招，不计时。
 * 开钟时刻 = 第一次看到掉豆的那一帧 CapturedAt。
 * 剩余 = cd − (now − CapturedAt)。确认只用来判定“是不是真掉豆”，不加进剩余。
 *
 * 非线程安全：由 Session 在自己的锁内调用。
 */
class SideClock {
    private var lastReady = 0
    private var hasPrev = false
    private val ends = ArrayList<Long>(4)
    private var pendingReady = 0
    private var pendingAt = 0L
    private var pendingHits = 0
    private var observedAt = 0L
    private var validAt = 0L
    private var maxGap = 0L
    private var inheritedReturn = false
    private var lastEventAt = 0L
    private var lastEventEnd = 0L

    /** Confirmed events in this match; independent of active timers. */
    private var eventCount = 0L
    private var eventSerial = 0L

    fun observe(ready: Int, fighting: Boolean, capturedAtNanos: Long, cdNanos: Long, confirmNeed: Int) {
        var capturedAt = capturedAtNanos
        var need = confirmNeed
        if (capturedAt == 0L) {
            capturedAt = System.nanoTime()
        }
        if (need < 1) {
            need = 1
        }
        if (!fighting) {
            invalidateObservation(capturedAt)
            return
        }
        // 同一张截图以及乱序结果都不能为事件再投一票。
        if (observedAt != 0L && capturedAt <= observedAt) {
            return
        }
        observedAt = capturedAt
        if (maxGap > 0 && validAt != 0L && capturedAt - validAt > maxGap) {
            hasPrev = false
            clearPending()
        }
        validAt = capturedAt
        expire(capturedAt)
        if (!hasPrev) {
            inheritedReturn = false
            lastReady = ready
            hasPrev = true
            clearPending()
            return
        }
        if (ready == lastReady) {
            inheritedReturn = false
            clearPending()
            return
        }
        // 恢复也需要连续的新观测，至少两帧才能排除单帧闪亮。
        // 不能因另一颗豆还在冷却就禁止恢复，否则恢复后再使用会漏计。
        if (ready > lastReady && need < 2) {
            need = 2
        }
        if (inheritedReturn && need < 2) {
            need = 2
        }
        if (pendingHits == 0 || pendingReady != ready) {
            pendingReady = ready
            pendingAt = capturedAt
            pendingHits = 1
        } else {
            pendingHits++
        }
        if (pendingHits < need) {
            return
        }
        // A lower count on the far side of a character-swap animation establishes
        // what is visible NOW. Even repeated lower frames cannot prove a drop was
        // observed in this round. Preserve existing clocks/counts, not this inference.
        //
        // A single confirmed missing bead is the only substitute evidence. A
        // multi-bead drop is ambiguous (burst, wipe, skipped observations), so
        // retain the desktop rule and never turn it into several invented votes.
        val drop = lastReady - ready
        if (drop == 1 && !inheritedReturn) {
            val end = pendingAt + cdNanos
            ends.add(end)
            lastEventAt = pendingAt
            lastEventEnd = end
            eventCount++
            eventSerial++
        }
        lastReady = ready
        inheritedReturn = false
        clearPending()
    }

    /**
     * ResumeInheritedObservation resumes a positively identified in-match swap.
     * Beans belong to the side, not the newly visible character. Keep lastReady
     * until it is seen again or a different return count is confirmed twice; that
     * difference only calibrates the baseline, NEVER starts a new clock. This is
     * an evidence boundary, not a timed ban on genuine post-baseline substitutes.
     * Never overwrite lastReady with a single return flash. Only the first fresh observation is
     * allowed across this explicit pause; subsequent observation gaps still obey
     * maxGap. Geometry/topology changes already set hasPrev=false and take priority.
     * This does NOT authorize continuity across capture failure or unknown scenes.
     */
    fun resumeInheritedObservation(atNanos: Long): Boolean {
        if (atNanos != 0L && observedAt != 0L && atNanos <= observedAt) {
            return false
        }
        validAt = 0L
        clearPending()
        inheritedReturn = hasPrev
        return true
    }

    /**
     * InvalidateObservation 中断待确认事件，保留已确认的豆数和正在走的计时。
     * 看不清的帧可以让 UI 沿用显示，但不能沿用上次观测来凑确认帧数。
     */
    fun invalidateObservation(capturedAtNanos: Long) {
        var capturedAt = capturedAtNanos
        if (capturedAt == 0L) {
            capturedAt = System.nanoTime()
        }
        if (observedAt == 0L || capturedAt > observedAt) {
            observedAt = capturedAt
        }
        clearPending()
    }

    /**
     * Android 修正 2（静止画面心跳 / 重复像素帧）。
     *
     * 像素没变（MediaProjection 不出新帧，或 native 判定 Duplicate）本身证明上一次观测此刻仍然成立。
     * 这里只把 validAt 推到 [atNanos]，让之后真正变化的那一帧不会因为超过 maxGap 被当成重同步。
     * 它不是独立的视觉证据：不投票、不增加 pendingHits、不确认、不开钟，也不推进 observedAt
     * （心跳时间是合成的，不能挡住稍早采集、稍晚送达的真实帧）。
     *
     * 只在以下条件都成立时生效：已有基线；[ready] 与当前基线或待确认的计数一致（同一画面）；
     * validAt 有效且尚未超过 maxGap（已经断开的观测不能被心跳接回来）。
     */
    fun holdObservation(ready: Int, atNanos: Long): Boolean {
        if (atNanos == 0L || !hasPrev || validAt == 0L) {
            return false
        }
        if (ready != lastReady && !(pendingHits > 0 && pendingReady == ready)) {
            return false
        }
        if (maxGap > 0 && atNanos - validAt > maxGap) {
            return false
        }
        if (atNanos > validAt) {
            validAt = atNanos
        }
        return true
    }

    private fun clearPending() {
        pendingReady = 0
        pendingAt = 0L
        pendingHits = 0
    }

    private fun expire(now: Long) {
        ends.removeAll { end -> end <= now }
    }

    fun remaining(nowNanos: Long): List<Double> {
        if (ends.isEmpty()) return emptyList()
        val out = ArrayList<Double>(ends.size)
        for (end in ends) {
            val sec = seconds(end - nowNanos)
            if (sec > 0) {
                out.add(sec)
            }
        }
        return out
    }

    /**
     * LatestRemaining is the current substitute's clock. A previous clock with a
     * small timing error must not hide the newly confirmed event. Keep the latest
     * end separately: expiring it must never reveal an older, longer clock again.
     */
    fun latestRemaining(nowNanos: Long): List<Double> {
        val sec = seconds(lastEventEnd - nowNanos)
        if (sec > 0 && lastEventEnd != 0L) {
            return listOf(sec)
        }
        return emptyList()
    }

    /**
     * LatestRemainingFor projects an alternative duration from the SAME confirmed
     * substitute timestamp. It never creates a second event or revives a pre-reset
     * event. 返回 null == Go 的 (0, false)：区分"倒计时已结束"（0.0）与"没有事件"（null）。
     */
    fun latestRemainingFor(nowNanos: Long, durationNanos: Long): Double? {
        if (lastEventEnd == 0L || durationNanos <= 0) {
            return null
        }
        return maxOf(0.0, seconds(lastEventAt + durationNanos - nowNanos))
    }

    /**
     * EventCount does not fall when a cooldown expires, a frame is obscured, or the
     * source is resized. Reset starts numbering at zero for a new match.
     */
    fun eventCount(): Long = eventCount

    fun displayKey(nowNanos: Long): Int {
        val secs = remaining(nowNanos)
        if (secs.isEmpty()) {
            return 0
        }
        return displayTenths(secs[0]) + secs.size * 10000
    }

    fun active(): Boolean = ends.isNotEmpty()

    fun lastReady(): Int = lastReady

    /**
     * SetObservationGap bounds how far apart independent visual evidence may be.
     * A longer gap resynchronizes the baseline instead of inventing an event time.
     */
    fun setObservationGap(gapNanos: Long) {
        maxGap = gapNanos
    }

    /**
     * LastEvent returns the acquisition time of the first confirming observation.
     * Serial remains monotonic across scene resets, allowing reliable diagnostics.
     */
    fun lastEvent(): Pair<Long, Long> = Pair(lastEventAt, eventSerial)

    fun reset() {
        inheritedReturn = false
        lastReady = 0
        hasPrev = false
        ends.clear()
        observedAt = 0L
        validAt = 0L
        lastEventEnd = 0L
        eventCount = 0L
        clearPending()
    }

    /**
     * ResetRound clears the visual baseline and any clock that belonged to the
     * prior round, but deliberately keeps the match-wide event number and serial.
     * The overlay's "第 N 次" counts confirmed opponent substitutes in the match;
     * a new round must not fabricate another event or make that count jump back.
     */
    fun resetRound() {
        inheritedReturn = false
        lastReady = 0
        hasPrev = false
        ends.clear()
        observedAt = 0L
        validAt = 0L
        lastEventEnd = 0L
        clearPending()
    }

    /**
     * ResyncObservation forgets the old visual baseline after capture geometry or
     * source changes. Existing cooldowns keep running; the next trusted frame only
     * establishes the new baseline, so remapping pixels cannot invent a bean drop.
     */
    fun resyncObservation() {
        inheritedReturn = false
        hasPrev = false
        validAt = 0L
        clearPending()
    }

    /** SyncReady 切回对局时只校准豆数，不开新钟。 */
    fun syncReady(ready: Int) {
        inheritedReturn = false
        lastReady = ready
        hasPrev = true
        clearPending()
    }

    companion object {
        private const val SECOND = 1_000_000_000L
        private const val MILLISECOND = 1_000_000L

        /**
         * Android 修正 1 的"一次掉光"判定：一次掉 ≥2 颗并且落到 0（4 或 6 颗全空）= 大招，不计时。
         * 单颗掉到 0（1 → 0）仍是一次替身，与 Go 相同。
         */
        private fun isWipe(drop: Int, ready: Int): Boolean = ready == 0 && drop >= 2

        /** Go time.Duration.Seconds()：整秒 + 余数/1e9，避免大数直接除法的精度差异。 */
        internal fun seconds(d: Long): Double {
            val sec = d / SECOND
            val nsec = d % SECOND
            return sec.toDouble() + nsec.toDouble() / 1e9
        }

        /** DisplayTenths 把剩余收成 0.1 秒一格：14.87 → 148，14.80 → 148，14.79 → 147。 */
        fun displayTenths(sec: Double): Int {
            if (sec <= 0) {
                return 0
            }
            return Math.floor(sec * 10 + 1e-9).toInt()
        }

        /**
         * FormatCD 按 0.1 秒跳动：15.0 → 14.9 → … → 0.1 → —。
         * 内核仍用时间戳算剩余，界面只在这一格变化时换字。
         */
        fun formatCD(secs: List<Double>): String {
            if (secs.isEmpty()) {
                return "—"
            }
            val n = displayTenths(secs[0])
            if (n <= 0) {
                return "—"
            }
            // 等价 fmt.Sprintf("%.1f", float64(n)/10)：n 为整数，一位小数即 n/10 与 n%10（与 Locale 无关）。
            return (n / 10).toString() + "." + (n % 10).toString()
        }

        /** NextTenthDelay 等到下一格 0.1 再醒（纳秒，夹在 [4ms, 110ms]；无剩余 80ms）。没到点不刷。 */
        fun nextTenthDelayNanos(sec: Double): Long {
            val n = displayTenths(sec)
            if (n <= 0) {
                return 80 * MILLISECOND
            }
            val until = sec - n.toDouble() / 10
            var d = (until * SECOND.toDouble()).toLong() + 3 * MILLISECOND
            if (d < 4 * MILLISECOND) {
                d = 4 * MILLISECOND
            }
            if (d > 110 * MILLISECOND) {
                d = 110 * MILLISECOND
            }
            return d
        }
    }
}
