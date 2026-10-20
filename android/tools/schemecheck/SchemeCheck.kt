package schemecheck

import com.narutotimer.ui.CalibrationRow
import com.narutotimer.ui.CalibrationScheme
import com.narutotimer.ui.CalibrationScheme.Scheme
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.roundToInt
import kotlin.random.Random
import kotlin.system.exitProcess

// CalibrationScheme (docs/区域校准.md 方案文件) with the device's real org.json:
// the doc example, exact round trips, rejections with a message, and the aspect hint.
// CalibrationRow (校准值是标准忍者值) against a model of the engine's row drop: see [rows].

private var checks = 0

private fun verify(ok: Boolean, message: () -> String) {
    checks++
    if (!ok) throw AssertionError(message())
}

private fun same(a: List<FloatArray?>, b: List<FloatArray?>) =
    a.size == b.size && a.indices.all { i -> a[i]?.let { x -> b[i]?.contentEquals(x) == true } ?: (b[i] == null) }

private fun show(boxes: List<FloatArray?>) = boxes.joinToString { it?.joinToString(prefix = "[", postfix = "]") ?: "auto" }

private val DOC = """
{
  "kind": "naruto-timer-calibration",
  "version": 1,
  "frameWidth": 2400,
  "frameHeight": 1080,
  "profile": "duel",
  "leftBeads": {"x": 0.081, "y": 0.104, "width": 0.052, "height": 0.022},
  "rightAvatar": {"x": 0.924, "y": 0.006, "width": 0.072, "height": 0.128}
}
""".trimIndent() + "\n"

private fun reject(label: String, text: String, expect: String) {
    val error = try {
        CalibrationScheme.decode(text)
        null
    } catch (e: CalibrationScheme.FormatException) {
        e.message.orEmpty()
    }
    verify(error != null) { "$label: accepted" }
    verify(error!!.contains(expect)) { "$label: message '$error' lacks '$expect'" }
    println("  reject $label: $error")
}

private fun randomBox(rnd: Random): FloatArray {
    while (true) {
        val x = if (rnd.nextInt(8) == 0) 0f else rnd.nextFloat()
        val y = if (rnd.nextInt(8) == 0) 0f else rnd.nextFloat()
        val w = if (rnd.nextInt(8) == 0) 1f - x else rnd.nextFloat() * (1f - x)
        val h = if (rnd.nextInt(8) == 0) 1f - y else rnd.nextFloat() * (1f - y)
        // Also 5-decimal boxes, as the editor stores them.
        val b = if (rnd.nextBoolean()) floatArrayOf(x, y, w, h) else floatArrayOf(x, y, w, h).map { Math.round(it * 100_000f) / 100_000f }.toFloatArray()
        if (CalibrationScheme.valid(b)) return b
    }
}

