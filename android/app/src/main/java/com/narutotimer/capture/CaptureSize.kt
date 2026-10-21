package com.narutotimer.capture

/**
 * Size of the projection VirtualDisplay (pure Kotlin, checked on the host by android/tools/appcheck).
 *
 * The game only runs in landscape, so the capture is always landscape with its long side
 * [LONG_SIDE], whatever the phone's orientation when capture starts. Sizing it from the current
 * window bounds made a portrait start 1280×2844: AUTO_MIRROR then letterboxes the landscape game
 * into a 1280×576 band, which the engine cannot read, and on Android 12–13 nothing ever resizes
 * the display (audit AND-01). A landscape start gets exactly the size it had before (same integer
 * arithmetic). Android 14+ still follows MediaProjection.Callback.onCapturedContentResize after
 * the start, unchanged.
 */
object CaptureSize {
    const val LONG_SIDE = 1280

    /** (width, height) of the capture for a screen of [boundsWidth]×[boundsHeight] px, either orientation. */
    fun landscape(boundsWidth: Int, boundsHeight: Int): Pair<Int, Int> {
        val long = maxOf(boundsWidth, boundsHeight, 1)
        val short = minOf(boundsWidth, boundsHeight).coerceAtLeast(0)
        val height = (LONG_SIDE.toLong() * short / long).toInt().coerceIn(1, LONG_SIDE)
        return LONG_SIDE to height
    }
}
