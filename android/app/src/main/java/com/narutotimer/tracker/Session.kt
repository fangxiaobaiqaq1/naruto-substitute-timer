package com.narutotimer.tracker

import com.narutotimer.AnalysisResult
import com.narutotimer.FrameBead
import com.narutotimer.Settings
import kotlin.math.max

/** Native-free Android port of the desktop session state machine. */
data class OverlayState(
    val revision: Long,
    val tagText: String,
    val tagColor: Int,
    /** Explicit player-side mode shown in the Android pill. */
    val sideModeText: String,
    val primaryText: String,
    val primaryColor: Int,
    val eventText: String,
    val dual: Boolean,
    val altText: String,
    val altColor: Int,
    val leftText: String,
    val leftColor: Int,
    val rightText: String,
    val rightColor: Int,
    val infoText: String,
    val showBothSides: Boolean,
    val mini: Boolean,
    val fontScale: Float,
    val opacity: Float,
)

object TimerColors {
    const val CLOCK_IDLE = 0xFFB8F4FF.toInt()
    const val CLOCK_LIVE = 0xFF7EEBFF.toInt()
    const val CLOCK_WARN = 0xFF4DA3FF.toInt()
    const val TAG_MINE = 0xFFB8F4FF.toInt()
    const val TAG_IDLE = 0xFF7ED2E6.toInt()
    const val GLASS_BG = 0xFF061820.toInt()
    const val PANEL_BG = 0xFF0A1622.toInt()
    const val PANEL_BTN = 0xFF123044.toInt()
    const val PANEL_HOVER = 0xFF1C4862.toInt()
    const val PANEL_BORDER = 0xFF388CA8.toInt()
    const val MINI_CONTROLS_BG = 0xE6061820.toInt()
}

