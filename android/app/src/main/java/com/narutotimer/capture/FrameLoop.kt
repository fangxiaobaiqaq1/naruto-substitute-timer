package com.narutotimer.capture

import android.graphics.Bitmap
import android.graphics.ColorSpace
import android.hardware.HardwareBuffer
import com.narutotimer.AnalysisResult
import com.narutotimer.AssetLoader
import com.narutotimer.NativeCore
import com.narutotimer.TimerApp
import java.nio.ByteBuffer
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

class CapturedFrame(
    val buffer: HardwareBuffer,
    val capturedAtNanos: Long,
    val method: Int,
    private val release: () -> Unit,
) {
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
    )

    private val started = AtomicBoolean(false)
    private val generation = AtomicLong(0)
    private val wake = Object()
    @Volatile private var pending: CapturedFrame? = null
    @Volatile private var thread: Thread? = null
    private val analyzed = AtomicLong(0)
    private val dropped = AtomicLong(0)
    private val totalNs = AtomicLong(0)
    @Volatile private var lastMethod = 0
    @Volatile private var lastWidth = 0
    @Volatile private var lastHeight = 0
    @Volatile private var lastResult: AnalysisResult? = null

    fun start(context: android.content.Context) {
        if (!started.compareAndSet(false, true)) return
        val run = generation.incrementAndGet()
        val app = context.applicationContext as TimerApp
        val t = Thread({
            while (started.get() && generation.get() == run) {
                val frame = synchronized(wake) {
                    while (started.get() && generation.get() == run && pending == null) {
                        try { wake.wait(100) } catch (_: InterruptedException) { /* re-check state */ }
                    }
                    val x = pending
                    pending = null
                    x
                } ?: continue
                try {
                    if (!started.get() || generation.get() != run) continue
                    if (app.nativeStatus != TimerApp.NativeStatus.READY) continue
                    val begin = System.nanoTime()
                    val result = analyze(frame)
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
                    frame.close()
                }
            }
        }, "nt-analysis-$run")
        thread = t
        t.start()
    }

    fun stop() {
        if (!started.compareAndSet(true, false)) return
        generation.incrementAndGet()
        synchronized(wake) {
            pending?.close()
            pending = null
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
            val old = pending
            pending = frame
            if (old != null) {
                old.close()
                dropped.incrementAndGet()
            }
            wake.notifyAll()
        }
    }

    fun offerFailure(message: String, method: Int) {
        val app = runCatching { TimerApp.instance }.getOrNull() ?: return
        app.session.onFrame(AnalysisResult.failure(message, System.nanoTime(), method.toString()))
    }

    fun stats(): Stats {
        val n = analyzed.get()
        return Stats(n, dropped.get(), if (n == 0L) 0.0 else totalNs.get().toDouble() / n / 1e6, lastMethod, lastWidth, lastHeight)
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

    private const val STOP_JOIN_MS = 1_000L
}
