package com.narutotimer.capture

import android.graphics.Bitmap
import android.graphics.ColorSpace
import android.graphics.PixelFormat
import android.hardware.HardwareBuffer
import android.media.Image
import android.os.Handler
import android.os.Looper
import android.util.Log
import com.narutotimer.AnalysisResult
import com.narutotimer.AssetLoader
import com.narutotimer.FrameGeometry
import com.narutotimer.NativeCore
import com.narutotimer.TimerApp
import java.nio.ByteBuffer
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

class CapturedFrame(
    val buffer: HardwareBuffer,
    val capturedAtNanos: Long,
    val method: Int,
    /**
     * CPU view of the same pixels when the source has one (ImageReader frames). Only
     * [FrameLoop.requestSnapshot] reads it; recognition always uses [buffer].
     */
    val image: Image? = null,
    /**
     * When the source asked for this frame (System.nanoTime), if it is asked for and may arrive
     * late (takeScreenshot); 0 = produced as it happened. Snapshots never use a frame requested
     * before the snapshot itself, so a slow screenshot cannot show an older screen.
     */
    val requestedAtNanos: Long = 0L,
    private val release: () -> Unit,
) {
    /** Offer order, assigned by [FrameLoop.offer] (snapshot requests wait for a newer frame). */
    internal var sequence = 0L
    private val closed = AtomicBoolean(false)
    fun close() { if (closed.compareAndSet(false, true)) runCatching { release() } }
}

/** A single-owner, latest-frame analysis loop. */
object FrameLoop {
    data class Stats(
        val analyzedFrames: Long,
        val droppedFrames: Long,
        val avgAnalyzeMillis: Double,
        val lastMethod: Int,
        val lastWidth: Int,
        val lastHeight: Int,
        val lastScene: String,
        val lastGateScore: Double,
        val lastUncertain: Boolean,
    )

    private val started = AtomicBoolean(false)
    private val generation = AtomicLong(0)
    private val wake = Object()
    @Volatile private var pending: CapturedFrame? = null
    /**
     * Frame pacing (guarded by [wake]): System.nanoTime before which the analysis thread does not
     * start another frame; see [takeFrame].
     */
    private var notBefore = 0L
    @Volatile private var thread: Thread? = null
    private val analyzed = AtomicLong(0)
    private val dropped = AtomicLong(0)
    private val totalNs = AtomicLong(0)
    @Volatile private var lastMethod = 0
    @Volatile private var lastWidth = 0
    @Volatile private var lastHeight = 0
    @Volatile private var lastResult: AnalysisResult? = null

    /** A calibration snapshot ([requestSnapshot]): one frame and what its analysis used. */
    class Snapshot(
        /** Software ARGB_8888 copy of the frame; the receiver owns it. */
        val bitmap: Bitmap,
        /** "camp" / "duel" when the frame was recognised as a match, otherwise "". */
        val layoutProfile: String,
        /**
         * [NativeCore.lastGeometry] of the same analysis (sequence and size checked; docs/区域校准.md
         * 每帧几何); null when there is none (failed analysis, engine rebuilt in between).
         */
        val geometry: FrameGeometry?,
        /**
         * Ninjas that same analysis recognised (index 0 left, 1 right: [AnalysisResult.leftNinja] /
         * [AnalysisResult.rightNinja]); "" = none, and always "" without [geometry].
         */
        val ninjas: List<String> = listOf("", ""),
    )

    /** A pending one-shot snapshot ([requestSnapshot]); null in normal operation. */
    private class SnapshotRequest(
        val afterSequence: Long,
        val requestedAtNanos: Long,
        val callback: (Snapshot) -> Unit,
    ) {
        fun accepts(frame: CapturedFrame): Boolean =
            frame.sequence > afterSequence && (frame.requestedAtNanos == 0L || frame.requestedAtNanos - requestedAtNanos >= 0)
    }
    @Volatile private var snapshot: SnapshotRequest? = null
    /** Frames offered so far (guarded by [wake]). */
    private var offeredFrames = 0L
    private val mainHandler by lazy { Handler(Looper.getMainLooper()) }

    /**
     * Serialises CPU reads of [CapturedFrame.image] planes with ImageReader.close() on the
     * capture side (resize/stop), which closes acquired images and unmaps their planes.
     */
    internal val imageLock = Any()

