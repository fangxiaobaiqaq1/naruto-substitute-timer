package hostchain

import android.content.Context
import com.narutotimer.AnalysisResult
import com.narutotimer.FrameGeometry
import com.narutotimer.NativeCore
import com.narutotimer.PixelPoint
import com.narutotimer.Settings
import com.narutotimer.tracker.NinjaRules
import com.narutotimer.tracker.OverlayState
import com.narutotimer.tracker.Session
import java.awt.RenderingHints
import java.awt.image.BufferedImage
import java.nio.ByteBuffer
import java.io.File
import kotlin.math.abs
import kotlin.math.roundToInt

// No fake AnalysisResult/Decoder/Settings/tracker and no injected fighting flags.
// Every delivered result below comes from production nativeAnalyzeBuffer + Decoder.
object HostPixels {
    @JvmStatic external fun registerAssets(repo: String)
    @JvmStatic external fun readPng(path: String, target: ByteBuffer?): IntArray
}

private const val BASE = 10_000_000_000L
private fun at(ms: Long) = BASE + ms * 1_000_000L
private var assertions = 0
private fun verify(ok: Boolean, message: String) {
    assertions++
    check(ok) { message }
}
private fun <T> equal(actual: T, expected: T, message: String) =
    verify(actual == expected, "$message: actual=$actual expected=$expected")

/** Fixture pixels only: RGBA copies, occlusion, and JDK-only bilinear resizing. */
private class Pixels(val width: Int, val height: Int) {
    val buffer: ByteBuffer = ByteBuffer.allocateDirect(width * height * 4)
    fun copy(): Pixels = Pixels(width, height).also {
        it.buffer.put(buffer.duplicate().clear()).clear()
    }
    fun pixel(x: Int, y: Int, rgba: Int) { buffer.putInt((y * width + x) * 4, rgba) }
    fun blit(src: Pixels, sx: Int, sy: Int, w: Int, h: Int, dx: Int, dy: Int): Pixels {
        for (y in 0 until h) for (x in 0 until w)
            pixel(dx + x, dy + y, src.buffer.getInt(((sy + y) * src.width + sx + x) * 4))
        return this
    }
    fun fill(x0: Int, y0: Int, x1: Int, y1: Int, rgba: Int): Pixels {
        for (y in y0 until y1) for (x in x0 until x1) pixel(x, y, rgba)
        return this
    }
    fun scaled(w: Int, h: Int): Pixels {
        val source = BufferedImage(width, height, BufferedImage.TYPE_INT_ARGB)
        for (y in 0 until height) for (x in 0 until width) {
            val rgba = buffer.getInt((y * width + x) * 4)
            source.setRGB(x, y, (rgba ushr 8) or (rgba shl 24))
        }
        val target = BufferedImage(w, h, BufferedImage.TYPE_INT_ARGB)
        val g = target.createGraphics()
        try {
            g.setRenderingHint(RenderingHints.KEY_INTERPOLATION, RenderingHints.VALUE_INTERPOLATION_BILINEAR)
            g.drawImage(source, 0, 0, w, h, null)
        } finally {
            g.dispose()
        }
        return Pixels(w, h).also { out ->
            for (y in 0 until h) for (x in 0 until w) {
                val argb = target.getRGB(x, y)
                out.pixel(x, y, (argb shl 8) or (argb ushr 24))
            }
        }
    }
    fun fresh(serial: Int = 60): Pixels = copy().also {
        // Outside the HUD/control ROIs: an independent pixel frame, NOT another vote
        // made by changing metadata. The original screenshot is never overwritten.
        it.pixel(500, 300, 0x504600ff or ((serial and 255) shl 8))
    }
    companion object {
        fun read(path: String): Pixels {
            val size = HostPixels.readPng(path, null)
            return Pixels(size[0], size[1]).also { HostPixels.readPng(path, it.buffer) }
        }
    }
}

private fun ready(f: AnalysisResult, side: Char) = f.frameBeads.count {
    it.label.startsWith(side) && !it.unknown && it.lit && !it.dark
}
private fun beans(f: AnalysisResult, left: Int, right: Int, profile: String = "duel", leftSlots: Int = 4, rightSlots: Int = 4) {
    equal(f.layoutProfile, profile, "decoded profile")
    equal(f.leftSlots, leftSlots, "decoded left slots")
    equal(f.rightSlots, rightSlots, "decoded right slots")
    val labels = (1..leftSlots).map { "L$it" } + (1..rightSlots).map { "R$it" }
    equal(f.frameBeads.map { it.label }, labels, "decoded labels in slot order")
    equal(f.frameBeads.size, leftSlots + rightSlots, "bean topology, not repeated labels")
    verify(f.frameBeads.all { !it.unknown && it.conf >= 0.6 && (it.lit != it.dark) }, "all beans must be readable")
    equal(ready(f, 'L'), left, "actual pixel left ready")
    equal(ready(f, 'R'), right, "actual pixel right ready")
}
private fun fight(f: AnalysisResult, left: Int, right: Int, profile: String = "duel", leftSlots: Int = 4, rightSlots: Int = 4) {
    beans(f, left, right, profile, leftSlots, rightSlots)
    verify(f.fighting && !f.hold && !f.uncertain && f.err == null, "real gate blocked frame: $f")
    equal(f.scene, "fight", "decoded fight scene")
    verify(!f.roundOpening, "covered round label must not invent round-opening evidence")
}
private fun display(s: OverlayState, event: Int, primary: String) {
    equal(s.eventText, "第 $event 次", "actual eventText")
    equal(s.primaryText, primary, "actual primaryText")
}
private data class Step(val frame: AnalysisResult, val overlay: OverlayState)