class Session(
    private val settings: Settings,
    private val playerNamesSink: (List<String>) -> Unit = {},
) {
    private val lock = Any()
    private val leftClock = SideClock()
    private val rightClock = SideClock()

    private var isFighting = false
    private var fightHits = 0
    private var leaveHits = 0
    private var lastFrame: AnalysisResult? = null
    private var playerSideState = if (settings.rememberSide) {
        settings.playerSide.takeIf { it == "left" || it == "right" } ?: ""
    } else ""
    private var autoSide = ""
    private var opponentName = ""
    private var leftNinja = ""
    private var rightNinja = ""
    private var leftNinjaCandidate = ""
    private var rightNinjaCandidate = ""

    private var lastCaptured = 0L
    private var scene = ""
    private var holdFreeze = false
    private var inheritOnReturn = false
    private var syncLeft = false
    private var syncRight = false
    private var roundOpeningActive = false
    private var roundOpeningSeenAt = 0L
    private var roundBaselineLeft = false
    private var roundBaselineRight = false

    private var sourceWidth = 0
    private var sourceHeight = 0
    private var sourceMethod = ""
    private var layoutProfile = ""
    private var leftSlots = 0
    private var rightSlots = 0
    private var lastBeadAt = 0L
    private var cadenceEma = 0L

    private var revisionState = 0L
    @Volatile var revision: Long = 0L
        private set

    val fighting: Boolean get() = synchronized(lock) { isFighting }
    val side: String get() = synchronized(lock) { effectiveSideLocked() }

    fun onFrame(f: AnalysisResult) {
        synchronized(lock) {
            // capturedAt is acquisition time, not analysis completion time. It is
            // the only timestamp allowed to establish event ordering or a clock.
            if (f.capturedAtNanos > 0 && lastCaptured > 0 && f.capturedAtNanos <= lastCaptured) return
            if (f.capturedAtNanos > 0) lastCaptured = f.capturedAtNanos
            lastFrame = f
            scene = f.scene

            val sourceChanged = updateSourceLocked(f)
            updateIdentityLocked(f)
            updateTopologyLocked(f)

            // Hold/error frames retain the display but never supply exit evidence,
            // bead votes, or a replacement for a missing fresh pixel frame.
            if (f.hold || f.err != null) {
                leaveHits = 0
                invalidateLocked(f.capturedAtNanos)
                if (isHoldScene(f.scene)) {
                    inheritOnReturn = f.scene == "vs" && isFighting
                    holdFreeze = true
                }
                bump()
                return
            }

            observeRoundOpeningLocked(f)
            applySceneLocked(f)
            if (sourceChanged) {
                // Geometry/source changes invalidate only the visual baseline. Any
                // already-confirmed cooldown continues from its acquisition time.
                leftClock.resyncObservation()
                rightClock.resyncObservation()
                syncLeft = false
                syncRight = false
            }

            if (!isFighting || !f.fighting) {
                invalidateLocked(f.capturedAtNanos)
                bump()
                return
            }

            if (roundOpeningActive) {
                roundBaselineLeft = true
                roundBaselineRight = true
            }
            observeBeadsLocked(f)
            bump()
        }
    }

    /**
     * Kept for the frame-loop API. A timer heartbeat has no newly acquired
     * pixels, so it cannot refresh validAt, add a confirmation vote, or extend
     * continuity across a capture blackout.
     */
    fun onHeartbeat(atNanos: Long = System.nanoTime()) {
        synchronized(lock) {
            // Deliberately no-op. Do not manufacture visual evidence from the
            // previous AnalysisResult, regardless of the supplied synthetic time.
        }
    }

    fun pollIntervalMs(): Int = synchronized(lock) {
        max(16, if (isFighting) settings.pollIntervalMs else settings.idlePollIntervalMs)
    }

    fun nextTickDelayNanos(nowNanos: Long): Long = synchronized(lock) {
        val values = ArrayList<Double>()
        opponentRemainingLocked(nowNanos)?.let { values += it }
        if (settings.showBothSides) {
            values += leftClock.latestRemaining(nowNanos)
            values += rightClock.latestRemaining(nowNanos)
        }
        if (values.isEmpty()) 200_000_000L
        else SideClock.nextTenthDelayNanos(values.minOrNull() ?: 0.0)
    }

    fun overlayState(nowNanos: Long): OverlayState = synchronized(lock) {
        val opp = opponentRemainingLocked(nowNanos) ?: emptyList()
        val left = leftClock.latestRemaining(nowNanos)
        val right = rightClock.latestRemaining(nowNanos)
        val player = effectiveSideLocked()
        val opponentNinja = opponentNinjaLocked()
        val candidate = when (player) {
            "left" -> rightNinjaCandidate
            "right" -> leftNinjaCandidate
            else -> ""
        }
        val shownNinja = opponentNinja.ifEmpty { candidate }
        val opponentSide = if (player == "left") "右" else if (player == "right") "左" else ""
        val tag = if (shownNinja.isNotEmpty() && opponentSide.isNotEmpty()) {
            "对面·$opponentSide · ${NinjaRules.shortLabel(shownNinja)}"
        } else if (opponentSide.isNotEmpty()) {
            "对面·$opponentSide"
        } else "对面·待认边"
        val dual = NinjaRules.dualCooldown(opponentNinja)
        val alt = if (!dual) "" else {
            val d = opponentClockLocked()?.latestRemainingFor(nowNanos, NinjaRules.ALTERNATE_COOLDOWN_NANOS)
            when {
                d == null -> "—"
                d <= 0.0 -> "就绪"
                else -> SideClock.formatCD(listOf(d))
            }
        }
        val event = when (player) {
            "left" -> "第 ${rightClock.eventCount()} 次"
            "right" -> "第 ${leftClock.eventCount()} 次"
            else -> "左${leftClock.eventCount()}次 / 右${rightClock.eventCount()}次"
        }
        val currentBeads = lastFrame?.frameBeads.orEmpty()
        val modeText = when (settings.playerSide) {
            "left" -> "我方·左"
            "right" -> "我方·右"
            else -> effectiveSideLocked().let {
                if (it == "left") "我方·自动·左" else if (it == "right") "我方·自动·右" else "我方·自动·待认边"
            }
        }
        OverlayState(
            revision, tag, if (opponentNinja.isEmpty()) TimerColors.TAG_IDLE else TimerColors.TAG_MINE,
            modeText, SideClock.formatCD(opp), clockColor(opp), event, dual, alt,
            if (alt == "—" || alt == "就绪" || alt.isEmpty()) TimerColors.CLOCK_IDLE else TimerColors.CLOCK_LIVE,
            SideClock.formatCD(left), clockColor(left), SideClock.formatCD(right), clockColor(right),
            "${sceneName(scene, layoutProfile)} · ${when {
                fHoldLocked() -> "待识别"
                isFighting -> "对局"
                else -> "待机"
            }}  豆 左${visibleReady(currentBeads, true)}/右${visibleReady(currentBeads, false)}",
            settings.showBothSides, settings.overlayMode == "mini", settings.fontScale,
            if (settings.overlayMode == "mini") settings.miniOpacity else settings.windowOpacity,
        )
    }

    fun swapSide() {
        synchronized(lock) {
            when (effectiveSideLocked()) {
                "left" -> lockSideLocked("right")
                "right" -> {
                    settings.playerSide = "auto"
                    playerSideState = autoSide
                    bump()
                }
                else -> lockSideLocked("left")
            }
        }
    }

    fun lockSide(side: String) {
        synchronized(lock) { if (side == "left" || side == "right") lockSideLocked(side) }
    }

    fun selectSideMode(mode: String) {
        synchronized(lock) {
            when (mode.lowercase()) {
                "left", "左", "左边" -> lockSideLocked("left")
                "right", "右", "右边" -> lockSideLocked("right")
                else -> {
                    settings.playerSide = "auto"
                    playerSideState = autoSide
                }
            }
            bump()
        }
    }

    fun setRememberSide(remember: Boolean) {
        synchronized(lock) {
            settings.rememberSide = remember
            if (!remember) {
                settings.playerSide = "auto"
                playerSideState = autoSide
            }
            bump()
        }
    }

    fun applySettings(names: List<String>, ninjaQuery: String, sideMode: String, remember: Boolean) {
        synchronized(lock) {
            settings.playerNames = names
            settings.ninjaQuery = GoText.trimSpace(ninjaQuery)
            settings.rememberSide = remember
            playerNamesSink(names)
            selectSideMode(sideMode)
            if (!remember && sideMode.lowercase() !in setOf("left", "右", "right", "左", "左边", "右边")) {
                settings.playerSide = "auto"
            }
            bump()
        }
    }

    fun setShowBothSides(on: Boolean) { synchronized(lock) { settings.showBothSides = on; bump() } }
    fun setMiniMode(on: Boolean) { synchronized(lock) { settings.overlayMode = if (on) "mini" else "full"; bump() } }
    fun resyncObservation() {
        synchronized(lock) {
            leftClock.resyncObservation()
            rightClock.resyncObservation()
            syncLeft = false
            syncRight = false
            roundBaselineLeft = false
            roundBaselineRight = false
            bump()
        }
    }

    private fun updateSourceLocked(f: AnalysisResult): Boolean {
        val changed = sourceWidth != 0 && (
            sourceWidth != f.width || sourceHeight != f.height || sourceMethod != f.captureMethod
        )
        if (sourceWidth == 0 || sourceChangedMeaningful(f)) {
            sourceWidth = f.width
            sourceHeight = f.height
            sourceMethod = f.captureMethod
        }
        // A layout profile is a different HUD/match, not a bead drop.
        if (f.layoutProfile.isNotEmpty() && layoutProfile.isNotEmpty() && f.layoutProfile != layoutProfile) {
            leftClock.reset()
            rightClock.reset()
            isFighting = false
            fightHits = 0
            leaveHits = 0
            holdFreeze = false
            inheritOnReturn = false
            clearRoundOpeningLocked()
            syncLeft = false
            syncRight = false
        }
        if (f.layoutProfile.isNotEmpty()) layoutProfile = f.layoutProfile
        return changed
    }

    private fun sourceChangedMeaningful(f: AnalysisResult): Boolean =
        sourceWidth != f.width || sourceHeight != f.height || sourceMethod != f.captureMethod

    private fun updateIdentityLocked(f: AnalysisResult) {
        // Candidate labels are display-only. They never enter the confirmed ninja
        // policy (in particular, they cannot enable the special 10-second clock).
        leftNinja = f.leftNinja
        rightNinja = f.rightNinja
        leftNinjaCandidate = f.leftNinjaCandidate
        rightNinjaCandidate = f.rightNinjaCandidate
        if (f.oppName.isNotEmpty()) opponentName = f.oppName

        if (f.err != null) return
        if (f.scene.isNotEmpty() && f.scene != "fight" && f.scene != "vs") {
            autoSide = ""
            if (!manualSideLocked()) playerSideState = ""
            opponentName = ""
            return
        }
        if (f.playerSide != "left" && f.playerSide != "right") return
        val mine = f.playerName.isEmpty() || settings.playerNames.isEmpty() || settings.playerNames.any {
            GoText.normalizeName(it) == GoText.normalizeName(f.playerName)
        }
        if (!mine) return
        autoSide = f.playerSide
        if (settings.playerSide != "left" && settings.playerSide != "right") {
            playerSideState = f.playerSide
            if (f.playerName.isNotEmpty()) opponentName = f.oppName
        }
    }

    private fun updateTopologyLocked(f: AnalysisResult) {
        val left = f.slots(true, settings.beadsPerSide)
        val right = f.slots(false, settings.beadsPerSide)
        if (leftSlots != 0 && leftSlots != left) leftClock.resyncObservation()
        if (rightSlots != 0 && rightSlots != right) rightClock.resyncObservation()
        leftSlots = left
        rightSlots = right
    }

    private fun observeRoundOpeningLocked(f: AnalysisResult) {
        val at = f.capturedAtNanos.takeIf { it > 0 } ?: return
        if (f.fighting && f.roundOpening) {
            if (!roundOpeningActive) beginVerifiedRoundLocked()
            roundOpeningActive = true
            roundOpeningSeenAt = at
            roundBaselineLeft = true
            roundBaselineRight = true
            return
        }
        if (f.hold || f.err != null || !f.fighting || !roundOpeningActive) return
        if (at - roundOpeningSeenAt <= ROUND_OPENING_MARKER_GAP_NANOS) return
        roundOpeningActive = false
        roundBaselineLeft = true
        roundBaselineRight = true
    }

    private fun applySceneLocked(f: AnalysisResult) {
        if (isHoldScene(f.scene)) {
            inheritOnReturn = f.scene == "vs" && isFighting
            holdFreeze = true
            return
        }
        if (f.fighting) {
            if (holdFreeze) {
                syncLeft = true
                syncRight = true
                holdFreeze = false
            }
            applyFightLocked(true)
        } else if (isEndScene(f.scene)) {
            applyFightLocked(false)
        }
    }

    private fun applyFightLocked(raw: Boolean) {
        val enter = settings.enterFightFrames.coerceAtLeast(1)
        val leave = settings.leaveFightFrames.coerceAtLeast(10)
        if (raw) {
            leaveHits = 0
            fightHits++
            if (!isFighting && fightHits >= enter) isFighting = true
        } else {
            fightHits = 0
            if (isFighting) {
                leaveHits++
                if (leaveHits >= leave) {
                    isFighting = false
                    leftClock.reset()
                    rightClock.reset()
                    clearRoundOpeningLocked()
                    holdFreeze = false
                    inheritOnReturn = false
                    syncLeft = false
                    syncRight = false
                }
            }
        }
    }

    private fun observeBeadsLocked(f: AnalysisResult) {
        if (f.duplicate) return
        val at = f.capturedAtNanos
        if (at <= 0L) {
            invalidateLocked(at)
            return
        }
        val interval = if (lastBeadAt > 0 && at > lastBeadAt) at - lastBeadAt else 0L
        if (interval > 0) cadenceEma = if (cadenceEma <= 0) interval else cadenceEma * 3 / 4 + interval / 4
        lastBeadAt = at
        var gap = settings.pollIntervalMs.toLong() * 3L * 1_000_000L
        if (cadenceEma > 0) gap = max(gap, cadenceEma * 3)
        gap = gap.coerceIn(150_000_000L, 8_000_000_000L)
        leftClock.setObservationGap(gap)
        rightClock.setObservationGap(gap)

        val beads = f.frameBeads
        val lc = readyCount(beads, true)
        val rc = readyCount(beads, false)
        val leftGood = sideObservable(beads, true) && sideBeads(beads, true).size == expectedSlotsLocked(true)
        val rightGood = sideObservable(beads, false) && sideBeads(beads, false).size == expectedSlotsLocked(false)

        if (roundBaselineLeft) {
            if (leftGood) {
                leftClock.syncReady(lc)
                syncLeft = false
                roundBaselineLeft = roundOpeningActive
            } else leftClock.invalidateObservation(at)
        } else if (leftGood) {
            if (syncLeft) {
                syncLeft = if (inheritOnReturn) !leftClock.resumeInheritedObservation(at) else {
                    leftClock.syncReady(lc); false
                }
            }
            leftClock.observe(lc, true, at, NinjaRules.DEFAULT_COOLDOWN_NANOS, settings.minimumConfirmFrames)
        } else leftClock.invalidateObservation(at)

        if (roundBaselineRight) {
            if (rightGood) {
                rightClock.syncReady(rc)
                syncRight = false
                roundBaselineRight = roundOpeningActive
            } else rightClock.invalidateObservation(at)
        } else if (rightGood) {
            if (syncRight) {
                syncRight = if (inheritOnReturn) !rightClock.resumeInheritedObservation(at) else {
                    rightClock.syncReady(rc); false
                }
            }
            rightClock.observe(rc, true, at, NinjaRules.DEFAULT_COOLDOWN_NANOS, settings.minimumConfirmFrames)
        } else rightClock.invalidateObservation(at)
    }

    private fun invalidateLocked(at: Long) {
        leftClock.invalidateObservation(at)
        rightClock.invalidateObservation(at)
    }

    private fun beginVerifiedRoundLocked() {
        leftClock.resetRound()
        rightClock.resetRound()
        syncLeft = false
        syncRight = false
        inheritOnReturn = false
        holdFreeze = false
        roundBaselineLeft = true
        roundBaselineRight = true
    }

    private fun clearRoundOpeningLocked() {
        roundOpeningActive = false
        roundOpeningSeenAt = 0L
        roundBaselineLeft = false
        roundBaselineRight = false
    }

    private fun sideBeads(beads: List<FrameBead>, left: Boolean): List<FrameBead> = beads.filter {
        if (it.label.isEmpty()) false else it.label[0].uppercaseChar() == if (left) 'L' else 'R'
    }

    private fun sideObservable(beads: List<FrameBead>, left: Boolean): Boolean {
        val side = sideBeads(beads, left)
        if (side.isEmpty()) return false
        return side.all {
            !it.unknown && (it.lit || it.gold || it.dark) && !(it.dark && (it.lit || it.gold))
        }
    }

    private fun readyCount(beads: List<FrameBead>, left: Boolean): Int = sideBeads(beads, left).count {
        !it.unknown && (it.lit || it.gold) && !it.dark
    }

    private fun expectedSlotsLocked(left: Boolean): Int {
        val n = if (left) leftSlots else rightSlots
        return if (n == 4 || n == 6) n else settings.beadsPerSide
    }

    private fun visibleReady(beads: List<FrameBead>, left: Boolean): String {
        val side = sideBeads(beads, left)
        if (!sideObservable(beads, left) || side.size != expectedSlotsLocked(left)) return "?"
        val n = readyCount(beads, left)
        return if (expectedSlotsLocked(true) == 6 || expectedSlotsLocked(false) == 6) "$n/${expectedSlotsLocked(left)}" else n.toString()
    }

    private fun effectiveSideLocked(): String {
        if (settings.rememberSide && (settings.playerSide == "left" || settings.playerSide == "right")) return settings.playerSide
        return playerSideState.ifEmpty { autoSide }
    }

    private fun manualSideLocked(): Boolean = settings.playerSide == "left" || settings.playerSide == "right"

    private fun lockSideLocked(s: String) {
        settings.playerSide = s
        playerSideState = s
        bump()
    }

    private fun opponentClockLocked(): SideClock? = when (effectiveSideLocked()) {
        "left" -> rightClock
        "right" -> leftClock
        else -> null
    }

    private fun opponentRemainingLocked(now: Long): List<Double>? = opponentClockLocked()?.latestRemaining(now)

    private fun opponentNinjaLocked(): String {
        val override = GoText.trimSpace(settings.ninjaQuery)
        if (override.isNotEmpty()) return override
        return when (effectiveSideLocked()) {
            "left" -> rightNinja
            "right" -> leftNinja
            else -> ""
        }
    }

    private fun clockColor(v: List<Double>): Int = if (v.isEmpty()) TimerColors.CLOCK_IDLE
    else if (SideClock.displayTenths(v[0]) <= 30) TimerColors.CLOCK_WARN else TimerColors.CLOCK_LIVE

    private fun fHoldLocked(): Boolean = lastFrame?.let { it.hold || it.err != null } == true

    private fun isHoldScene(s: String): Boolean = s.isNotEmpty() && s in settings.holdScenes
    private fun isEndScene(s: String): Boolean = s.isNotEmpty() && s in settings.endScenes
    private fun sceneName(s: String, profile: String) = when (s) {
        "fight" -> if (profile == "camp") "训" else "决"
        "result" -> "结"
        "lobby" -> "厅"
        "vs" -> "VS"
        "queue" -> "匹"
        "pick" -> "选"
        "ban" -> "禁"
        else -> s.ifEmpty { "待识别" }
    }

    private fun bump() {
        revisionState++
        revision = revisionState
    }

    companion object {
        private const val ROUND_OPENING_MARKER_GAP_NANOS = 250_000_000L
    }
}
