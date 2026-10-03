package com.narutotimer.capture

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.AccessibilityService.ScreenshotResult
import android.accessibilityservice.AccessibilityService.TakeScreenshotCallback
import android.os.Handler
import android.os.Looper
import android.view.Display
import com.narutotimer.NativeCore
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

class ScreenshotCapture(private val service: AccessibilityService) {
    private val runningState = AtomicBoolean(false)
    private val requestInFlight = AtomicBoolean(false)
    private val runGeneration = AtomicLong(0)
    private val handler = Handler(Looper.getMainLooper())
    @Volatile private var executor: ExecutorService? = null

    private val tick = object : Runnable {
        override fun run() {
            if (!runningState.get()) return
            val run = runGeneration.get()
            if (!requestInFlight.compareAndSet(false, true)) return
            val exec = executor
            if (exec == null || exec.isShutdown) {
                requestInFlight.set(false)
                scheduleNext(run)
                return
            }
            try {
                service.takeScreenshot(Display.DEFAULT_DISPLAY, exec, object : TakeScreenshotCallback {
                    override fun onSuccess(result: ScreenshotResult) {
                        var handedOff = false
                        var hb: android.hardware.HardwareBuffer? = null
                        try {
                            hb = result.hardwareBuffer
                            if (hb != null && runningState.get() && runGeneration.get() == run) {
                                val owned = hb
                                FrameLoop.offer(CapturedFrame(owned, System.nanoTime(), NativeCore.METHOD_SCREENSHOT) {
                                    runCatching { owned.close() }
                                })
                                handedOff = true
                            }
                        } finally {
                            if (!handedOff) runCatching { hb?.close() }
                            requestInFlight.set(false)
                            scheduleNext(run)
                        }
                    }

                    override fun onFailure(errorCode: Int) {
                        requestInFlight.set(false)
                        scheduleNext(run)
                    }
                })
            } catch (_: RejectedExecutionException) {
                requestInFlight.set(false)
                scheduleNext(run)
            } catch (_: Throwable) {
                requestInFlight.set(false)
                scheduleNext(run)
            }
        }
    }

    fun start() {
        if (!runningState.compareAndSet(false, true)) return
        runGeneration.incrementAndGet()
        executor = Executors.newSingleThreadExecutor { r -> Thread(r, "nt-a11y-shot") }
        FrameLoop.start(service)
        handler.removeCallbacks(tick)
        handler.post(tick)
    }

    fun stop() {
        if (!runningState.compareAndSet(true, false)) return
        runGeneration.incrementAndGet()
        handler.removeCallbacks(tick)
        requestInFlight.set(false)
        executor?.shutdownNow()
        executor = null
    }

    val running: Boolean get() = runningState.get()

    private fun scheduleNext(run: Long) {
        if (runningState.get() && runGeneration.get() == run) {
            handler.removeCallbacks(tick)
            handler.postDelayed(tick, INTERVAL_MS)
        }
    }

    companion object { const val INTERVAL_MS = 333L }
}