private class Run(val label: String, mode: String = "right", private val quiet: Boolean = false) {
    val settings = Settings(Context()).apply {
        playerSide = mode
        rememberSide = true
        showBothSides = false
        overlayMode = "mini"
        overlayX = 237
        overlayY = 91
    }
    val session = Session(settings)
    private var sequence = 0L
    init {
        if (!quiet) println("\nCASE $label")
        verify(NativeCore.nativeFinishAssets(), "native pipeline: ${NativeCore.lastError()}")
    }
    fun state(ms: Long) = session.overlayState(at(ms))
    fun feed(p: Pixels, ms: Long, note: String, duplicate: Boolean = false, trace: Boolean = !quiet): Step {
        val f = checkNotNull(NativeCore.analyzeBuffer(
            p.buffer, p.width, p.height, p.width * 4, at(ms), NativeCore.METHOD_BUFFER,
        )) { "nativeAnalyzeBuffer/Decoder failed: ${NativeCore.lastError()}" }
        equal(f.width, p.width, "JNI width")
        equal(f.height, p.height, "JNI height")
        equal(f.capturedAtNanos, at(ms), "JNI acquisition time")
        equal(f.captureMethod, "android-buffer", "JNI source")
        equal(f.duplicate, duplicate, "real duplicate flag ($note)")
        equal(f.reused, duplicate, "real reuse flag ($note)")
        if (f.err == null) equal(f.sequence, ++sequence, "JNI sequence")
        else equal(f.sequence, 0L, "failed frame is not a successful delivery")
        if (duplicate) {
            equal(f.analysisStartedNanos, 0L, "duplicate must skip analysis")
            equal(f.analyzedAtNanos, 0L, "duplicate must skip analysis completion")
        }
        session.onFrame(f)
        val s = state(ms)
        equal(settings.overlayX, 237, "user X must survive")
        equal(settings.overlayY, 91, "user Y must survive")
        verify(s.mini && !s.showBothSides, "retain user's mini / single-opponent mode")
        if (trace) println(
            "  ${ms}ms $note seq=${f.sequence} fight=${f.fighting} hold=${f.hold} " +
                "unc=${f.uncertain} err=${f.err} dup=${f.duplicate} reused=${f.reused} " +
                "beans=${ready(f, 'L')}/${ready(f, 'R')} scene=${f.scene} gate=${f.gateScore} " +
                "event='${s.eventText}' primary='${s.primaryText}' left='${s.leftText}' right='${s.rightText}'",
        )
        return Step(f, s)
    }
}

// A clearly labelled *synthetic UI-status fixture*, not gameplay. Paste the real
// lobby-bottom template at its manifest's 1920x1080 ROI; do not inject Scene/flags.
private fun lobbyFixture(repo: String): Pixels {
    val p = Pixels(1920, 1080)
    for (y in 0 until p.height) for (x in 0 until p.width) {
        val grey = 70 + ((x / 32 + y / 32) % 2) * 60
        p.pixel(x, y, (grey shl 24) or (grey shl 16) or (grey shl 8) or 255)
    }
    val label = Pixels.read("$repo/assets/templates/lobby_bottom.png")
    return p.blit(label, 0, 0, label.width, label.height, 38, 929)
}

private const val FIXTURE_BG = 0x202020ff
private const val DARK_BODY = 0x1c3662ff
private const val RIGHT_NINJA = "长门[秽土转生]"

private fun identities(f: AnalysisResult, left: String) {
    equal(f.leftNinja, left, "current native left identity")
    equal(f.rightNinja, RIGHT_NINJA, "untouched native right identity")
}

private fun leftGeometry(f: AnalysisResult, xs: List<Int>, y: Int, tolerance: Int = 1) {
    val row = f.frameBeads.filter { it.label.startsWith('L') }
    verify(row.size == xs.size && row.zip(xs).all { abs(it.first.x - it.second) <= tolerance },
        "independent expected slot pitch (including L5/L6): got=${row.map { it.x }} want=$xs")
    verify(row.all { abs(it.y - y) <= tolerance },
        "identity-gated row offset: got=${row.map { it.y }} want=$y tolerance=$tolerance")
}

