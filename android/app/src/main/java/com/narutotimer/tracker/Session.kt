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
    val cardWidth: Float,
    val cardHeight: Float,
)

/** Shared with internal/ui/theme.go: cyan = cooling down, amber = last 3 s. */
object TimerColors {
    const val CLOCK_IDLE = 0xFF9FB9C4.toInt()
    const val CLOCK_LIVE = 0xFF5CE1FF.toInt()
    const val CLOCK_WARN = 0xFFFFB547.toInt()
    const val TAG_MINE = 0xFFE3F6FB.toInt()
    const val TAG_IDLE = 0xFF8FB3C0.toInt()
    const val GLASS_BG = 0xFF061820.toInt()
    const val PANEL_BG = 0xFF0B1A24.toInt()
    const val PANEL_BTN = 0xFF15303F.toInt()
    const val PANEL_HOVER = 0xFF1E4457.toInt()
    const val PANEL_BORDER = 0xFF24485A.toInt()
    const val EXIT_BG = 0xFF3A1E24.toInt()
    const val EXIT_TEXT = 0xFFFF9A8F.toInt()
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
    /** Go s.beads: bead display of the latest fighting, error-free frame; empty otherwise. */
    private var displayBeads: List<FrameBead> = emptyList()
    private var playerSideState = if (settings.rememberSide) {
        settings.playerSide.takeIf { it == "left" || it == "right" } ?: ""
    } else ""

    init {
        // Go restoreSide: without 记住认边 a previous manual side is not restored,
        // so the persisted mode must not keep claiming "我方·左" either.
        if (!settings.rememberSide && settings.playerSide != "auto") settings.playerSide = "auto"
    }
    private var autoSide = ""
    private var leftNinja = ""
    private var rightNinja = ""
    private var leftNinjaCandidate = ""
    private var rightNinjaCandidate = ""

    // Go ninjaDisplay: tag history only, never a cooldown, identity, slot or palette input.
    // Keeps a short name-reading gap from blinking the label. Index 0 = left, 1 = right.
    private val ninjaDisplayName = arrayOf("", "")
    private val ninjaDisplaySeenAt = LongArray(2)
    private var ninjaDisplayAt = 0L

    private var sceneObservedAt = 0L
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
    /** Go s.cadence: the clocks' observation gap from every delivered frame (GE-02). */
    private val cadence = ObservationCadence()

    private var revisionState = 0L
    @Volatile var revision: Long = 0L
        private set

    /**
     * Called after every [revision] change, on the mutating thread with the session lock held, so
     * it must only schedule work (OverlayController posts a refresh while it is not polling).
     */
    @Volatile var revisionListener: (() -> Unit)? = null

    val fighting: Boolean get() = synchronized(lock) { isFighting }
    val side: String get() = synchronized(lock) { effectiveSideLocked() }

    /**
     * Whether the native account-text reader (nt/hudtext.h) should run: only in the automatic
     * side mode and only while 我方 is unknown. Once a side is known it stays until a non-fight,
     * non-VS page (Go maybeAutoSide), so reading on would only cost CPU.
     */
    fun wantsTextRecognition(): Boolean = synchronized(lock) {
        settings.playerSide == "auto" && effectiveSideLocked().isEmpty()
    }

