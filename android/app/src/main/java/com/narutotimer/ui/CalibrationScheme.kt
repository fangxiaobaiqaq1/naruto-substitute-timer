package com.narutotimer.ui

import org.json.JSONException
import org.json.JSONObject
import org.json.JSONTokener
import java.math.BigDecimal
import java.math.MathContext
import java.math.RoundingMode
import kotlin.math.abs
import kotlin.math.floor

/**
 * 校准方案文件（docs/区域校准.md「方案文件」，Windows 与安卓通用）：
 *
 * ```json
 * {
 *   "kind": "naruto-timer-calibration",
 *   "version": 1,
 *   "frameWidth": 2400,
 *   "frameHeight": 1080,
 *   "profile": "duel",
 *   "leftBeads": {"x": 0.081, "y": 0.104, "width": 0.052, "height": 0.022}
 * }
 * ```
 *
 * Only calibrated boxes are present (absent = automatic); frameWidth/Height are a hint only.
 * Pure Kotlin + org.json (no Android framework types), so it is checked on its own by
 * android/tools/schemecheck. Boxes are x, y, w, h fractions of the whole frame in
 * Settings.KEY_CALIB_RECTS order (left beads, right beads, left avatar, right avatar).
 */
object CalibrationScheme {
    const val KIND = "naruto-timer-calibration"
    const val VERSION = 1
    /** File keys, in Settings.KEY_CALIB_RECTS order. */
    val KEYS = listOf("leftBeads", "rightBeads", "leftAvatar", "rightAvatar")
    /** A scheme is a few hundred bytes; a file this large is something else. */
    const val MAX_BYTES = 64 * 1024
    /** Import warns when the file's frame aspect differs from the current frame by more than this. */
    const val ASPECT_TOLERANCE = 0.02

    private val NAMES = listOf("左侧豆", "右侧豆", "左侧头像", "右侧头像")
    /** Boxes written as float64 elsewhere may end a hair past the frame edge. */
    private const val EDGE_SLACK = 1e-6
    private const val MAX_FRAME_SIDE = 100_000

    class Scheme(
        /** "camp" / "duel": the layout of the frame the boxes were drawn on. */
        val profile: String,
        /** Size of that frame (hint only; 0 = unknown). */
        val frameWidth: Int,
        val frameHeight: Int,
        /** Four boxes (x, y, w, h) in [KEYS] order; null = automatic. */
        val boxes: List<FloatArray?>,
    )

    /** A file that cannot be imported; the message is shown to the player as is. */
    class FormatException(message: String) : Exception(message)

    /** Default document name, e.g. 校准方案-2400x1080.json. */
    fun fileName(frameWidth: Int, frameHeight: Int): String =
        if (frameWidth > 0 && frameHeight > 0) "校准方案-${frameWidth}x${frameHeight}.json" else "校准方案.json"

    /** The file text; boxes must already be valid calibration boxes (see [decode]). */
    fun encode(scheme: Scheme): String {
        require(scheme.profile == "camp" || scheme.profile == "duel") { "profile ${scheme.profile}" }
        require(scheme.boxes.size == KEYS.size) { "boxes ${scheme.boxes.size}" }
        val out = StringBuilder("{\n")
        out.append("  \"kind\": \"").append(KIND).append("\",\n")
        out.append("  \"version\": ").append(VERSION).append(",\n")
        out.append("  \"frameWidth\": ").append(scheme.frameWidth.coerceAtLeast(0)).append(",\n")
        out.append("  \"frameHeight\": ").append(scheme.frameHeight.coerceAtLeast(0)).append(",\n")
        out.append("  \"profile\": \"").append(scheme.profile).append('"')
        for ((i, key) in KEYS.withIndex()) {
            val b = scheme.boxes[i] ?: continue
            require(valid(b)) { "$key ${b.joinToString()}" }
            out.append(",\n  \"").append(key).append("\": {")
                .append("\"x\": ").append(number(b[0]))
                .append(", \"y\": ").append(number(b[1]))
                .append(", \"width\": ").append(number(b[2]))
                .append(", \"height\": ").append(number(b[3]))
                .append('}')
        }
        return out.append("\n}\n").toString()
    }

