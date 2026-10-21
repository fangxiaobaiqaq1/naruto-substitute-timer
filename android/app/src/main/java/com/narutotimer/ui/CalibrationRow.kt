package com.narutotimer.ui

/**
 * 校准值是标准忍者值（docs/区域校准.md）：a saved bead box is always the standard (normal ninja) bead
 * row. On a frame where a side's sampled beads lie below its standard row (the engine recognised
 * 九喇嘛水门 / 侠隐佐助 / 百战鼬 there and dropped the whole row), the editor shows that side's bead
 * boxes lowered onto the visible beads ([shown]) and stores what is drawn there raised back to the
 * standard row ([standard]). The drop is in bead pitches, so every box moves by its own pitch, as
 * the engine drops a row by the scale of the box it uses.
 *
 * Pure Kotlin (no Android types), checked by android/tools/schemecheck. Boxes are x, y, w, h
 * fractions of the whole frame; [width] × [height] is the analysed frame in pixels.
 */
object CalibrationRow {
    /** Smaller drops (bead pitches) are only the whole-pixel rounding of the sampled centres: none. */
    const val MIN_DROP = 0.1

    /**
     * How far this frame's sampled bead row lies below the standard row, in bead pitches:
     * (mean y of the first 4 sampled centres [beadY] − standard row y) / pitch, with standard row
     * y = (box.y + box.h/2)·H and pitch = box.w·W/4, [beadBox] being this frame's standard box
     * (SideGeometry.beadBox). 0 below [MIN_DROP] or when anything is missing.
     */
    fun drop(beadY: IntArray, beadBox: FloatArray?, width: Int, height: Int): Double {
        if (beadY.size < 4 || beadBox == null || beadBox.size != 4 || width <= 0 || height <= 0) return 0.0
        val pitch = beadBox[2].toDouble() * width / 4
        if (!(pitch > 0.0)) return 0.0
        val row = (beadBox[1].toDouble() + beadBox[3].toDouble() / 2) * height
        val mean = (beadY[0].toDouble() + beadY[1] + beadY[2] + beadY[3]) / 4
        val d = (mean - row) / pitch
        return if (d >= MIN_DROP && d.isFinite()) d else 0.0
    }

    /** Vertical offset (fraction of the frame height) of a [boxWidth]-wide bead box between the standard row and a row [drop] pitches lower. */
    fun shift(drop: Double, boxWidth: Float, width: Int, height: Int): Float {
        if (!(drop > 0.0) || !(boxWidth > 0f) || width <= 0 || height <= 0) return 0f
        return (drop * boxWidth * width / 4 / height).toFloat()
    }

    /** A standard bead box as shown on the frame: lowered by drop·(its width·W/4) px. */
    fun shown(box: FloatArray, drop: Double, width: Int, height: Int): FloatArray =
        floatArrayOf(box[0], box[1] + shift(drop, box[2], width, height), box[2], box[3])

    /** A bead box drawn on the frame as its standard value: raised by drop·(its width·W/4) px. */
    fun standard(box: FloatArray, drop: Double, width: Int, height: Int): FloatArray =
        floatArrayOf(box[0], box[1] - shift(drop, box[2], width, height), box[2], box[3])
}