    /** Go session.captureOnce (internal/ui/run.go), step for step. */
    fun onFrame(f: AnalysisResult) {
        synchronized(lock) {
            // capturedAt is acquisition time, not analysis completion time. It is
            // the only timestamp allowed to establish event ordering or a clock. A
            // repeated/older one is still processed: SideClock refuses it a vote and
            // applyScene refuses it exit evidence.
            lastFrame = f
            scene = f.scene
            // Every delivered frame is a cadence sample, holds and duplicates included; a
            // failure frame (no capture time) breaks the chain. The bound for this frame comes
            // from the cadence before it (Go ObservationCadence, GE-02).
            val gap = cadence.next(f.capturedAtNanos, settings.pollIntervalMs.toLong() * 1_000_000L)
            leftClock.setObservationGap(gap)
            rightClock.setObservationGap(gap)
            // Before the hold/error branch: the first frame of a new source may be a
            // hold frame. Failure frames (no image) keep the known source.
            if (updateObservationSpaceLocked(f.width, f.height, f.captureMethod)) {
                // Only the visual baseline is invalid; confirmed cooldowns continue.
                leftClock.resyncObservation()
                rightClock.resyncObservation()
                clearNinjaDisplaysLocked()
            }
            // A verified round-opening marker counts even while the beads are obscured.
            observeRoundOpeningLocked(f)
            // Topology, confirmed ninjas and the bead display come only from fight
            // frames without an error; VS/result/failure frames carry none of them.
            if (f.fighting && f.err == null) {
                updateHudLocked(f)
                displayBeads = f.frameBeads
            } else {
                updateNinjaDisplayLocked(f)
                displayBeads = emptyList()
            }

            // Hold/error frames retain the display but never supply exit evidence,
            // bead votes, or a replacement for a missing fresh pixel frame.
            if (f.hold || f.err != null) {
                if (f.err != null && (holdFreeze || syncLeft || syncRight)) {
                    leftClock.resyncObservation()
                    rightClock.resyncObservation()
                }
                leaveHits = 0
                invalidateLocked(f.capturedAtNanos)
                if (isHoldScene(f.scene)) {
                    inheritOnReturn = f.scene == "vs" && isFighting
                    holdFreeze = true
                }
                maybeAutoSideLocked(f)
                bump()
                return
            }

            if (f.fighting && f.layoutProfile.isNotEmpty()) {
                if (layoutProfile.isNotEmpty() && layoutProfile != f.layoutProfile) {
                    // A different HUD belongs to a different match, never a bead drop.
                    leftClock.reset()
                    rightClock.reset()
                    isFighting = false
                    holdFreeze = false
                    clearRoundOpeningLocked()
                    fightHits = 0
                    leaveHits = 0
                    syncLeft = false
                    syncRight = false
                }
                layoutProfile = f.layoutProfile
            }
            applySceneLocked(f)
            maybeAutoSideLocked(f)

            if (!isFighting || !f.fighting) {
                invalidateLocked(f.capturedAtNanos)
                bump()
                return
            }
            observeBeadsLocked(f)
            bump()
        }
    }

    /**
     * A new capture run started (FrameLoop.start): the time since the previous run's last frame is
     * an outage, not a capture interval. Go sees an outage as failed captures, which break the
     * cadence chain the same way (ObservationCadence.Next without a capture time).
     */
    fun onCaptureStart() {
        synchronized(lock) { cadence.next(0L, 0L) }
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
        // Go refreshOverlay: the confirmed name only while the fight is readable (or the
        // user's override), else the recent display (cleared on VS/end), else the candidate.
        val confirmed = if (GoText.trimSpace(settings.ninjaQuery).isEmpty() && (fHoldLocked() || !isFighting)) "" else opponentNinja
        val shownConfirmed = confirmed.ifEmpty { recentOpponentDisplayLocked() }
        val candidate = when (player) {
            "left" -> rightNinjaCandidate
            "right" -> leftNinjaCandidate
            else -> ""
        }
        val shownNinja = shownConfirmed.ifEmpty { candidate }
        val opponentSide = if (player == "left") "右" else if (player == "right") "左" else ""
        val tag = if (shownNinja.isNotEmpty() && opponentSide.isNotEmpty()) {
            "对面·$opponentSide · ${NinjaRules.shortLabel(shownNinja)}"
        } else if (opponentSide.isNotEmpty()) {
            "对面·$opponentSide"
        } else "对面·待认边"
        // Go dualClockText: no 10 s clock while the opponent side is unknown.
        val opponentClock = opponentClockLocked()
        val dual = opponentClock != null && NinjaRules.dualCooldown(opponentNinja)
        val altRemaining = if (dual) opponentClock?.latestRemainingFor(nowNanos, NinjaRules.ALTERNATE_COOLDOWN_NANOS) else null
        val alt = when {
            !dual -> ""
            altRemaining == null -> "—"
            altRemaining <= 0.0 -> "就绪"
            else -> SideClock.formatCD(listOf(altRemaining))
        }
        val altColor = if (altRemaining != null && altRemaining > 0.0) clockColor(listOf(altRemaining)) else TimerColors.CLOCK_IDLE
        val event = when (player) {
            "left" -> "第 ${rightClock.eventCount()} 次"
            "right" -> "第 ${leftClock.eventCount()} 次"
            else -> "左${leftClock.eventCount()}次 / 右${rightClock.eventCount()}次"
        }
        val currentBeads = displayBeads
        // Missing visual evidence must not stop an already-confirmed cooldown.
        // Without a clock, expose why we are waiting instead of the pill's generic "—".
        val opponentLeft = player == "right"
        val primary = when {
            opp.isNotEmpty() -> SideClock.formatCD(opp)
            lastFrame == null -> "待采集"
            fHoldLocked() -> "待识别"
            !isFighting || lastFrame?.fighting != true -> "待机"
            player.isEmpty() -> "待认边"
            !sideObservable(currentBeads, opponentLeft) ||
                sideBeads(currentBeads, opponentLeft).size != expectedSlotsLocked(opponentLeft) -> "待识别"
            else -> "待替身"
        }
        val modeText = when (settings.playerSide) {
            "left" -> "我方·左"
            "right" -> "我方·右"
            else -> effectiveSideLocked().let {
                if (it == "left") "我方·自动·左" else if (it == "right") "我方·自动·右" else "我方·自动·待认边"
            }
        }
        OverlayState(
            revision, tag, if (shownConfirmed.isEmpty()) TimerColors.TAG_IDLE else TimerColors.TAG_MINE,
            modeText, primary, clockColor(opp), event, dual, alt, altColor,
            SideClock.formatCD(left), clockColor(left), SideClock.formatCD(right), clockColor(right),
            "${sceneName(scene, layoutProfile)} · ${when {
                fHoldLocked() -> "待识别"
                isFighting -> "对局"
                else -> "待机"
            }}  豆 左${visibleReady(currentBeads, true)}/右${visibleReady(currentBeads, false)}",
            settings.showBothSides, settings.overlayMode == "mini", settings.fontScale,
            if (settings.overlayMode == "mini") settings.miniOpacity else settings.windowOpacity,
            settings.cardWidth, settings.cardHeight,
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
    /**
     * A new region calibration reads the same screen with new geometry. Like a
     * frame-source change, only the visual baseline is invalidated; confirmed
     * cooldowns continue.
     */
    fun resyncGeometry() {
        synchronized(lock) {
            leftClock.resyncObservation()
            rightClock.resyncObservation()
            syncLeft = false
            syncRight = false
            clearNinjaDisplaysLocked()
            bump()
        }
    }