private fun report(label: String, steps: List<Step>) {
    val (f, s) = steps.last()
    println("  $label L=${steps.joinToString("->") { ready(it.frame, 'L').toString() }} R=${ready(f, 'R')} " +
        "names='${f.leftNinja}'/'${f.rightNinja}' slots=${f.leftSlots}/${f.rightSlots} " +
        "row=${f.frameBeads.filter { it.label.startsWith('L') }.joinToString { "${it.x},${it.y}" }} " +
        "fight=${f.fighting} hold=${f.hold} unc=${f.uncertain} event='${s.eventText}' CD='${s.primaryText}' " +
        "clocks=${s.leftText}/${s.rightText}")
}

// Parameters describe only pixels to paint and expectations to ASSERT. They are
// never passed to native as identity, palette, topology, Fighting or scene flags.
private data class Synthetic(
    val label: String, val title: String, val name: String, val color: Int,
    val slots: Int = 4, val offset: Int = 0, val dark: Int = DARK_BODY,
) {
    // Round AFTER adding the offset, in the 960x540 title-template reference.
    val y: Int get() = (101 * .6 + offset).roundToInt()
}

private fun syntheticFixture(repo: String, background: Pixels, firstX: Int, spec: Synthetic): Pixels {
    val p = background.copy()
        // Remove the original Sasuke face/title/gauge; rebuild the left HUD.
        // Preserve only the right HUD and shared controls from the background.
        // These are deliberately NOT captures of the other ninjas.
        .fill(0, 0, firstX - 8, 110, FIXTURE_BG)
        .fill(firstX - 8, 0, firstX + 240, 40, FIXTURE_BG)
        // Remove the old energy gauge AND purple halo before painting bodies.
        .fill(firstX - 10, 40, firstX + 240, 112, FIXTURE_BG)
    // A synthesized camp edge, not the resized photo's thick/ambiguous end cap.
    // All three scanlines and the 15px bead pitch have independent geometry.
    for (row in -1..1) p.fill(firstX - row, 42 + row, firstX + 120, 43 + row, 0xf0460aff.toInt())
    val title = Pixels.read("$repo/internal/ninja/templates/${spec.title}.png")
    p.blit(title, 0, 0, title.width, title.height, firstX + 8, 15) // unscaled real title
    for (i in 0 until spec.slots) {
        val x = firstX + 15 * i // independent calibrated pitch, NEVER derived from native L5/L6
        p.fill(x - 3, spec.y - 4, x + 4, spec.y + 5, spec.color) // separated 7x9 bodies, with real slot gaps
    }
    return p
}

// nativeLastGeometry of the frame just delivered (docs/区域校准.md 每帧几何).
private fun geometryOf(f: AnalysisResult, label: String): FrameGeometry {
    val g = checkNotNull(NativeCore.lastGeometry()) { "nativeLastGeometry ($label): ${NativeCore.lastError()}" }
    equal(g.sequence, f.sequence, "geometry belongs to the delivered frame ($label)")
    equal(listOf(g.width, g.height, g.layoutProfile), listOf(f.width, f.height, f.layoutProfile), "geometry frame ($label)")
    for ((side, prefix) in listOf(g.left to 'L', g.right to 'R')) {
        equal(side.beads, f.beads.filter { it.label.startsWith(prefix) }.map { PixelPoint(it.x, it.y) }, "geometry beads ($label)")
        verify(side.nameRoi != null && side.avatarRoi != null && side.beadBox != null && side.avatarBox != null,
            "geometry regions and default boxes ($label): $side")
    }
    return g
}

