package hostchain

import android.content.Context
import com.narutotimer.AnalysisResult
import com.narutotimer.NativeCore
import com.narutotimer.Settings
import com.narutotimer.tracker.Session
import java.nio.ByteBuffer

// Real PNG/model -> production nativeAnalyzeBuffer -> Decoder -> Settings/Session/SideClock.
// Synthetic capture times, not Android OS/AssetManager/FrameLoop or live-device validation.
private var assertions = 0
private fun verify(ok: Boolean, message: String) { assertions++; check(ok) { message } }
private fun time(ms: Long) = 40_000_000_000L + ms * 1_000_000

private class VSPixels(val w: Int, val h: Int) {
    val bytes: ByteBuffer = ByteBuffer.allocateDirect(w * h * 4)
    fun copy() = VSPixels(w, h).also { it.bytes.put(bytes.duplicate().clear()).clear() }
    fun blit(src: VSPixels, sx: Int, sy: Int, w: Int, h: Int, dx: Int, dy: Int): VSPixels {
        for (y in 0 until h) for (x in 0 until w)
            bytes.putInt(((dy + y) * this.w + dx + x) * 4, src.bytes.getInt(((sy + y) * src.w + sx + x) * 4))
        return this
    }
    companion object {
        fun read(path: String): VSPixels {
            val size = HostPixels.readPng(path, null)
            return VSPixels(size[0], size[1]).also { HostPixels.readPng(path, it.bytes) }
        }
    }
}
private class VSRun(mode: String = "auto", preMatch: Boolean = false) {
    val settings = Settings(Context()).apply {
        playerNames = listOf("白方小")
        playerSide = mode
        rememberSide = true
        preMatchSideRecognition = preMatch
    }
    val session = Session(settings) { NativeCore.setPlayerNames(it) }
    private var sequence = 0L
    init {
        NativeCore.setPlayerNames(settings.playerNames)
        NativeCore.nativeSetPreMatchSideRecognition(settings.preMatchSideRecognition)
        NativeCore.nativeSetTextRecognition(false) // keep battle OCR off throughout
        verify(NativeCore.nativeFinishAssets(), "pipeline init: ${NativeCore.lastError()}")
        settings.addListener { key ->
            if (key == Settings.KEY_PRE_MATCH_SIDE_RECOGNITION)
                NativeCore.nativeSetPreMatchSideRecognition(settings.preMatchSideRecognition)
        }
    }
    fun feed(p: VSPixels, ms: Long, duplicate: Boolean, reused: Boolean): AnalysisResult {
        val f = checkNotNull(NativeCore.analyzeBuffer(p.bytes, p.w, p.h, p.w * 4, time(ms), NativeCore.METHOD_BUFFER))
        verify(f.err == null && f.sequence == ++sequence, "successful JNI sequence")
        verify(f.capturedAtNanos == time(ms), "capture time preserved")
        verify(f.duplicate == duplicate && f.reused == reused,
            "${ms}ms dup/reuse=${f.duplicate}/${f.reused}, expected $duplicate/$reused")
        session.onFrame(f)
        return f
    }
    fun vs(p: VSPixels, ms: Long, duplicate: Boolean, reused: Boolean, side: String? = ""): AnalysisResult {
        val before = session.overlayState(time(ms))
        val f = feed(p, ms, duplicate, reused)
        verify(f.scene == "vs" && f.hold && f.uncertain && !f.fighting && !f.roundOpening, "VS holds, not round opening: $f")
        verify((side == null || f.playerSide == side) && f.oppName.isEmpty(), "only complete mine, never non-mine inference: ${f.playerSide}")
        if (f.playerSide.isNotEmpty()) verify(f.playerName == "白方小", "exact full name")
        val after = session.overlayState(time(ms))
        verify(before.leftText == after.leftText && before.rightText == after.rightText, "VS must not clear or start clocks")
        return f
    }
}

