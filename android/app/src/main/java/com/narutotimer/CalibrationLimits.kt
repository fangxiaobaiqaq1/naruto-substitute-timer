package com.narutotimer

import java.util.Locale

/**
 * Which region-calibration boxes are used (docs/区域校准.md「框的大小限制」). The same rule, with the
 * same double arithmetic, as Go internal/config/calibration_limits.go and the native core
 * (nt::config::CalibrationRectIssue, configured_layout.cpp): [Settings.calibRect], the scheme
 * import ([com.narutotimer.ui.CalibrationScheme]) and the editor accept exactly the boxes the
 * engine uses. A box that fails is not used: that region stays automatic.
 *
 * Only the box's own fractions count, never the frame size. u, v = its width and height in units
 * of the automatic box at HUD scale 1 along each axis (bead box 16w, 36h; portrait box 960w/68.5,
 * 540h/68.5); on every frame the automatic box has max(u, v) = 1, so k = max(u, v) is how many
 * times the automatic box it is. Usable: [MIN_SCALE] ≤ k ≤ [MAX_SCALE] and min(u, v) ≥ k·[MIN_SHAPE].
 *
 * Pure Kotlin (no Android types): android/tools/schemecheck compiles it with CalibrationScheme.
 */
object CalibrationLimits {
    const val MAX_SCALE = 2.0
    const val MIN_SCALE = 0.5
    const val MIN_SHAPE = 0.25

    /** Boxes 2 and 3 of [Settings.KEY_CALIB_RECTS] / CalibrationScheme.KEYS are portrait boxes. */
    fun isAvatar(index: Int): Boolean = index >= 2

    /**
     * x, y, w, h inside the frame with a positive size, checked like the native core does it
     * (the Float values widened to double: x + w ≤ 1, y + h ≤ 1).
     */
    fun inFrame(b: FloatArray): Boolean =
        b.size == 4 && inFrame(b[0].toDouble(), b[1].toDouble(), b[2].toDouble(), b[3].toDouble())

    /** [inFrame] in double (x, y, w, h fractions of the frame). */
    fun inFrame(x: Double, y: Double, w: Double, h: Double): Boolean =
        x >= 0 && y >= 0 && w > 0 && h > 0 && x + w <= 1 && y + h <= 1

    /** null when the size of [b] (an [inFrame] box) is usable, else why it is not (for the player). */
    fun issue(avatar: Boolean, b: FloatArray): String? = issue(avatar, b[2].toDouble(), b[3].toDouble())

    /** [issue] for a box [w] × [h] (fractions of the frame). */
    fun issue(avatar: Boolean, w: Double, h: Double): String? {
        val u: Double
        val v: Double
        if (avatar) {
            u = w * 960 / 68.5
            v = h * 540 / 68.5
        } else {
            u = w * 16
            v = h * 36
        }
        var k = u
        var short = v
        if (v > u) {
            k = v
            short = u
        }
        val kind = if (avatar) "头像框" else "豆框"
        return when {
            !(k <= MAX_SCALE) -> "${kind}太大：是自动框的 ${format(k, 2)} 倍（只用 0.5–2 倍的框）"
            !(k >= MIN_SCALE) -> "${kind}太小：只有自动框的 ${format(k, 2)} 倍（只用 0.5–2 倍的框）"
            !(short >= k * MIN_SHAPE) -> "${kind}太扁或太窄：宽高按自动框折算相差 ${format(k / short, 1)} 倍（最多 4 倍）"
            else -> null
        }
    }

    /** What the engine uses for box [index]: inside the frame and within the size limits. */
    fun usable(index: Int, b: FloatArray): Boolean = inFrame(b) && issue(isAvatar(index), b) == null

    private fun format(v: Double, digits: Int): String = String.format(Locale.ROOT, "%.${digits}f", v)
}