// Region calibration (docs/区域校准.md) through production Settings -> NativeCore
// -> nativeSetCalibration: an identical screen is re-read with the new boxes
// at once (no reuse, no duplicate), and clearing them restores the automatic row.
private fun calibrationChecks(frame: Pixels) {
    val run = Run("region calibration applies live to an identical screen")
    val auto = run.feed(frame, 0, "automatic layout").frame
    fight(auto, 2, 4)
    val geometry = geometryOf(auto, "automatic")
    val dup = run.feed(frame, 16, "identical pixels reuse the analysis", duplicate = true).frame
    equal(geometryOf(dup, "reused").copy(sequence = 0), geometry.copy(sequence = 0), "a reused frame carries its geometry")
    val left = auto.frameBeads.filter { it.label.startsWith('L') }
    val pitch = (left[3].x - left[0].x) / 3f
    val box = floatArrayOf(
        (left[0].x - pitch / 2) / frame.width, (left[0].y - pitch / 2) / frame.height,
        4 * pitch / frame.width, pitch / frame.height,
    )
    run.settings.calibProfile = auto.layoutProfile
    // A box over empty background moves the left row there on the SAME pixels.
    run.settings.setCalibRect(Settings.KEY_CALIB_LEFT_BEADS, floatArrayOf(box[0], box[1] + 0.3f, box[2], box[3]))
    verify(NativeCore.setCalibration(run.settings), "nativeSetCalibration: ${NativeCore.lastError()}")
    val moved = run.feed(frame, 32, "same pixels, left box moved").frame
    verify(moved.frameBeads.filter { it.label.startsWith('L') }.all { !it.lit && it.y > left[0].y + 100 },
        "left row must follow the new box: ${moved.frameBeads}")
    equal(ready(moved, 'R'), 4, "uncalibrated right row unchanged")
    // The box around the real row reads it like the automatic layout.
    run.settings.setCalibRect(Settings.KEY_CALIB_LEFT_BEADS, box)
    verify(NativeCore.setCalibration(run.settings), "nativeSetCalibration: ${NativeCore.lastError()}")
    fight(run.feed(frame, 48, "same pixels, box on the real row").frame, 2, 4)
    run.settings.setCalibRect(Settings.KEY_CALIB_LEFT_BEADS, null)
    verify(NativeCore.setCalibration(run.settings), "nativeSetCalibration: ${NativeCore.lastError()}")
    fight(run.feed(frame, 64, "calibration cleared").frame, 2, 4)
    // All four default boxes saved unchanged (float32 Settings) read the same screen identically.
    run.settings.calibProfile = geometry.layoutProfile
    val defaults = listOf(geometry.left.beadBox, geometry.right.beadBox, geometry.left.avatarBox, geometry.right.avatarBox)
    Settings.KEY_CALIB_RECTS.zip(defaults).forEach { (key, box) -> run.settings.setCalibRect(key, box!!.toFloatArray()) }
    verify(NativeCore.setCalibration(run.settings), "nativeSetCalibration: ${NativeCore.lastError()}")
    verify(NativeCore.lastGeometry() == null, "a rebuilt engine drops the old calibration's geometry")
    val saved = run.feed(frame, 80, "same pixels, default boxes saved").frame
    equal(saved.beads, auto.beads, "default boxes reproduce the automatic beads")
    equal(listOf(saved.leftNinja, saved.rightNinja), listOf(auto.leftNinja, auto.rightNinja), "default boxes keep identities")
    val again = geometryOf(saved, "saved defaults")
    for ((a, b) in listOf(again.left to geometry.left, again.right to geometry.right)) {
        equal(listOf(a.beads, a.nameRoi, a.avatarRoi), listOf(b.beads, b.nameRoi, b.avatarRoi), "default boxes reproduce the regions")
    }
    println("  geometry: L ${geometry.left}\n            R ${geometry.right}")
    Settings.KEY_CALIB_RECTS.forEach { run.settings.setCalibRect(it, null) }
    verify(NativeCore.setCalibration(run.settings), "nativeSetCalibration: ${NativeCore.lastError()}")
}