private fun staticVS(p: VSPixels, label: String) {
    val run = VSRun()
    verify(!Settings(Context()).preMatchSideRecognition, "fresh Settings defaults false")
    run.vs(p, 0, false, false)
    run.vs(p, 16, true, true)
    verify(run.session.side.isEmpty(), "default off cannot select side")
    run.settings.preMatchSideRecognition = true
    run.vs(p, 32, true, false)
    HostPixels.waitOCR()
    run.vs(p, 48, true, false)
    verify(run.session.side.isEmpty(), "one OCR acquisition is not confirmation")
    run.vs(p, 64, true, true)
    run.vs(p, 432, true, false)
    HostPixels.waitOCR()
    val accepted = run.vs(p, 448, true, false, "left")
    sourceRules(accepted)
    verify(run.session.side == "left", "real native footer side reaches Session")
    verify(run.session.overlayState(time(448)).eventText == "第 0 次", "Hold cannot create substitute events")
    // Defense in depth: even a valid result already delivered by JNI is refused
    // by a new Session with the option off; no synthetic AnalysisResult fields.
    val offSettings = Settings(Context()).apply { playerNames = listOf("白方小") }
    val off = Session(offSettings)
    off.onFrame(accepted)
    verify(off.side.isEmpty(), "Session rejects VS evidence while disabled")
    run.session.lockSide("right")
    run.vs(p, 464, true, true, "left")
    verify(run.session.side == "right", "manual lock must win over fresh/cached OCR")
    run.settings.preMatchSideRecognition = false
    run.vs(p, 480, true, false)
    run.vs(p, 488, true, true)
    verify(run.session.side == "right", "disable cannot override manual lock")
    run.session.selectSideMode("auto")
    verify(run.session.side.isEmpty(), "disabled VS cache cannot return when switching manual to auto")
    run.session.lockSide("right")
    run.settings.preMatchSideRecognition = true
    run.vs(p, 496, true, false)
    HostPixels.waitOCR()
    run.vs(p, 512, true, false)
    run.session.applySettings(listOf("张三"), "", "right", true)
    run.vs(p, 528, true, false)
    HostPixels.waitOCR()
    run.vs(p, 544, true, false)
    verify(run.session.side == "right", "rename cannot override manual lock")
    run.settings.preMatchSideRecognition = false
    run.vs(p, 560, true, false)
    HostPixels.waitOCR()
    println("PASS JNI static $label: two-stage OCR, duplicate/reuse split, opt-in, rename, manual priority, Hold event=0")
}

// Explicitly synthetic provenance UNIT tests (not extra OCR/scene accuracy claims).
// Production Session/Settings handle these flags; all pixel tests above/below use real JNI results.
private fun sourceRules(vs: AnalysisResult) {
    fun settings() = Settings(Context()).apply {
        playerNames = listOf("白方小")
        preMatchSideRecognition = true
        playerSide = "right"
    }
    val battle = vs.copy(scene = "fight", fighting = true, uncertain = false, hold = false)
    val s = settings()
    val manual = Session(s)
    manual.onFrame(battle)
    s.preMatchSideRecognition = false
    verify(manual.side == "right", "disable preserves manual with battle identity cached")
    manual.selectSideMode("auto")
    verify(manual.side == "left", "disable must NOT revoke battle-origin auto identity")
    val autoSettings = settings().apply { playerSide = "auto" }
    val auto = Session(autoSettings)
    auto.onFrame(vs)
    auto.onFrame(battle.copy(playerSide = "", playerName = ""))
    autoSettings.preMatchSideRecognition = false
    verify(auto.side.isEmpty(), "VS-origin effective auto side clears immediately even on fight scene")
    val replacedSettings = settings().apply { playerSide = "auto" }
    val replaced = Session(replacedSettings)
    replaced.onFrame(vs)
    replaced.onFrame(battle)
    replacedSettings.preMatchSideRecognition = false
    verify(replaced.side == "left", "new battle-origin identity supersedes VS provenance")
}