    fun start(context: android.content.Context) {
        if (!started.compareAndSet(false, true)) return
        val run = generation.incrementAndGet()
        val app = context.applicationContext as TimerApp
        val t = Thread({
            var paceUntil = 0L
            while (started.get() && generation.get() == run) {
                val frame = takeFrame(run, paceUntil) ?: continue
                val begin = System.nanoTime()
                var result: AnalysisResult? = null
                try {
                    if (!started.get() || generation.get() != run) continue
                    if (app.nativeStatus != TimerApp.NativeStatus.READY) continue
                    result = analyze(frame)
                    val elapsed = System.nanoTime() - begin
                    if (result != null) {
                        lastResult = result
                        lastWidth = result.width
                        lastHeight = result.height
                        // Heartbeats are only valid after a freshly captured frame was analyzed.
                        app.session.onFrame(result)
                        app.session.onHeartbeat(System.nanoTime())
                        AssetLoader.drainOppCrop(app)
                    }
                    totalNs.addAndGet(elapsed)
                    analyzed.incrementAndGet()
                    lastMethod = frame.method
                } catch (t: Throwable) {
                    app.session.onFrame(AnalysisResult.failure(t.message ?: "analysis failed", frame.capturedAtNanos, "native"))
                } finally {
                    // Without a pending request this is one volatile read per frame.
                    val shot = snapshot
                    if (shot != null && shot.accepts(frame)) {
                        runCatching { takeSnapshot(frame, shot, result) }
                    }
                    frame.close()
                    // Go nextCaptureWait: the next frame starts no earlier than this one's start +
                    // pollIntervalMs (16 ms in a fight, 400 ms otherwise), read after onFrame so the
                    // first fight frame already switches to the fight interval.
                    paceUntil = begin + app.session.pollIntervalMs() * 1_000_000L
                }
            }
        }, "nt-analysis-$run")
        thread = t
        t.start()
    }

    /**
     * The newest offered frame, taken no earlier than [paceUntil] (System.nanoTime): frames offered
     * while the analysis waits for its pace replace each other in [pending] (newest wins, the
     * older is closed at once), so a 60–120 Hz projection is analysed at most every
     * pollIntervalMs. The wait is measured from the previous frame's start (Go nextCaptureWait),
     * so an analysis slower than the interval never waits, and a frame that arrives after the
     * deadline starts at once. A pending snapshot request skips the wait for the frame it accepts.
     * Null once the loop stopped or restarted.
     */
    private fun takeFrame(run: Long, paceUntil: Long): CapturedFrame? = synchronized(wake) {
        notBefore = paceUntil
        while (started.get() && generation.get() == run) {
            val frame = pending
            val left = paceUntil - System.nanoTime()
            if (frame != null && (left <= 0L || snapshot?.accepts(frame) == true)) {
                pending = null
                return frame
            }
            // Before the deadline offer() does not wake this thread (the newest frame simply
            // waits in pending); after it, the next offer does.
            val waitNs = if (left > 0L) left else IDLE_WAIT_NS
            try {
                wake.wait(waitNs / 1_000_000L, (waitNs % 1_000_000L).toInt())
            } catch (_: InterruptedException) { /* re-check state */ }
        }
        null
    }

    fun stop() {
        if (!started.compareAndSet(true, false)) return
        generation.incrementAndGet()
        synchronized(wake) {
            pending?.close()
            pending = null
            notBefore = 0L
            snapshot = null // The requester's own timeout reports the failure.
            wake.notifyAll()
        }
        val old = thread
        thread = null
        old?.interrupt()
        if (old != null && old !== Thread.currentThread()) runCatching { old.join(STOP_JOIN_MS) }
    }

    val running: Boolean get() = started.get()

    fun offer(frame: CapturedFrame) {
        if (!started.get()) {
            frame.close()
            return
        }
        synchronized(wake) {
            if (!started.get()) {
                frame.close()
                return
            }
            frame.sequence = ++offeredFrames
            val old = pending
            pending = frame
            if (old != null) {
                old.close()
                dropped.incrementAndGet()
            }
            // While the analysis paces itself it sleeps until its deadline; waking it for every
            // frame of a 60–120 Hz source would only put it back to sleep.
            if (System.nanoTime() - notBefore >= 0L || snapshot != null) wake.notifyAll()
        }
    }

