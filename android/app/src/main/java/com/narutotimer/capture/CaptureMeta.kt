package com.narutotimer.capture

import com.narutotimer.NativeCore

/**
 * Frame metadata that Session compares across frames. Pure functions (no Android or native
 * dependencies) so android/tools/sessioncheck can run them on the host.
 */
object CaptureMeta {
    /**
     * nt::frame::MethodName: the captureMethod native frames report. Failure frames must name
     * the same source, or Session (Go ObservationSpace) would see a source change at each failure.
     */
    fun methodName(method: Int): String = when (method) {
        NativeCore.METHOD_PROJECTION -> "android-media-projection"
        NativeCore.METHOD_SCREENSHOT -> "android-accessibility-screenshot"
        else -> "android-buffer"
    }

    /**
     * A screenshot frame is stamped when the system took it ([timestampMillis] is
     * ScreenshotResult.timestamp: SystemClock.uptimeMillis, the System.nanoTime clock), like Go
     * (capture time) and projection (Image.timestamp), not when the callback ran. Clamped to
     * [requestedAtNanos, nowNanos]: millisecond resolution and a bad value cannot reorder frames.
     */
    fun screenshotNanos(requestedAtNanos: Long, timestampMillis: Long, nowNanos: Long): Long =
        (timestampMillis * 1_000_000L).coerceIn(requestedAtNanos, maxOf(requestedAtNanos, nowNanos))
}