private fun run() {
    // The doc's example, and its own text back.
    val doc = CalibrationScheme.decode(DOC)
    verify(doc.profile == "duel" && doc.frameWidth == 2400 && doc.frameHeight == 1080) { "doc header" }
    verify(same(doc.boxes, listOf(floatArrayOf(0.081f, 0.104f, 0.052f, 0.022f), null, null, floatArrayOf(0.924f, 0.006f, 0.072f, 0.128f)))) {
        "doc boxes: ${show(doc.boxes)}"
    }
    val text = CalibrationScheme.encode(doc)
    verify(text == DOC) { "doc layout:\n$text" }
    println("  doc example: parsed and re-encoded byte for byte")

    // Every key in order; tiny values stay plain decimals.
    val all = Scheme("camp", 1280, 576, listOf(
        floatArrayOf(0f, 0f, 1f, 1f),
        floatArrayOf(0.00001f, 0.5f, 0.99999f, 0.5f),
        floatArrayOf(0.1f, 0.2f, 0.3f, 0.4f),
        floatArrayOf(0.004f, 0.006f, 0.072f, 0.128f),
    ))
    val allText = CalibrationScheme.encode(all)
    val order = CalibrationScheme.KEYS.map { allText.indexOf("\"$it\"") }
    verify(order.all { it > 0 } && order == order.sorted()) { "key order: $allText" }
    verify(!allText.contains('E') && allText.contains("\"x\": 0.00001,")) { "plain decimals: $allText" }
    verify(same(CalibrationScheme.decode(allText).boxes, all.boxes)) { "all boxes" }

    // Exact Float round trips.
    val rnd = Random(20261020)
    repeat(3000) {
        val scheme = Scheme(
            if (rnd.nextBoolean()) "camp" else "duel", rnd.nextInt(0, 5000), rnd.nextInt(0, 5000),
            List(4) { if (rnd.nextInt(4) == 0) null else randomBox(rnd) },
        )
        val encoded = CalibrationScheme.encode(scheme)
        val back = CalibrationScheme.decode(encoded)
        verify(back.profile == scheme.profile && same(back.boxes, scheme.boxes)) { "round trip ${show(scheme.boxes)} -> ${show(back.boxes)}\n$encoded" }
        verify(back.frameWidth == scheme.frameWidth && back.frameHeight == scheme.frameHeight) { "frame size $encoded" }
        verify(back.boxes.all { it == null || CalibrationScheme.valid(it) }) { "valid boxes" }
    }
    println("  3000 random schemes: exact Float round trip")

    // Accepted variations.
    verify(CalibrationScheme.decode("\uFEFF" + DOC).profile == "duel") { "BOM" }
    verify(CalibrationScheme.decode(DOC.replace("\"version\": 1", "\"version\": 1.0")).profile == "duel") { "version 1.0" }
    val noSize = CalibrationScheme.decode(DOC.replace("\"frameWidth\": 2400,", "").replace("\"frameHeight\": 1080,", "\"frameHeight\": 0.5,"))
    verify(noSize.frameWidth == 0 && noSize.frameHeight == 0) { "frame size is only a hint" }
    verify(CalibrationScheme.aspectDifference(noSize, 2400, 1080) == null) { "unknown aspect" }
    val nullBox = CalibrationScheme.decode(DOC.replace("\"profile\": \"duel\",", "\"profile\": \"duel\", \"rightBeads\": null, \"extra\": [1, 2],"))
    verify(nullBox.boxes[1] == null) { "null box = automatic" }
    val empty = CalibrationScheme.decode("""{"kind": "naruto-timer-calibration", "version": 1, "profile": "camp"}""")
    verify(empty.profile == "camp" && empty.boxes.all { it == null }) { "all automatic" }
    // Written as float64 elsewhere: a hair past the edge is pulled inside, still valid in Float.
    val edge = CalibrationScheme.decode(DOC.replace(
        "\"x\": 0.924, \"y\": 0.006, \"width\": 0.072, \"height\": 0.128",
        "\"x\": 0.9, \"y\": 0, \"width\": 0.10000000000000009, \"height\": 1.0000000001",
    )).boxes[3]!!
    verify(CalibrationScheme.valid(edge) && edge[0] + edge[2] <= 1f && edge[1] + edge[3] <= 1f) { "edge slack ${edge.joinToString()}" }

    // Rejected files: clear Chinese messages.
    reject("not JSON", "hello", "不是校准方案文件")
    reject("array", "[1, 2]", "不是校准方案文件")
    reject("other kind", DOC.replace("naruto-timer-calibration", "something-else"), "kind")
    reject("no kind", DOC.replace("\"kind\": \"naruto-timer-calibration\",", ""), "kind")
    reject("version 2", DOC.replace("\"version\": 1", "\"version\": 2"), "版本 2 不支持")
    reject("version string", DOC.replace("\"version\": 1", "\"version\": \"1\""), "应为数字")
    reject("no version", DOC.replace("\"version\": 1,", ""), "缺少 version")
    reject("profile", DOC.replace("\"duel\"", "\"arena\""), "profile")
    reject("past the right edge", DOC.replace("\"x\": 0.924", "\"x\": 0.95"), "超出画面")
    reject("negative", DOC.replace("\"x\": 0.081", "\"x\": -0.01"), "超出画面")
    reject("past the bottom", DOC.replace("\"y\": 0.104", "\"y\": 0.99"), "超出画面")
    reject("zero width", DOC.replace("\"width\": 0.052", "\"width\": 0"), "宽高")
    // Android's org.json refuses NaN itself; the box check would catch it otherwise.
    reject("NaN", DOC.replace("\"x\": 0.081", "\"x\": NaN"), "不是校准方案文件")
    reject("truncated", DOC.substring(0, DOC.length / 2), "不是校准方案文件")
    reject("quoted number", DOC.replace("\"x\": 0.081", "\"x\": \"0.081\""), "缺少数字 x")
    reject("missing height", DOC.replace(", \"height\": 0.022", ""), "缺少数字 height")
    reject("box as array", DOC.replace("{\"x\": 0.081, \"y\": 0.104, \"width\": 0.052, \"height\": 0.022}", "[0.081, 0.104, 0.052, 0.022]"), "左侧豆")

    // Aspect hint (> 2 % warns) and the default document name.
    val aspect = { w: Int, h: Int -> CalibrationScheme.aspectDifference(doc, w, h)!! }
    verify(aspect(1280, 576) < 1e-9) { "same 20:9 frame at capture width" }
    verify(aspect(2412, 1080) < CalibrationScheme.ASPECT_TOLERANCE) { "0.5 % is fine" }
    verify(aspect(1920, 1080) > CalibrationScheme.ASPECT_TOLERANCE) { "16:9 vs 20:9 warns" }
    verify(aspect(1080, 2400) > CalibrationScheme.ASPECT_TOLERANCE) { "portrait warns" }
    verify(CalibrationScheme.fileName(2400, 1080) == "校准方案-2400x1080.json") { "file name" }
}