    /**
     * Go app.ObservationSpace.Update: a change only when a known field changes.
     * Missing sizes or method (failure frames) neither count nor erase the source. The method
     * counts only for a frame with an image (GE-09): a frame without pixels describes no
     * coordinate system, so a failure naming another method is no source change; a real switch
     * is seen at the first image of the new method.
     */
    private fun updateObservationSpaceLocked(width: Int, height: Int, method: String): Boolean {
        if (width <= 0 || height <= 0) return false
        var changed = sourceWidth > 0 && sourceHeight > 0 && (sourceWidth != width || sourceHeight != height)
        sourceWidth = width
        sourceHeight = height
        if (method.isNotEmpty()) {
            changed = changed || (sourceMethod.isNotEmpty() && sourceMethod != method)
            sourceMethod = method
        }
        return changed
    }

    /** Go updateHUD: fighting frames without an error only. */
    private fun updateHudLocked(f: AnalysisResult) {
        if (layoutProfile.isNotEmpty() && f.layoutProfile.isNotEmpty() && layoutProfile != f.layoutProfile) {
            clearNinjaDisplaysLocked()
        }
        updateNinjaDisplayLocked(f)
        val left = f.slots(true, settings.beadsPerSide)
        val right = f.slots(false, settings.beadsPerSide)
        if (leftSlots != 0 && leftSlots != left) leftClock.resyncObservation()
        if (rightSlots != 0 && rightSlots != right) rightClock.resyncObservation()
        leftSlots = left
        rightSlots = right
        // Candidate labels are display-only. They never enter the confirmed ninja
        // policy (in particular, they cannot enable the special 10-second clock).
        leftNinja = f.leftNinja
        rightNinja = f.rightNinja
        leftNinjaCandidate = f.leftNinjaCandidate
        rightNinjaCandidate = f.rightNinjaCandidate
    }

    /** Go maybeAutoSide. */
    private fun maybeAutoSideLocked(f: AnalysisResult) {
        if (f.err != null) return
        if (f.scene.isNotEmpty() && f.scene != "fight" && f.scene != "vs") {
            autoSide = ""
            if (!manualSideLocked()) playerSideState = ""
            return
        }
        if (f.playerSide != "left" && f.playerSide != "right") return
        // A read name must be one of mine (none configured: not mine). A frame in
        // flight when the account changed is not current evidence.
        if (f.playerName.isNotEmpty() && settings.playerNames.none {
                GoText.normalizeName(it) == GoText.normalizeName(f.playerName)
            }) return
        // Remembered under a manual lock so switching back to auto applies at once.
        autoSide = f.playerSide
        if (!manualSideLocked()) playerSideState = f.playerSide
    }

    private fun clearNinjaDisplaysLocked() {
        ninjaDisplayName.fill("")
        ninjaDisplaySeenAt.fill(0L)
    }

