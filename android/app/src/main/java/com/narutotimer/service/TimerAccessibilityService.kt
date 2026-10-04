package com.narutotimer.service

import android.accessibilityservice.AccessibilityService
import android.content.Context
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.util.Log
import android.view.accessibility.AccessibilityEvent
import com.narutotimer.Settings as AppSettings
import com.narutotimer.TimerApp
import com.narutotimer.capture.FrameLoop
import com.narutotimer.capture.ProjectionCaptureService
import com.narutotimer.capture.ScreenshotCapture
import com.narutotimer.overlay.OverlayController

/**
 * Owns the explicit overlay lifecycle. Binding the accessibility service only
 * establishes the connection; it never starts a window or a capture loop.
 */
class TimerAccessibilityService : AccessibilityService() {
    private val main = Handler(Looper.getMainLooper())
    private var overlay: OverlayController? = null
    private var fallback: ScreenshotCapture? = null
    private var connected = false

    override fun onServiceConnected() {
        super.onServiceConnected()
        connected = true
        instance = this
        Log.i(TAG, "accessibility service connected")
        // Deliberately do not start the overlay here. The user controls its
        // lifecycle from MainActivity (or the pill's explicit 退出 action).
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent?) {}
    override fun onInterrupt() {}

    /** Start the overlay and its recognition source, safely and idempotently. */
    fun startOverlay() = onMain { startOverlayOnMain() }

    /** Stop only the overlay and recognition loop; do not stop MediaProjection. */
    fun stopOverlay() = onMain { stopOverlayOnMain() }

    /** Stop and then start the overlay without touching the projection token. */
    fun restartOverlay() = onMain {
        stopOverlayOnMain()
        startOverlayOnMain()
    }

    private fun startOverlayOnMain() {
        if (!connected || instance !== this) return
        val app = application as? TimerApp ?: return
        if (!app.isReady()) return
        val appSettings = app.settings
        appSettings.overlayEnabled = true
        try {
            val controller = overlay ?: OverlayController(this, app.session, appSettings).also { overlay = it }
            if (!controller.isShowing) controller.show()
            if (!controller.isShowing) {
                Log.w(TAG, "overlay did not attach")
                return
            }
            FrameLoop.start(this)
            startScreenshotFallbackOnMain()
        } catch (t: Throwable) {
            Log.e(TAG, "overlay start failed", t)
            // Keep the persisted intent, but leave the actual lifecycle stopped.
            runCatching { overlay?.destroy() }
            overlay = null
            fallback?.stop()
            fallback = null
            FrameLoop.stop()
        }
    }

    private fun stopOverlayOnMain() {
        val appSettings = (application as? TimerApp)?.settings
        // Persist the explicit stop even if the service is already half torn
        // down. This prevents a later accessibility bind from reviving it.
        appSettings?.overlayEnabled = false
        runCatching { fallback?.stop() }
            .onFailure { Log.w(TAG, "fallback teardown failed", it) }
        fallback = null
        runCatching { overlay?.destroy() }
            .onFailure { Log.w(TAG, "overlay teardown failed", it) }
        overlay = null
        // ProjectionCaptureService intentionally remains alive. Its frames are
        // dropped until the user explicitly starts the overlay again.
        FrameLoop.stop()
    }

    override fun onDestroy() {
        connected = false
        if (instance === this) instance = null
        Log.i(TAG, "accessibility service destroyed")
        // onDestroy may race with a pending start/stop callback. Do not assume
        // that an overlay exists, and never let teardown escape as a service
        // fault.
        onMain {
            runCatching { stopOverlayOnMain() }
                .onFailure { Log.w(TAG, "accessibility teardown failed", it) }
        }
        super.onDestroy()
    }

    /** Start fallback only while the explicitly enabled overlay is running. */
    fun startScreenshotFallback() = onMain { startScreenshotFallbackOnMain() }

    fun stopScreenshotFallback() = onMain {
        runCatching { fallback?.stop() }
            .onFailure { Log.w(TAG, "fallback stop failed", it) }
        fallback = null
    }

    /** Opens a temporary drag mode after the in-overlay settings panel closes. */
    fun beginPositionEdit() = onMain { overlay?.startPositionEdit() }

    val isOverlayRunning: Boolean
        get() = overlay?.isShowing == true

    private fun startScreenshotFallbackOnMain() {
        val app = application as? TimerApp ?: return
        if (!connected || instance !== this || !app.settings.overlayEnabled || ProjectionCaptureService.running) return
        if (fallback == null) fallback = ScreenshotCapture(this)
        runCatching { fallback?.start() }
            .onFailure { Log.w(TAG, "fallback start failed", it) }
    }

    private fun onMain(action: () -> Unit) {
        if (Looper.myLooper() === Looper.getMainLooper()) action() else main.post(action)
    }

    companion object {
        private const val TAG = "TimerAccessibility"
        @Volatile var instance: TimerAccessibilityService? = null
            private set

        fun isEnabled(context: Context): Boolean {
            val expected = context.packageName + "/" + TimerAccessibilityService::class.java.name
            return Settings.Secure.getString(
                context.contentResolver,
                Settings.Secure.ENABLED_ACCESSIBILITY_SERVICES,
            )?.split(':')?.any { it.equals(expected, true) } == true
        }
    }
}