// ---- 校准值是标准忍者值 (CalibrationRow) ----

/** The editor's stored form (CalibrationActivity.toStored: 5 decimals). */
private fun round5(b: FloatArray) = FloatArray(4) { Math.round(b[it] * 100_000f) / 100_000f }

/** Largest coordinate difference of two x, y, w, h boxes in frame pixels. */
private fun pxDiff(a: FloatArray, b: FloatArray, w: Int, h: Int): Double = maxOf(
    abs(a[0] - b[0]).toDouble() * w, abs(a[1] - b[1]).toDouble() * h,
    abs(a[2] - b[2]).toDouble() * w, abs(a[3] - b[3]).toDouble() * h,
)

/** Float noise of a frame-fraction box, in pixels (a Float y near 0.1 is good to ~2e-5 px at 1800). */
private const val PX_EPS = 1e-3

private fun centreY(b: FloatArray, h: Int) = (b[1].toDouble() + b[3].toDouble() / 2) * h

private fun show(b: FloatArray) = b.joinToString(prefix = "[", postfix = "]")

/** The editor's 2 px nudges on a box: moves and every edge, both ways. */
private fun nudges(w: Int, h: Int): List<Pair<String, (FloatArray) -> FloatArray>> {
    val dx = 2f / w
    val dy = 2f / h
    return listOf(1f, -1f).flatMap { k ->
        listOf<Pair<String, (FloatArray) -> FloatArray>>(
            "move x $k" to { b -> floatArrayOf(b[0] + k * dx, b[1], b[2], b[3]) },
            "move y $k" to { b -> floatArrayOf(b[0], b[1] + k * dy, b[2], b[3]) },
            "left edge $k" to { b -> floatArrayOf(b[0] + k * dx, b[1], b[2] - k * dx, b[3]) },
            "right edge $k" to { b -> floatArrayOf(b[0], b[1], b[2] + k * dx, b[3]) },
            "top edge $k" to { b -> floatArrayOf(b[0], b[1] + k * dy, b[2], b[3] - k * dy) },
            "bottom edge $k" to { b -> floatArrayOf(b[0], b[1], b[2], b[3] + k * dy) },
        )
    }
}