private fun specialChecks(repo: String, path: String) {
    println("\nSPECIAL: real PNG + controlled edits; all other ninja HUDs below are SYNTHETIC, not gameplay.")
    val original = Pixels.read(path)
    equal(original.width, 1024, "special user fixture width")
    equal(original.height, 461, "special user fixture height")
    val real = Run("real Xiayin purple 4 -> 3 -> 3", quiet = true)
    val baseline = real.feed(original, 0, "unaltered special user PNG")
    fun realFrame(f: AnalysisResult, count: Int) {
        fight(f, count, 4, "camp")
        identities(f, NinjaRules.SASUKE_XIAYIN)
        leftGeometry(f, listOf(115, 128, 141, 154), 59)
    }
    realFrame(baseline.frame, 4)
    display(baseline.overlay, 0, "待替身")
    // L4's entire body AND glow are covered (39x31), not a single voting pixel.
    // Start past L3's body, below the HP bar; leave both battle controls intact.
    val dark = original.copy().fill(148, 52, 187, 83, DARK_BODY)
    val drop = real.feed(dark, 100, "controlled dark L4 body and glow")
    realFrame(drop.frame, 3)
    display(drop.overlay, 0, "待替身")
    val confirmed = real.feed(dark.fresh(), 200, "independent confirming pixels")
    realFrame(confirmed.frame, 3)
    display(confirmed.overlay, 1, "14.9")
    equal(confirmed.overlay.leftText, "14.9", "real special left opponent clock")
    equal(confirmed.overlay.rightText, "—", "real special right unchanged")
    display(real.state(15_100), 1, "待替身") // first-drop timestamp, not confirmation time
    report("REAL Xiayin purple / controlled L4", listOf(baseline, drop, confirmed))

    // Each rejection has a NEW native engine/Session: one-control continuation
    // of an already established fight is intentionally a different policy.
    fun reject(label: String, pixels: Pixels, inspect: (AnalysisResult) -> Unit) {
        val step = Run(label, quiet = true).feed(pixels, 0, label)
        val f = step.frame
        verify(f.hold && f.uncertain && !f.fighting && f.err == null, "$label must cold-start HOLD: $f")
        inspect(f)
        display(step.overlay, 0, "待识别")
        equal(step.overlay.leftText, "—", "$label must not start left clock")
        equal(step.overlay.rightText, "—", "$label must not start right clock")
        report("REJECT $label", listOf(step))
    }
    for ((label, pixels) in listOf(
        "real/reset hidden" to original.copy().fill(411, 4, 460, 58, FIXTURE_BG),
        "real/substitute hidden" to original.copy().fill(688, 373, 797, 453, FIXTURE_BG),
    )) {
        reject(label, pixels) { f ->
            beans(f, 4, 4, "camp")
            identities(f, NinjaRules.SASUKE_XIAYIN)
            leftGeometry(f, listOf(115, 128, 141, 154), 59)
        }
    }

    // Approximately aspect-preserving 1024x461 -> 1200x540 (rounding <0.1%).
    // HUD scale is now 1 for the repository's 960x540 name templates.
    val background = original.scaled(1200, 540)
    val probe = Run("scaled real anchor only", quiet = true).feed(background, 0, "bilinear fixture background")
    // Geometric center from the original capture, not the sampler's bounded
    // glint retry (which can move an individual sampling row by a few pixels).
    val firstX = (115.0 * 1200 / 1024).roundToInt()
    identities(probe.frame, NinjaRules.SASUKE_XIAYIN)
    leftGeometry(probe.frame, (0..3).map { firstX + 15 * it }, 70, tolerance = 3)

    val types = listOf(
        Synthetic("Xiayin red / same title", "sasuke_xiayin", NinjaRules.SASUKE_XIAYIN, 0xf05028ff.toInt(), offset = 9, dark = 0x280a0fff),
        Synthetic("Obito purple", "obito", NinjaRules.OBITO, 0xff46ffff.toInt()),
        Synthetic("Naruto red", "naruto", NinjaRules.NARUTO, 0xf05028ff.toInt()),
        Synthetic("Hashirama warm / L6 drop", "hashirama", NinjaRules.HASHIRAMA, 0xf04112ff.toInt(), slots = 6),
        Synthetic("Madara warm", "madara", NinjaRules.MADARA, 0xf04112ff.toInt()),
        Synthetic("Minato blue / energy +13", "minato_kyubi", NinjaRules.MINATO_KYUBI, 0x28b9ebff, offset = 13),
    )
    for (spec in types) {
        val full = syntheticFixture(repo, background, firstX, spec)
        val lastX = firstX + (spec.slots - 1) * 15
        val fewer = full.copy().fill(lastX - 3, spec.y - 4, lastX + 4, spec.y + 5, spec.dark)
        val run = Run("SYNTHETIC ${spec.label}", quiet = true)
        val steps = listOf(
            run.feed(full, 0, "synthetic full row + repository title"),
            run.feed(fewer, 100, "synthetic last body dark"),
            run.feed(fewer.fresh(), 200, "independent confirming pixels"),
        )
        for ((i, step) in steps.withIndex()) {
            val count = spec.slots - if (i == 0) 0 else 1
            fight(step.frame, count, 4, "camp", spec.slots)
            identities(step.frame, spec.name)
            leftGeometry(step.frame, (0 until spec.slots).map { firstX + 15 * it }, spec.y)
            display(step.overlay, if (i == 2) 1 else 0, if (i == 2) "14.9" else "待替身")
            equal(step.overlay.leftText, if (i == 2) "14.9" else "—", "synthetic left opponent clock")
            equal(step.overlay.rightText, "—", "synthetic right must stay idle")
            if (spec.slots == 6) {
                // L5 stays lit; L6 itself changes. Neither is a duplicate of L4
                // or a sample at the obsolete 30-unit extension coordinates.
                verify(step.frame.frameBeads.single { it.label == "L5" }.lit, "actual fifth slot must be lit")
                equal(step.frame.frameBeads.single { it.label == "L6" }.dark, i > 0, "actual sixth slot transition")
            }
        }
        display(run.state(15_100), 1, "待替身")
        report("SYNTHETIC ${spec.label}", steps)
    }

    fun unreadable(f: AnalysisResult, name: String, y: Int) {
        equal(f.layoutProfile, "camp", "negative fixture profile")
        identities(f, name)
        equal(f.leftSlots, 4, "no unauthorized six-slot expansion")
        equal(f.rightSlots, 4, "untouched right topology")
        equal(ready(f, 'R'), 4, "negative fixture preserves right beans")
        leftGeometry(f, (0..3).map { firstX + 15 * it }, y)
        verify(f.frameBeads.filter { it.label.startsWith('L') }.all { it.unknown && !it.lit },
            "colored effect / unverified identity must not supply left bean votes: $f")
    }
    val xiayin = types[0]
    val thin = syntheticFixture(repo, background, firstX, xiayin)
        .fill(firstX - 10, 50, firstX + 240, 112, FIXTURE_BG)
        .fill(firstX - 10, xiayin.y - 2, firstX + 60, xiayin.y + 3, xiayin.color)
    reject("SYNTHETIC named Xiayin / thin red band", thin) { unreadable(it, xiayin.name, 70) }
    // Both exact title and portrait are absent, with a fresh engine (no hint).
    for (spec in listOf(xiayin, types[3])) {
        val unnamed = syntheticFixture(repo, background, firstX, spec)
            .fill(firstX - 8, 0, firstX + 240, 40, FIXTURE_BG)
        reject("SYNTHETIC ${spec.label} / no identity", unnamed) { unreadable(it, "", 61) }
    }
    val obito = types[1]
    val wash = syntheticFixture(repo, background, firstX, obito)
        .fill(firstX - 10, 49, firstX + 60, 88, obito.color)
    reject("SYNTHETIC named Obito / purple wash", wash) { unreadable(it, obito.name, 61) }
    println("  LIMIT: other ninja original captures are unavailable here; synthetic titles/colors are NOT live-game validation.")
}