    /**
     * One-shot frame snapshot (region calibration). The next frame offered after this call (and,
     * for screenshots, also requested after it) is analysed as usual, then copied to a software
     * ARGB_8888 bitmap before it is closed, and
     * [callback] receives a [Snapshot] on the main thread: the bitmap, that frame's layout profile
     * ("camp" / "duel" when the frame was recognised as a match, otherwise "" — lobby, VS, a
     * non-game screen, native not ready; the engine's camp/duel pick is only a guess there) and
     * the geometry that same analysis used, read on this thread right after it, with the ninjas it recognised.
     * A frame that cannot be copied is skipped and the next one is tried. A newer request
     * replaces a pending one; [stop] drops it, so callers should apply their own timeout.
     * Returns false when the loop is not running.
     */
    fun requestSnapshot(callback: (Snapshot) -> Unit): Boolean {
        synchronized(wake) {
            if (!started.get()) return false
            snapshot = SnapshotRequest(offeredFrames, System.nanoTime(), callback)
        }
        return true
    }

    /** Withdraws a pending [requestSnapshot] made with [callback] (no-op once delivered). */
    fun cancelSnapshot(callback: (Snapshot) -> Unit) {
        synchronized(wake) {
            if (snapshot?.callback === callback) snapshot = null
        }
    }

    fun offerFailure(message: String, method: Int) {
        val app = runCatching { TimerApp.instance }.getOrNull() ?: return
        app.session.onFrame(AnalysisResult.failure(message, System.nanoTime(), method.toString()))
    }

    fun stats(): Stats {
        val n = analyzed.get()
        val r = lastResult
        return Stats(
            n, dropped.get(), if (n == 0L) 0.0 else totalNs.get().toDouble() / n / 1e6,
            lastMethod, lastWidth, lastHeight, r?.scene.orEmpty(), r?.gateScore ?: 0.0, r?.uncertain == true,
        )
    }

    /** Analysis thread, before [CapturedFrame.close]. */
    private fun takeSnapshot(frame: CapturedFrame, request: SnapshotRequest, result: AnalysisResult?) {
        if (!started.get()) return
        val begin = System.nanoTime()
        val bitmap = runCatching { copyFrame(frame) }
            .onFailure { Log.w(TAG, "snapshot copy failed", it) }
            .getOrNull() ?: return // Retried on the next frame.
        val claimed = synchronized(wake) {
            (snapshot === request).also { if (it) snapshot = null }
        }
        if (!claimed) {
            bitmap.recycle()
            return
        }
        // Same frame's analysis; a just-failed analysis falls back to a very recent result.
        val analysed = result ?: lastResult?.takeIf {
            it.analyzedAtNanos > 0 && System.nanoTime() - it.analyzedAtNanos in 0..SNAPSHOT_RESULT_MAX_AGE_NS
        }
        val profile = analysed?.takeIf { it.fighting }?.layoutProfile?.takeIf { it == "camp" || it == "duel" }.orEmpty()
        // The geometry of that same analysis: this is the only analysing thread, so nothing else
        // replaced it; a calibration rebuild in between clears it (null) and a failed frame has
        // sequence 0. The size check keeps a fallback result's geometry off a resized frame.
        val geometry = analysed?.takeIf { it.sequence > 0 }?.let { a ->
            runCatching { NativeCore.lastGeometry() }
                .onFailure { Log.w(TAG, "snapshot geometry failed", it) }
                .getOrNull()
                ?.takeIf { it.sequence == a.sequence && it.width == bitmap.width && it.height == bitmap.height }
        }
        val ninjas = if (geometry != null && analysed != null) listOf(analysed.leftNinja, analysed.rightNinja) else listOf("", "")
        Log.i(
            TAG,
            "snapshot ${bitmap.width}x${bitmap.height} method=${frame.method} " +
                "${if (frame.image != null) "image" else "buffer"} ${(System.nanoTime() - begin) / 1_000_000}ms " +
                "profile=$profile geometry=${geometry?.let { "seq ${it.sequence} ${it.layoutProfile}" } ?: "none"} " +
                "ninjas=${ninjas.joinToString("|")}",
        )
        val shot = Snapshot(bitmap, profile, geometry, ninjas)
        mainHandler.post { request.callback(shot) }
    }