    /**
     * Parses and checks a scheme file. Throws [FormatException] (Chinese message) when the kind or
     * version is wrong, the profile is not camp/duel or a box is not a box inside the frame. Boxes
     * come back as valid calibration boxes (x, y ≥ 0, w, h > 0, x + w ≤ 1, y + h ≤ 1 in Float).
     */
    fun decode(text: String): Scheme {
        val root = try {
            JSONTokener(text.removePrefix("\uFEFF")).nextValue()
        } catch (e: JSONException) {
            throw FormatException("不是校准方案文件（不是有效的 JSON）。")
        }
        if (root !is JSONObject) throw FormatException("不是校准方案文件（内容不是 JSON 对象）。")
        if (root.opt("kind") != KIND) throw FormatException("不是校准方案文件（kind 不是 \"$KIND\"）。")
        val version = root.opt("version")
        if (version == null || version == JSONObject.NULL) throw FormatException("方案文件缺少 version。")
        if (version !is Number) throw FormatException("方案文件的 version 应为数字 $VERSION。")
        if (version.toDouble() != VERSION.toDouble()) {
            throw FormatException("方案文件版本 $version 不支持（只支持 version $VERSION），请更新计时器。")
        }
        val profile = root.opt("profile")
        if (profile != "camp" && profile != "duel") {
            throw FormatException("profile 必须是 \"camp\"（训练场）或 \"duel\"（决斗场）。")
        }
        return Scheme(
            profile = profile as String,
            frameWidth = frameSide(root.opt("frameWidth")),
            frameHeight = frameSide(root.opt("frameHeight")),
            boxes = KEYS.indices.map { box(root, it) },
        )
    }

    /** Relative difference of the scheme's frame aspect from [width]×[height]; null if either is unknown. */
    fun aspectDifference(scheme: Scheme, width: Int, height: Int): Double? {
        if (scheme.frameWidth <= 0 || scheme.frameHeight <= 0 || width <= 0 || height <= 0) return null
        val current = width.toDouble() / height
        return abs(scheme.frameWidth.toDouble() / scheme.frameHeight - current) / current
    }

    /** Same checks as Settings.calibRect. */
    fun valid(b: FloatArray): Boolean =
        b.size == 4 && b.all { it.isFinite() } && b[0] >= 0f && b[1] >= 0f && b[2] > 0f && b[3] > 0f &&
            b[0] + b[2] <= 1f && b[1] + b[3] <= 1f

    private fun box(root: JSONObject, index: Int): FloatArray? {
        val key = KEYS[index]
        val name = "${NAMES[index]}（$key）"
        val v = root.opt(key)
        if (v == null || v == JSONObject.NULL) return null
        if (v !is JSONObject) throw FormatException("${name}不是 {x, y, width, height}。")
        fun field(f: String): Double {
            val n = v.opt(f)
            val d = (n as? Number)?.toDouble()
            if (d == null || !d.isFinite()) throw FormatException("${name}缺少数字 $f。")
            return d
        }
        val x = field("x")
        val y = field("y")
        val w = field("width")
        val h = field("height")
        if (w <= 0.0 || h <= 0.0) throw FormatException("${name}的宽高必须大于 0。")
        if (x < -EDGE_SLACK || y < -EDGE_SLACK || x + w > 1 + EDGE_SLACK || y + h > 1 + EDGE_SLACK) {
            throw FormatException("${name}超出画面：坐标是整帧的比例，x、y 和 x+width、y+height 都要在 0–1 之间。")
        }
        return fit(x, y, w, h) ?: throw FormatException("${name}太小。")
    }

    /** The box as Float; only a box that is not valid as it is gets pulled inside the frame ([EDGE_SLACK], rounding). */
    private fun fit(x: Double, y: Double, w: Double, h: Double): FloatArray? {
        val exact = floatArrayOf(x.toFloat(), y.toFloat(), w.toFloat(), h.toFloat())
        if (valid(exact)) return exact
        val fx = x.coerceIn(0.0, 1.0).toFloat()
        val fy = y.coerceIn(0.0, 1.0).toFloat()
        var fw = w.toFloat().coerceAtMost(1f - fx)
        var fh = h.toFloat().coerceAtMost(1f - fy)
        while (fw > 0f && fx + fw > 1f) fw = Math.nextDown(fw)
        while (fh > 0f && fy + fh > 1f) fh = Math.nextDown(fh)
        return floatArrayOf(fx, fy, fw, fh).takeIf(::valid)
    }

    private fun frameSide(v: Any?): Int {
        val d = (v as? Number)?.toDouble() ?: return 0
        return if (d >= 1 && d <= MAX_FRAME_SIDE && d == floor(d)) d.toInt() else 0
    }

    /**
     * Shortest plain decimal (no exponent, valid JSON) that reads back as exactly [v], both as a
     * Float and through a Double (org.json parses numbers as Double; [decode] then narrows).
     */
    internal fun number(v: Float): String {
        val exact = BigDecimal(v.toDouble())
        for (digits in 1..9) {
            val s = exact.round(MathContext(digits, RoundingMode.HALF_EVEN)).stripTrailingZeros().toPlainString()
            if (s.toFloat() == v && s.toDouble().toFloat() == v) return s
        }
        return exact.toPlainString()
    }
}