// Consume the EXACT exported pixels from the Go full-session matrix. Different
// aspect ratios are independently rendered HUDs, not stretched phone screenshots.
private fun matrixChecks(directory: String) {
    if (directory.isEmpty()) return
    val cases = File(directory).walkTopDown().filter { it.isFile && it.name == "case.tsv" }.sortedBy { it.path }.toList()
    verify(cases.isNotEmpty(), "matrix directory contains no exported cases")
    for ((number, meta) in cases.withIndex()) {
        val fields = meta.readText().trim().split('\t')
        verify(fields.size == 4, "invalid matrix metadata: $meta")
        val (profile, side, name, slotText) = fields
        val slots = slotText.toInt()
        verify(profile in listOf("camp", "duel") && side in listOf("left", "right") && slots in listOf(4, 6), "invalid matrix layout")
        val left = side == "left"
        val run = Run("MATRIX ${meta.parentFile.name}", if (left) "right" else "left", quiet = true)
        for (i in 0..2) {
            val pixels = Pixels.read(File(meta.parentFile, "$i.png").path)
            val step = run.feed(pixels, i * 100L, "exported Go matrix frame $i")
            val count = slots - if (i == 0) 0 else 1
            fight(step.frame, if (left) count else 4, if (left) 4 else count, profile, if (left) slots else 4, if (left) 4 else slots)
            equal(if (left) step.frame.leftNinja else step.frame.rightNinja, name, "matrix current ninja identity")
            display(step.overlay, if (i == 2) 1 else 0, if (i == 2) "14.9" else "待替身")
        }
        if ((number + 1) % 28 == 0 || number + 1 == cases.size) println("  MATRIX ${number + 1}/${cases.size}: identity/slots/current counts/event=1/CD=14.9 passed")
    }
}