private fun swapClock(ordinary: VSPixels, opening: VSPixels, swap: VSPixels) {
    // Same controlled 2 -> 1 -> 1 left-bean sequence as ChainCheck, starting an
    // actual production SideClock before switching to the unmodified user VS PNG.
    val baseline = ordinary.copy().blit(ordinary, 127, 44, 13, 19, 140, 44)
    val confirm = ordinary.copy().also { it.bytes.putInt((300 * it.w + 500) * 4, 0x50463cff) }
    val run = VSRun("right", true)
    verify(run.feed(baseline, 0, false, false).fighting, "baseline real battle gate")
    run.feed(ordinary, 100, false, false)
    // Async/settings reanalysis of unchanged BATTLE pixels is still Duplicate:
    // it must never turn the pending bead drop into an extra confirmation vote.
    NativeCore.nativeSetTextRecognition(true)
    run.feed(ordinary, 150, true, false)
    verify(run.session.overlayState(time(150)).eventText == "第 0 次", "OCR reanalysis is not a bead vote")
    NativeCore.nativeSetTextRecognition(false)
    run.feed(ordinary, 166, true, false)
    verify(run.session.overlayState(time(166)).eventText == "第 0 次", "disable reanalysis is not a bead vote")
    HostPixels.waitOCR()
    run.feed(confirm, 200, false, false)
    val started = run.session.overlayState(time(200))
    verify(started.eventText == "第 1 次" && started.primaryText == "14.9", "production clock started at first bead drop")
    run.vs(swap, 300, false, false)
    HostPixels.waitOCR()
    run.vs(swap, 316, true, false)
    run.vs(swap, 700, true, false)
    HostPixels.waitOCR()
    run.vs(swap, 716, true, false, "left")
    verify(run.session.side == "right", "manual side remains right through death swap")
    verify(run.session.fighting, "VS hold does not transition the session out of its existing fight")
    val held = run.session.overlayState(time(716))
    verify(held.eventText == "第 1 次" && held.primaryText == "14.3", "death swap retains the established ticking clock")
    // Opening and swap use the same footer/glyphs; no artificial round event.
    run.vs(opening, 732, false, false, null)
    verify(run.session.overlayState(time(732)).eventText == "第 1 次", "opening logo is still Hold, not RoundOpening")
    val battleReturn = run.feed(ordinary, 748, false, false)
    verify(battleReturn.scene == "fight" && battleReturn.playerSide.isEmpty(), "readable battle return has no new identity")
    val beforeOff = run.session.overlayState(time(748))
    run.settings.preMatchSideRecognition = false // cached source is VS although current scene is fight
    verify(run.session.side == "right", "off preserves manual side immediately")
    run.session.selectSideMode("auto")
    verify(run.session.side.isEmpty(), "off clears VS-origin auto cache even after return to fight")
    val afterOff = run.session.overlayState(time(748))
    verify(beforeOff.leftText == afterOff.leftText && beforeOff.rightText == afterOff.rightText,
        "identity revocation does not touch established clocks")
    run.session.lockSide("right")
    verify(run.session.overlayState(time(748)).eventText == "第 1 次", "toggle never resets clock event count")
    HostPixels.waitOCR()
    println("PASS JNI death swap/opening: existing event=1 clock keeps ticking, no new substitute or round state")
}

fun main(args: Array<String>) {
    require(args.size == 2) { "VSCheckKt <repo> <ordinary-user.png>" }
    val repo = args[0]
    verify(NativeCore.nativeInit(), "native init")
    HostPixels.registerAssets(repo)
    HostPixels.setOCRModel("$repo/internal/ocr/neuraldata/recognizer.onnx")
    val dir = "$repo/inbox/regressions/vs-account-side-20261107"
    val opening = VSPixels.read("$dir/opening.png")
    val swap = VSPixels.read("$dir/swap.png")
    staticVS(opening, "opening")
    staticVS(swap, "swap")
    swapClock(VSPixels.read(args[1]), opening, swap)
    NativeCore.nativeRelease()
    println("PASS VS chain: $assertions assertions; real model/PNGs + production JNI/Decoder/Settings/Session/SideClock")
    println("LIMIT: host model loader; no Android AssetManager, OS capture, FrameLoop scheduling, or UI/device validation")
}