    private fun copyFrame(frame: CapturedFrame): Bitmap? {
        // ImageReader frames (projection) usually lack GPU_SAMPLED_IMAGE usage, so they
        // are read from the locked CPU planes; screenshots wrap the buffer directly.
        frame.image?.let { image ->
            runCatching { synchronized(imageLock) { copyImage(image) } }
                .onFailure { Log.w(TAG, "snapshot plane copy failed", it) }
                .getOrNull()?.let { return it }
        }
        if (frame.buffer.usage and HardwareBuffer.USAGE_GPU_SAMPLED_IMAGE == 0L) return null
        val source = Bitmap.wrapHardwareBuffer(frame.buffer, ColorSpace.get(ColorSpace.Named.SRGB)) ?: return null
        return try {
            source.copy(Bitmap.Config.ARGB_8888, false)
        } finally {
            source.recycle()
        }
    }

    private fun copyImage(image: Image): Bitmap? {
        if (image.format != PixelFormat.RGBA_8888 && image.format != PixelFormat.RGBX_8888) return null
        val plane = image.planes.firstOrNull() ?: return null
        val width = image.width
        val height = image.height
        val rowBytes = width * 4
        val rowStride = plane.rowStride
        val src = plane.buffer.duplicate()
        if (width <= 0 || height <= 0 || plane.pixelStride != 4 || rowStride < rowBytes ||
            src.capacity() < rowStride.toLong() * (height - 1) + rowBytes) return null
        // Memory order R,G,B,A matches ARGB_8888 bitmaps; drop the row padding.
        val packed = ByteBuffer.allocateDirect(rowBytes * height)
        for (y in 0 until height) {
            val start = y * rowStride
            src.limit(start + rowBytes)
            src.position(start)
            packed.put(src)
        }
        packed.rewind()
        val bitmap = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
        bitmap.copyPixelsFromBuffer(packed)
        bitmap.setHasAlpha(false) // A screen frame is opaque (RGBX padding byte is not alpha).
        return bitmap
    }

    private fun analyze(frame: CapturedFrame): AnalysisResult? {
        val result = runCatching {
            NativeCore.analyzeHardwareBuffer(frame.buffer, frame.capturedAtNanos, frame.method)
        }.getOrNull()
        if (result != null) return result

        // Some emulator images expose a HardwareBuffer that JNI cannot CPU-lock. A
        // software copy keeps capture usable without changing the projection token.
        val error = NativeCore.lastError().lowercase()
        if (!error.contains("hardwarebuffer") && !error.contains("lock failed") &&
            !error.contains("unsupported") && !error.contains("bad arguments")) return null
        return runCatching { analyzeViaBitmap(frame) }.getOrNull()
    }

    private fun analyzeViaBitmap(frame: CapturedFrame): AnalysisResult? {
        val source = Bitmap.wrapHardwareBuffer(frame.buffer, ColorSpace.get(ColorSpace.Named.SRGB)) ?: return null
        val bitmap = try {
            source.copy(Bitmap.Config.ARGB_8888, false) ?: return null
        } finally {
            source.recycle()
        }
        try {
            val width = bitmap.width
            val height = bitmap.height
            if (width <= 0 || height <= 0) return null
            val pixels = IntArray(width * height)
            bitmap.getPixels(pixels, 0, width, 0, 0, width, height)
            val rgba = ByteBuffer.allocateDirect(width * height * 4)
            for (pixel in pixels) {
                rgba.put((pixel ushr 16).toByte())
                rgba.put((pixel ushr 8).toByte())
                rgba.put(pixel.toByte())
                rgba.put((pixel ushr 24).toByte())
            }
            rgba.flip()
            return NativeCore.analyzeBuffer(rgba, width, height, width * 4, frame.capturedAtNanos, frame.method)
        } finally {
            bitmap.recycle()
        }
    }

    private const val TAG = "FrameLoop"
    /** Longest single wait for a frame; the loop re-checks its state after it. */
    private const val IDLE_WAIT_NS = 100_000_000L
    private const val STOP_JOIN_MS = 1_000L
    private const val SNAPSHOT_RESULT_MAX_AGE_NS = 2_000_000_000L
}
