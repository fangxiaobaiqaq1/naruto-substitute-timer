package schemecheck

import com.narutotimer.ui.CalibrationScheme
import com.narutotimer.ui.CalibrationScheme.Scheme
import kotlin.random.Random
import kotlin.system.exitProcess

// CalibrationScheme (docs/区域校准.md 方案文件) with the device's real org.json:
// the doc example, exact round trips, rejections with a message, and the aspect hint.

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

fun main() {
    try {
        println("CalibrationScheme on ${System.getProperty("java.vm.name")} ${System.getProperty("java.vm.version")}, org.json from the device")
        run()
    } catch (t: Throwable) {
        println("FAIL after $checks checks: $t")
        t.printStackTrace(System.out)
        exitProcess(1)
    }
    println("PASS: $checks checks")
}