fun main(args: Array<String>) {
    require(args.size == 4) { "Usage: ChainCheckKt <repo> <ordinary.png> <special.png> <matrix-dir-or-empty>" }
    val repo = args[0]
    println("CONTROLLED PIXEL SEQUENCES, NOT recorded gameplay. Synthetic acquisition timestamps.")
    println("Production JNI -> NativeCore.Decoder -> Settings -> Session -> SideClock/NinjaRules.")
    println("NOT covered: Android OS capture, HardwareBuffer, FrameLoop scheduling, Canvas/touch/drag.")
    verify(NativeCore.nativeInit(), "native init failed")
    equal(NativeCore.nativeResultHeaderBytes(), 72, "production JNI header")
    HostPixels.registerAssets(repo)
    NativeCore.setPlayerNames(emptyList())
    val original = Pixels.read(args[1])
    equal(original.width, 1024, "user fixture width")
    equal(original.height, 461, "user fixture height")
    val before = original.copy().blit(original, 127, 44, 13, 19, 140, 44)
    val confirm = original.fresh()
    // Hide all available controls (including the training exit), not just the
    // ordinary attack/substitute pair; all eight HUD beans remain visible.
    val occluded = original.copy().fill(700, 350, 950, 450, 0x404040ff)
        .fill(0, 70, 180, 230, 0x404040ff)
    val blank = Pixels(1024, 461).fill(0, 0, 1024, 461, 0x000000ff)
    val tiny = Pixels(32, 32).fill(0, 0, 32, 32, 0x404040ff)
    val lobby = lobbyFixture(repo)

    // All no-clock primaryText branches, using real decoded native results.
    val status = Run("no-clock statuses", "auto")
    equal(status.state(0).primaryText, "待采集", "no lastFrame")
    val raw = status.feed(original, 0, "unaltered user PNG")
    fight(raw.frame, 1, 4)
    equal(raw.frame.playerSide, "", "fixture has no automatic side evidence")
    equal(raw.overlay.primaryText, "待认边", "fight without identified player side")
    status.session.selectSideMode("right")
    display(status.state(0), 0, "待替身")
    equal(status.state(0).sideModeText, "我方·右", "manual player mode")
    verify(status.state(0).tagText.startsWith("对面·左"), "opponent is the left side")
    val obscured = status.feed(occluded, 100, "two controls occluded, beans intact")
    beans(obscured.frame, 1, 4)
    verify(obscured.frame.hold && obscured.frame.uncertain && !obscured.frame.fighting, "controls occlusion must hold")
    display(obscured.overlay, 0, "待识别")
    val idle = status.feed(lobby, 200, "synthetic lobby-template fixture")
    verify(!idle.frame.fighting && !idle.frame.hold && !idle.frame.uncertain && idle.frame.err == null, "lobby must be a clear non-fight")
    equal(idle.frame.scene, "lobby", "actual native lobby classification")
    display(idle.overlay, 0, "待机")
    val failed = status.feed(tiny, 300, "native too-small capture error")
    verify(failed.frame.hold && failed.frame.err != null && !failed.frame.uncertain, "real error without uncertain flag")
    display(failed.overlay, 0, "待识别")
    val black = status.feed(blank, 400, "opaque black pixel frame")
    verify(black.frame.hold && black.frame.uncertain && black.frame.err == null, "black frame is uncertain, not capture error")
    display(black.overlay, 0, "待识别")
    verify(black.overlay.infoText.contains("画面遮挡"), "preserve pre-existing blank scene label")

    val normal = Run("100ms 2 -> 1 -> 1 and existing-clock precedence")
    equal(normal.session.pollIntervalMs(), 400, "production idle cadence setting")
    val baseline = normal.feed(before, 0, "patched L2 baseline")
    fight(baseline.frame, 2, 4)
    display(baseline.overlay, 0, "待替身")
    equal(normal.session.pollIntervalMs(), 16, "production fighting cadence setting")
    val drop = normal.feed(original, 100, "first drop")
    fight(drop.frame, 1, 4)
    display(drop.overlay, 0, "待替身")
    val confirmed = normal.feed(confirm, 200, "independent confirming pixels")
    fight(confirmed.frame, 1, 4)
    display(confirmed.overlay, 1, "14.9")
    equal(confirmed.overlay.leftText, "14.9", "left opponent clock")
    equal(confirmed.overlay.rightText, "—", "no right-side event")
    display(normal.feed(occluded, 300, "hold keeps ticking").overlay, 1, "14.8")
    display(normal.feed(lobby, 400, "non-fight keeps established clock").overlay, 1, "14.7")
    display(normal.feed(tiny, 500, "error keeps ticking").overlay, 1, "14.6")
    display(normal.feed(original, 600, "readable return keeps ticking").overlay, 1, "14.5")
    display(normal.state(15_100), 1, "待替身") // Event timestamp is the FIRST drop at 100ms.

    val repeated = Run("duplicates and heartbeat never supply a confirmation vote")
    fight(repeated.feed(before, 0, "baseline").frame, 2, 4)
    display(repeated.feed(original, 100, "pending drop").overlay, 0, "待替身")
    val dup = repeated.feed(original, 150, "new JNI delivery, identical pixels", duplicate = true)
    fight(dup.frame, 1, 4)
    display(dup.overlay, 0, "待替身")
    val revision = repeated.session.revision
    repeated.session.onHeartbeat(at(175))
    equal(repeated.session.revision, revision, "heartbeat is not a frame")
    display(repeated.state(175), 0, "待替身")
    display(repeated.feed(confirm, 200, "first independent confirmation").overlay, 1, "14.9")

    val interrupted = Run("hold interrupts pending drop even with eight readable beans")
    fight(interrupted.feed(before, 0, "baseline").frame, 2, 4)
    display(interrupted.feed(original, 100, "pending drop").overlay, 0, "待替身")
    val held = interrupted.feed(occluded, 150, "controls hidden, bean counts unchanged")
    beans(held.frame, 1, 4)
    verify(held.frame.hold && !held.frame.fighting, "real gate must stop pending confirmation")
    display(held.overlay, 0, "待识别")
    val firstReturn = interrupted.feed(original, 200, "first readable return is NOT confirmation")
    fight(firstReturn.frame, 1, 4)
    display(firstReturn.overlay, 0, "待替身")
    display(interrupted.feed(confirm, 300, "second readable return").overlay, 1, "14.9")

    // Our player is RIGHT: a right-only drop may appear in diagnostic rightText,
    // but must NEVER start the single-opponent primary/event display for the LEFT.
    val right4 = raw.frame.beads.single { it.label == "R4" }
    val dark2 = raw.frame.beads.single { it.label == "L2" }
    val rightDrop = original.copy().blit(original, dark2.x - 6, dark2.y - 9, 13, 19, right4.x - 6, right4.y - 9)
    val side = Run("our side RIGHT, only LEFT drives opponent primary/event")
    fight(side.feed(original, 0, "1/4 baseline").frame, 1, 4)
    fight(side.feed(rightDrop, 100, "right-only 4 -> 3").frame, 1, 3)
    val ownDrop = side.feed(rightDrop.fresh(), 200, "right-only independent confirmation")
    fight(ownDrop.frame, 1, 3)
    display(ownDrop.overlay, 0, "待替身")
    equal(ownDrop.overlay.leftText, "—", "opponent left remains idle")
    equal(ownDrop.overlay.rightText, "14.9", "own-side diagnostic proves pixels really dropped")
    equal(ownDrop.overlay.sideModeText, "我方·右", "never silently swap player side")
    verify(ownDrop.overlay.tagText.startsWith("对面·左"), "primary still monitors left")

    // GE-02 (Go app.ObservationCadence, Session.kt): a frame's observation gap is 3x the cadence
    // measured BEFORE it. A session's first interval has no cadence yet and meets the 150 ms floor,
    // so a drop inside the very first 333 ms interval resyncs the baseline (Go captureOnce agrees,
    // android/tools/sessioncheck). Once one interval is known, a slow cadence still confirms drops.
    val first = Run("333ms acquisition cadence, first interval of a session (GE-02 floor)")
    fight(first.feed(before, 0, "baseline").frame, 2, 4)
    fight(first.feed(original, 333, "drop in the first interval").frame, 1, 4)
    val firstConfirm = first.feed(confirm, 666, "same count again")
    fight(firstConfirm.frame, 1, 4)
    display(firstConfirm.overlay, 0, "待替身")

    val slow = Run("333ms acquisition cadence (no FrameLoop/OS scheduling claim)")
    fight(slow.feed(before, 0, "baseline").frame, 2, 4)
    fight(slow.feed(before.fresh(1), 333, "baseline again: cadence 333 ms").frame, 2, 4)
    fight(slow.feed(original, 666, "first drop").frame, 1, 4)
    val slowConfirm = slow.feed(confirm, 999, "independent confirmation")
    fight(slowConfirm.frame, 1, 4)
    display(slowConfirm.overlay, 1, "14.6") // 15 - .333 = 14.667, production format floors tenths.

    // Regression: real repeated acquisitions are not a capture blackout.
    // Before the fix this sequence silently used the first drop as a new baseline.
    val risk = Run("16ms warmup + 1008ms repeated native captures + two drop frames")
    fight(risk.feed(before, 0, "baseline").frame, 2, 4)
    var warmed = before
    for (i in 1..32) {
        warmed = before.fresh(i)
        val s = risk.feed(warmed, i * 16L, "16ms fresh warmup #$i", trace = false)
        fight(s.frame, 2, 4)
        display(s.overlay, 0, "待替身")
    }
    for (i in 1..63) {
        // Each simulated acquisition re-enters real nativeAnalyzeBuffer; not a
        // Session heartbeat or replayed AnalysisResult, despite unchanged pixels.
        val s = risk.feed(warmed, 512L + i * 16L, "repeat #$i", duplicate = true, trace = false)
        fight(s.frame, 2, 4)
        display(s.overlay, 0, "待替身")
    }
    println("  32 fresh 16ms warmup frames, then 63 duplicate/reused native deliveries over 1008ms; event='${risk.state(1520).eventText}'")
    val riskDrop = risk.feed(original, 1536, "drop after prolonged true pixel duplicates")
    fight(riskDrop.frame, 1, 4)
    display(riskDrop.overlay, 0, "待替身")
    val riskConfirm = risk.feed(confirm, 1552, "second fresh drop frame")
    fight(riskConfirm.frame, 1, 4)
    display(riskConfirm.overlay, 1, "14.9")

    val noPixels = Run("heartbeat without captures must NOT bridge a real blackout")
    fight(noPixels.feed(before, 0, "baseline").frame, 2, 4)
    fight(noPixels.feed(before.fresh(), 16, "16ms cadence established").frame, 2, 4)
    val beforeHeartbeats = noPixels.session.revision
    for (i in 2..64) noPixels.session.onHeartbeat(at(i * 16L))
    equal(noPixels.session.revision, beforeHeartbeats, "no new pixels means no visual evidence")
    fight(noPixels.feed(original, 1040, "first frame after capture blackout").frame, 1, 4)
    val afterBlackout = noPixels.feed(confirm, 1056, "second fresh frame cannot prove when the hidden drop happened")
    fight(afterBlackout.frame, 1, 4)
    display(afterBlackout.overlay, 0, "待替身")
    calibrationChecks(before)
    specialChecks(repo, args[2])
    matrixChecks(args[3])
    NativeCore.nativeRelease()
    println("\nPASS: $assertions assertions; original eight ordinary groups retained; controlled pixels only, no live-device/recorded-gameplay claim.")
}