/**
 * Engine model: a standard bead box (pitch = s·pitch_P of the 1600×900 layout), the row dropped by
 * units·s·1600/960 (13 水门/鼬, 9 佐助) and sampled at whole pixels, as SideGeometry reports it.
 */
private fun rows() {
    val level = floatArrayOf(0.08f, 0.1f, 0.05f, 0.025f)
    verify(CalibrationRow.drop(IntArray(0), level, 2400, 1080) == 0.0) { "no beads" }
    verify(CalibrationRow.drop(intArrayOf(300, 300, 300), level, 2400, 1080) == 0.0) { "three beads" }
    verify(CalibrationRow.drop(IntArray(4) { 300 }, null, 2400, 1080) == 0.0) { "no box" }
    verify(CalibrationRow.drop(IntArray(4) { 300 }, level, 0, 1080) == 0.0) { "no frame size" }
    verify(CalibrationRow.drop(IntArray(4) { 300 }, floatArrayOf(0.08f, 0.1f, 0f, 0.025f), 2400, 1080) == 0.0) { "empty box" }
    verify(CalibrationRow.shown(level, 0.0, 2400, 1080).contentEquals(level)) { "no drop: shown as is" }
    verify(CalibrationRow.standard(level, 0.0, 2400, 1080).contentEquals(level)) { "no drop: stored as is" }
    val narrow = CalibrationRow.shift(0.8, 0.05f, 2400, 1080)
    verify(abs(CalibrationRow.shift(0.8, 0.1f, 2400, 1080) - 2 * narrow) < 1e-7 && abs(narrow * 1080 - 0.8 * 30) < 1e-3) {
        "shift = drop·its own pitch: $narrow"
    }

    val rnd = Random(20261021)
    val frames = listOf(1280 to 576, 1280 to 720, 1600 to 900, 1920 to 1080, 2400 to 1080, 2560 to 1600, 2880 to 1800, 3200 to 1440)
    var cases = 0
    var worstDrop = 0.0
    var worstNudge = 0.0
    var worstTight = 0.0
    for ((w, h) in frames) for (pitchP in listOf(25.0, 77.0 / 3, 76.0 / 3)) for (units in listOf(13.0, 9.0)) repeat(25) {
        val s = minOf(w / 1600.0, h / 900.0) * (0.95 + 0.1 * rnd.nextDouble())
        val pitch = s * pitchP
        val c0 = w * (0.05 + 0.03 * rnd.nextDouble())
        val std = floatArrayOf(
            ((c0 - pitch / 2) / w).toFloat(), (h * (0.08 + 0.04 * rnd.nextDouble()) / h).toFloat(),
            (4 * pitch / w).toFloat(), (pitch / h).toFloat(),
        )
        val row = centreY(std, h)
        val boxPitch = std[2].toDouble() * w / 4
        val off = units * s * 1600 / 960
        val sampled = (row + off).roundToInt()
        val tag = "${w}x$h pitch $pitchP units $units ${show(std)}"
        // Slots 5/6 (柱间) do not count; a level or raised row is no drop.
        val d = CalibrationRow.drop(intArrayOf(sampled, sampled, sampled, sampled, sampled + 40, sampled + 40), std, w, h)
        worstDrop = max(worstDrop, abs(d * boxPitch - off))
        verify(d >= CalibrationRow.MIN_DROP && abs(d * boxPitch - off) <= 0.5 + 1e-6) { "$tag: drop $d" }
        verify(CalibrationRow.drop(IntArray(4) { row.roundToInt() }, std, w, h) == 0.0) { "$tag: level row" }
        verify(CalibrationRow.drop(IntArray(4) { (row - off).roundToInt() }, std, w, h) == 0.0) { "$tag: raised row" }

        // The lowered default sits on the sampled beads; left as is, it is the default again.
        val shown = CalibrationRow.shown(std, d, w, h)
        verify(shown[0] == std[0] && shown[2] == std[2] && shown[3] == std[3] && abs(centreY(shown, h) - sampled) < PX_EPS) {
            "$tag: shown ${show(shown)} vs beads at $sampled"
        }
        verify(pxDiff(CalibrationRow.standard(shown, d, w, h), std, w, h) < PX_EPS) { "$tag: lowered default changed" }
        val std5 = round5(std)
        verify(round5(CalibrationRow.standard(CalibrationRow.shown(std5, d, w, h), d, w, h)).contentEquals(std5)) {
            "$tag: stored box changed"
        }

        // A 2 px nudge on the lowered box = the standard box nudged the same way (≤ 0.5 px).
        for ((label, nudge) in nudges(w, h)) {
            val diff = pxDiff(CalibrationRow.standard(nudge(shown), d, w, h), nudge(std), w, h)
            worstNudge = max(worstNudge, diff)
            verify(diff <= 0.5 + PX_EPS) { "$tag: $label off by $diff px" }
        }

        // A fresh tight box on the visible beads (true row, or from the sampled pixels) is the standard row (≤ 0.5 px).
        val xs = IntArray(4) { (c0 + it * pitch).roundToInt() }
        val pv = (xs[3] - xs[0]) / 3.0
        val tight = listOf(
            floatArrayOf(std[0], ((row + off - pitch / 2) / h).toFloat(), std[2], std[3]),
            floatArrayOf(((xs[0] - pv / 2) / w).toFloat(), ((sampled - pv / 2) / h).toFloat(), (4 * pv / w).toFloat(), (pv / h).toFloat()),
        )
        for (t in tight) {
            val got = CalibrationRow.standard(t, d, w, h)
            val diff = abs(centreY(got, h) - row)
            worstTight = max(worstTight, diff)
            verify(diff <= 0.5 + PX_EPS && got[0] == t[0] && got[2] == t[2] && got[3] == t[3]) { "$tag: tight ${show(t)} -> ${show(got)} off by $diff px" }
        }
        cases++
    }
    println(
        "  $cases dropped rows: drop within ${"%.3f".format(worstDrop)} px, lowered default unchanged, " +
            "2 px nudges within ${"%.3f".format(worstNudge)} px, tight boxes within ${"%.3f".format(worstTight)} px",
    )

    // Display ↔ standard round trips (bead-box sized boxes), exact once stored.
    repeat(3000) {
        val w = rnd.nextInt(640, 5000)
        val h = (w / (0.5 + 2.5 * rnd.nextDouble())).toInt().coerceAtLeast(1)
        val d = CalibrationRow.MIN_DROP + 1.4 * rnd.nextDouble()
        val b = floatArrayOf(rnd.nextFloat() * 0.8f, rnd.nextFloat() * 0.5f, 0.001f + rnd.nextFloat() * 0.2f, 0.001f + rnd.nextFloat() * 0.1f)
        val there = CalibrationRow.shown(CalibrationRow.standard(b, d, w, h), d, w, h)
        val back = CalibrationRow.standard(CalibrationRow.shown(b, d, w, h), d, w, h)
        verify(pxDiff(there, b, 1, 1) <= 1e-6 && pxDiff(back, b, 1, 1) <= 1e-6) { "round trip ${show(b)} d $d ${w}x$h" }
        val b5 = round5(b)
        val stored = round5(CalibrationRow.standard(CalibrationRow.shown(b5, d, w, h), d, w, h))
        verify(stored.contentEquals(b5) && round5(CalibrationRow.shown(CalibrationRow.standard(b5, d, w, h), d, w, h)).contentEquals(b5)) {
            "stored round trip ${show(b5)} d $d ${w}x$h"
        }
    }
    println("  3000 display ↔ standard round trips: within 1e-6, stored boxes exact")
}

fun main() {
    try {
        println("CalibrationScheme on ${System.getProperty("java.vm.name")} ${System.getProperty("java.vm.version")}, org.json from the device")
        run()
        rows()
    } catch (t: Throwable) {
        println("FAIL after $checks checks: $t")
        t.printStackTrace(System.out)
        exitProcess(1)
    }
    println("PASS: $checks checks")
}