    /** Go updateNinjaDisplay (internal/ui/ninja_display.go). */
    private fun updateNinjaDisplayLocked(f: AnalysisResult) {
        val at = frameTime(f.capturedAtNanos)
        if (ninjaDisplayAt != 0L && at < ninjaDisplayAt) clearNinjaDisplaysLocked()
        if (ninjaDisplayAt != 0L && at == ninjaDisplayAt) return
        ninjaDisplayAt = at
        if (f.scene == "vs" || (!f.hold && !f.fighting && isEndScene(f.scene))) {
            clearNinjaDisplaysLocked()
            return
        }
        for (i in 0..1) {
            if (at - ninjaDisplaySeenAt[i] > NAME_DISPLAY_TTL_NANOS) {
                ninjaDisplayName[i] = ""
                ninjaDisplaySeenAt[i] = 0L
            }
            if (!f.fighting || f.hold || f.err != null) continue
            val name = if (i == 0) f.leftNinja else f.rightNinja
            val candidate = if (i == 0) f.leftNinjaCandidate else f.rightNinjaCandidate
            if (name.isNotEmpty()) {
                ninjaDisplayName[i] = name
                ninjaDisplaySeenAt[i] = at
            } else if (candidate.isNotEmpty() && candidate != ninjaDisplayName[i]) {
                ninjaDisplayName[i] = ""
                ninjaDisplaySeenAt[i] = 0L
            }
        }
    }

    /** Go recentOpponentDisplay; Android shows no "needs recheck" suffix. */
    private fun recentOpponentDisplayLocked(): String {
        val index = when (effectiveSideLocked()) {
            "left" -> 1
            "right" -> 0
            else -> return ""
        }
        val age = ninjaDisplayAt - ninjaDisplaySeenAt[index]
        val name = ninjaDisplayName[index]
        return if (name.isEmpty() || age < 0 || age > NAME_DISPLAY_TTL_NANOS) "" else name
    }

    /** Go frameTime: a frame without an acquisition time is observed now. */
    private fun frameTime(at: Long): Long = if (at != 0L) at else System.nanoTime()

    private fun observeRoundOpeningLocked(f: AnalysisResult) {
        val at = frameTime(f.capturedAtNanos)
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

    /** Go applyScene: exit evidence must be consecutive, fresh end-scene frames. */
    private fun applySceneLocked(f: AnalysisResult) {
        if (f.capturedAtNanos != 0L) {
            if (sceneObservedAt != 0L && f.capturedAtNanos <= sceneObservedAt) {
                leaveHits = 0
                return
            }
            sceneObservedAt = f.capturedAtNanos
        }
        if (f.hold || f.err != null || !isEndScene(f.scene)) leaveHits = 0
        if (isHoldScene(f.scene)) {
            inheritOnReturn = f.scene == "vs" && isFighting
            holdFreeze = true
            return
        }
        if (f.hold || f.err != null) return
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
                    syncLeft = false
                    syncRight = false
                }
            }
        }
    }

    /** Go observeBeads: one usable fight frame; onFrame has already set the observation gap. */
    private fun observeBeadsLocked(f: AnalysisResult) {
        val at = f.capturedAtNanos
        if (at <= 0L) {
            invalidateLocked(at)
            return
        }
        val beads = f.frameBeads
        val lc = readyCount(beads, true)
        val rc = readyCount(beads, false)
        val leftGood = sideObservable(beads, true) && sideBeads(beads, true).size == expectedSlotsLocked(true)
        val rightGood = sideObservable(beads, false) && sideBeads(beads, false).size == expectedSlotsLocked(false)
        if (f.duplicate) {
            // Fresh acquisitions with identical pixels maintain continuity,
            // but never add a confirmation vote. onHeartbeat remains a no-op.
            if (!leftGood || !leftClock.holdObservation(lc, at)) leftClock.invalidateObservation(at)
            if (!rightGood || !rightClock.holdObservation(rc, at)) rightClock.invalidateObservation(at)
            return
        }

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

    private fun fHoldLocked(): Boolean = lastFrame?.let { it.hold || it.err != null || it.uncertain } == true

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
        "blank" -> "画面遮挡"
        "unsupported-resolution" -> "画面比例未校准"
        // The mode word already says 待识别; avoid "待识别 · 待识别".
        else -> s.ifEmpty { "画面" }
    }

    private fun bump() {
        revisionState++
        revision = revisionState
        revisionListener?.invoke()
    }

    companion object {
        private const val ROUND_OPENING_MARKER_GAP_NANOS = 250_000_000L
        /** Go nameDisplayTTL. */
        private const val NAME_DISPLAY_TTL_NANOS = 2_000_000_000L
    }
}
