package com.narutotimer.overlay

// Owner: kt-overlay-ui
// Android-native floating timer controller. The card can be dragged directly;
// a tap opens its action row.

import android.accessibilityservice.AccessibilityService
import android.content.Context
import android.graphics.PixelFormat
import android.graphics.Rect
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.view.ContextThemeWrapper
import android.view.Gravity
import android.view.View
import android.view.WindowInsets
import android.view.WindowManager
import android.view.accessibility.AccessibilityEvent
import android.view.inputmethod.InputMethodManager
import android.widget.FrameLayout
import com.narutotimer.R
import com.narutotimer.Settings
import com.narutotimer.capture.ProjectionCaptureService
import com.narutotimer.service.TimerAccessibilityService
import com.narutotimer.tracker.OverlayState
import com.narutotimer.tracker.Session
import com.narutotimer.ui.ProjectionPermissionActivity
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/** Owns the timer card and the in-overlay settings TYPE_ACCESSIBILITY_OVERLAY windows. */
class OverlayController(
    private val service: AccessibilityService,
    private val session: Session,
    private val settings: Settings,
) {
    private val wm: WindowManager = service.getSystemService(WindowManager::class.java)
    private val main = Handler(Looper.getMainLooper())

    private var view: MiniTimerView? = null
    private var settingsView: OverlaySettingsView? = null
    private var settingsRoot: FrameLayout? = null
    private val params = overlayParams()
    private val settingsParams = settingsOverlayParams()

    private var showing = false
    private var settingsVisible = false
    private var destroyed = false
    private var controlsVisible = false
    private var positionEditing = false
    private var rightDocked = false

    // Clock refresh is kept on the main thread. Revision polling avoids asking
    // Session for state on every 0.1-second repaint when the scene is idle.
    private var lastSessionRevision = Long.MIN_VALUE
    private var clockDeadline = 0L
    private var forceRender = true
    private var lastState: OverlayState? = null
    private var appliedOpacity = -1f
    private var lastErrorLogAt = 0L
    private var lastStatusAt = 0L

    private val tick = Runnable { tickNow() }
    private val autoHide = Runnable {
        setControls(false)
        setEditing(false)
    }
    private val settingsListener: (String) -> Unit = { key ->
        // Settings can be changed from MainActivity's thread. WindowManager is
        // always driven from the main looper.
        main.post { onSettingChanged(key) }
    }

    // ---- Public API -------------------------------------------------------

    /** Adds the timer window and starts the refresh loop. Main-thread API. */
    fun show() {
        if (destroyed || showing) return
        val v = MiniTimerView(service)
        v.listener = viewListener
        v.onConfigChanged = { main.post { reclamp() } }
        val state = safeState(System.nanoTime())
        if (state != null) {
            v.render(state)
            lastState = state
            params.alpha = state.opacity
            appliedOpacity = state.opacity
        }
        v.measure(View.MeasureSpec.UNSPECIFIED, View.MeasureSpec.UNSPECIFIED)
        placeFromSettings(safeRect(), v.measuredWidth, v.measuredHeight)

        try {
            wm.addView(v, params)
        } catch (e: RuntimeException) {
            Log.e(TAG, "add overlay failed", e)
            return
        }
        v.addOnLayoutChangeListener { _, l, t, r, b, ol, ot, or, ob ->
            if (r - l != or - ol || b - t != ob - ot) main.post { onViewResized() }
        }
        view = v
        showing = true
        settings.addListener(settingsListener)
        forceRender = true
        tickNow()
    }

    /** Removes the overlay windows and stops refresh. Main-thread API. */
    fun hide() {
        if (!showing && !settingsVisible) return
        closeSettings()
        showing = false
        main.removeCallbacks(tick)
        main.removeCallbacks(autoHide)
        settings.removeListener(settingsListener)
        controlsVisible = false
        positionEditing = false
        view?.let { v ->
            v.listener = null
            v.onConfigChanged = null
            runCatching { wm.removeViewImmediate(v) }
        }
        view = null
        lastState = null
        lastSessionRevision = Long.MIN_VALUE
        appliedOpacity = -1f
    }

    /** Service teardown hook. */
    fun destroy() {
        hide()
        destroyed = true
        main.removeCallbacksAndMessages(null)
    }

    val isShowing: Boolean get() = showing

    /** Highlights the card so it is easy to find and drag (from the settings screens). */
    fun startPositionEdit() {
        if (!showing) return
        setControls(false)
        setEditing(true)
        main.removeCallbacks(autoHide)
        main.postDelayed(autoHide, EDIT_AUTO_END_MS)
    }

    // ---- Refresh ----------------------------------------------------------

    private fun tickNow() {
        main.removeCallbacks(tick)
        if (!showing) return
        val now = System.nanoTime()
        val revision = session.revision
        if (forceRender || revision != lastSessionRevision || now - clockDeadline >= 0) {
            forceRender = false
            lastSessionRevision = revision
            val state = safeState(now)
            if (state != null) {
                lastState = state
                view?.render(state)
                applyOpacity(state.opacity)
            }
            val delay = runCatching { session.nextTickDelayNanos(now) }
                .getOrDefault(IDLE_TICK_NANOS)
            clockDeadline = now + delay.coerceAtLeast(MIN_TICK_NANOS)
        }
        if (settingsVisible && SystemClock.uptimeMillis() - lastStatusAt >= STATUS_REFRESH_MS) {
            updateSettingsStatus()
        }
        val wait = min(clockDeadline - now, REVISION_POLL_NANOS)
            .coerceAtLeast(MIN_TICK_NANOS)
        main.postDelayed(tick, (wait + 999_999) / 1_000_000)
    }

    private fun safeState(now: Long): OverlayState? = try {
        session.overlayState(now)
    } catch (e: RuntimeException) {
        val at = SystemClock.uptimeMillis()
        if (at - lastErrorLogAt > 5_000) {
            lastErrorLogAt = at
            Log.e(TAG, "overlayState failed", e)
        }
        null
    }

    private fun refreshSoon() {
        forceRender = true
        if (showing) main.post { tickNow() }
    }

    private fun onSettingChanged(key: String) {
        if (!showing) return
        settingsView?.refresh(key)
        when (key) {
            Settings.KEY_OVERLAY_X, Settings.KEY_OVERLAY_Y -> onPositionSettingChanged()
            else -> {
                forceRender = true
                tickNow()
            }
        }
    }

    private fun applyOpacity(opacity: Float) {
        if (abs(opacity - appliedOpacity) < 0.001f) return
        appliedOpacity = opacity
        params.alpha = opacity
        updateLayout()
    }

    // ---- Controls ---------------------------------------------------------

    private fun setControls(on: Boolean) {
        main.removeCallbacks(autoHide)
        if (on) main.postDelayed(autoHide, CONTROLS_AUTO_HIDE_MS)
        if (controlsVisible == on) return
        controlsVisible = on
        view?.controlsVisible = on
        view?.sendAccessibilityEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED)
    }

    private fun setEditing(on: Boolean) {
        if (positionEditing == on) return
        positionEditing = on
        view?.editing = on
    }

    private fun touchActivity() {
        if (controlsVisible || positionEditing) {
            main.removeCallbacks(autoHide)
            main.postDelayed(autoHide, if (positionEditing) EDIT_AUTO_END_MS else CONTROLS_AUTO_HIDE_MS)
        }
    }

    private fun currentState(): OverlayState? = safeState(System.nanoTime()) ?: lastState

    private fun toggleBothSides() {
        val on = !(currentState()?.showBothSides ?: settings.showBothSides)
        session.setShowBothSides(on)
        refreshSoon()
    }

    private fun toggleMini() {
        val on = !(currentState()?.mini ?: (settings.overlayMode == "mini"))
        setControls(false)
        session.setMiniMode(on)
        refreshSoon()
    }

    private fun openSettings() {
        if (!showing) return
        setControls(false)
        setEditing(false)
        if (settingsVisible) {
            settingsView?.refresh()
            settingsView?.requestFocus()
            updateSettingsLayout()
            return
        }

        val root = FrameLayout(service).apply {
            setBackgroundColor(SCRIM)
            isFocusable = true
            isFocusableInTouchMode = true
            // Tapping the dimmed area closes the panel, like a dialog.
            setOnClickListener { closeSettings() }
            setOnKeyListener { _, keyCode, event ->
                if (keyCode == android.view.KeyEvent.KEYCODE_BACK &&
                    event.action == android.view.KeyEvent.ACTION_UP
                ) {
                    closeSettings()
                    true
                } else false
            }
        }
        // A service context carries the light DeviceDefault theme; the panel's
        // framework widgets must use the app's dark theme to stay readable.
        val themed = ContextThemeWrapper(service, R.style.Theme_NarutoTimer)
        val panel = OverlaySettingsView(
            themed,
            session,
            settings,
            object : OverlaySettingsView.Callbacks {
                override fun onClose() = closeSettings()
                override fun onSaved() {
                    refreshSoon()
                    settingsView?.refresh()
                }
                override fun onEditPosition() {
                    closeSettings()
                    startPositionEdit()
                }
                override fun onResetPosition() = onPositionSettingChanged()
                override fun onRequestProjection() {
                    // The consent dialog would open underneath this panel.
                    closeSettings()
                    runCatching { ProjectionPermissionActivity.launch(service) }
                        .onFailure { Log.e(TAG, "projection permission failed", it) }
                }
                override fun onStopProjection() {
                    runCatching { ProjectionCaptureService.stop(service) }
                        .onFailure { Log.e(TAG, "projection stop failed", it) }
                    main.postDelayed({ updateSettingsStatus() }, 300)
                }
            },
        )
        panel.isClickable = true // Taps inside the panel never reach the scrim.
        panel.onConfigChanged = { main.post { updateSettingsLayout() } }
        root.addView(
            panel,
            FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
                Gravity.CENTER,
            ),
        )
        settingsRoot = root
        settingsView = panel
        settingsVisible = true
        updateSettingsLayout()
        try {
            wm.addView(root, settingsParams)
            root.requestFocus()
            updateSettingsStatus()
        } catch (e: RuntimeException) {
            Log.e(TAG, "open settings failed", e)
            settingsRoot = null
            settingsView = null
            settingsVisible = false
        }
    }

    private fun closeSettings() {
        if (!settingsVisible && settingsRoot == null) return
        val imm = service.getSystemService(Context.INPUT_METHOD_SERVICE) as? InputMethodManager
        settingsRoot?.let { root ->
            imm?.hideSoftInputFromWindow(root.windowToken, 0)
            runCatching { wm.removeViewImmediate(root) }
        }
        settingsView?.callbacks = object : OverlaySettingsView.Callbacks {}
        settingsView?.onConfigChanged = null
        settingsView = null
        settingsRoot = null
        settingsVisible = false
        forceRender = true
        if (showing) main.post { tickNow() }
    }

    private fun settingsOverlayParams(): WindowManager.LayoutParams =
        WindowManager.LayoutParams(
            WindowManager.LayoutParams.MATCH_PARENT,
            WindowManager.LayoutParams.MATCH_PARENT,
            WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
            WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN or
                WindowManager.LayoutParams.FLAG_DRAWS_SYSTEM_BAR_BACKGROUNDS,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.TOP or Gravity.LEFT
            softInputMode = WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE or
                WindowManager.LayoutParams.SOFT_INPUT_STATE_UNCHANGED
            // Coordinates come from safeRect(), which already excludes cutouts.
            layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS
            setFitInsetsTypes(0)
            title = "NarutoTimerSettings"
        }

    private fun updateSettingsLayout() {
        if (!settingsVisible || settingsRoot == null) return
        val b = safeRect()
        settingsParams.x = b.left
        settingsParams.y = b.top
        settingsParams.width = b.width().coerceAtLeast(1)
        settingsParams.height = b.height().coerceAtLeast(1)
        settingsView?.setMaxSize(b.width(), b.height())
        settingsRoot?.let { root ->
            if (root.isAttachedToWindow) runCatching { wm.updateViewLayout(root, settingsParams) }
        }
        updateSettingsStatus()
    }

    private fun updateSettingsStatus() {
        lastStatusAt = SystemClock.uptimeMillis()
        settingsView?.setStatus(
            "悬浮窗：运行中\n" +
                "识别：${if (session.fighting) "对局中" else "等待对局画面"}\n" +
                "录屏采集：${if (ProjectionCaptureService.running) "运行中（约 60 帧/秒）" else "未运行（无障碍截屏，约 3 帧/秒）"}",
        )
    }

    private val viewListener = object : MiniTimerView.Listener {
        override fun onSettings() = openSettings()

        override fun onSwapSide() {
            session.swapSide()
            refreshSoon()
            touchActivity()
        }

        override fun onToggleBothSides() {
            toggleBothSides()
            touchActivity()
        }
        override fun onToggleMini() = toggleMini()
        override fun onExit() {
            TimerAccessibilityService.instance?.stopOverlay()
        }
        override fun onDrag(dx: Int, dy: Int) = moveBy(dx, dy)
        override fun onDragEnd() = savePosition()
        override fun onDismissControls() {
            if (positionEditing) {
                setEditing(false)
                return
            }
            setControls(!controlsVisible)
        }
        override fun onOutsideTouch() {
            if (controlsVisible) setControls(false)
            if (positionEditing) setEditing(false)
        }
        override fun onTouchActivity() = touchActivity()
    }

    // ---- Safe placement ---------------------------------------------------

    /**
     * Returns the display area which is safe for the card. It accounts for
     * status / navigation bars, gesture exclusion edges and display cutouts.
     */
    private fun safeRect(): Rect {
        val fallback = Rect(
            0,
            0,
            service.resources.displayMetrics.widthPixels,
            service.resources.displayMetrics.heightPixels,
        )
        return runCatching {
            val metrics = wm.currentWindowMetrics
            val bounds = Rect(metrics.bounds)
            val insets = metrics.windowInsets.getInsetsIgnoringVisibility(
                WindowInsets.Type.systemBars() or
                    WindowInsets.Type.displayCutout() or
                    WindowInsets.Type.mandatorySystemGestures(),
            )
            bounds.left += insets.left
            bounds.top += insets.top
            bounds.right -= insets.right
            bounds.bottom -= insets.bottom
            if (bounds.width() > 0 && bounds.height() > 0) bounds else fallback
        }.getOrElse { fallback }
    }

    private fun viewW(): Int = view?.width?.takeIf { it > 0 } ?: view?.measuredWidth ?: 0
    private fun viewH(): Int = view?.height?.takeIf { it > 0 } ?: view?.measuredHeight ?: 0

    private fun overlayParams(): WindowManager.LayoutParams {
        // The card receives taps (its small bounds only). FLAG_WATCH_OUTSIDE_TOUCH
        // reports game touches elsewhere, so an open action row closes itself.
        val flags = WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or
            WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL or
            WindowManager.LayoutParams.FLAG_WATCH_OUTSIDE_TOUCH or
            WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN
        return WindowManager.LayoutParams(
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
            flags,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.TOP or Gravity.LEFT
            // We clamp against the cutout-aware safe rectangle ourselves, so
            // x/y must be display coordinates, not cutout-shifted ones.
            layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS
            setFitInsetsTypes(0)
            title = "NarutoTimerOverlay"
        }
    }

    /**
     * Places the card from the saved coordinates (or the default spot) and
     * clamps only the live copy. The saved values survive rotation, so a
     * landscape position is restored after a portrait detour.
     */
    private fun placeFromSettings(b: Rect, w: Int, h: Int) {
        val sx = settings.overlayX
        val sy = settings.overlayY
        if (sx == Int.MIN_VALUE || sy == Int.MIN_VALUE) {
            // Upper middle of the arena: below the name/bean HUD, away from skills.
            params.x = b.left + ((b.width() - w) / 2f).roundToInt()
            params.y = b.top + (b.height() * DEFAULT_Y_FRACTION).roundToInt()
            rightDocked = false
        } else {
            params.x = sx
            params.y = sy
            rightDocked = isNearRight(b, sx, w)
        }
        clamp(b, w, h)
    }

    private fun clamp(b: Rect, w: Int, h: Int) {
        params.x = params.x.coerceIn(b.left, max(b.left, b.right - w))
        params.y = params.y.coerceIn(b.top, max(b.top, b.bottom - h))
    }

    private fun isNearRight(b: Rect, x: Int, w: Int): Boolean =
        w > 0 && b.right - (x + w) <= (RIGHT_SNAP_DP * service.resources.displayMetrics.density).roundToInt()

    private fun moveBy(dx: Int, dy: Int) {
        val b = safeRect()
        params.x += dx
        params.y += dy
        clamp(b, viewW(), viewH())
        updateLayout()
    }

    /** Keep the exact user-selected coordinates; only clamp to the safe display. */
    private fun savePosition() {
        val b = safeRect()
        val w = viewW()
        clamp(b, w, viewH())
        rightDocked = isNearRight(b, params.x, w)
        updateLayout()
        settings.overlayX = params.x
        settings.overlayY = params.y
    }

    private fun onPositionSettingChanged() {
        if (settings.overlayX == params.x && settings.overlayY == params.y) return
        placeFromSettings(safeRect(), viewW(), viewH())
        updateLayout()
    }

    private fun onViewResized() {
        if (!showing) return
        val b = safeRect()
        val w = viewW()
        // A right-docked card grows to the left when its action row opens.
        if (rightDocked) params.x = b.right - w
        clamp(b, w, viewH())
        updateLayout()
    }

    private fun reclamp() {
        if (!showing) return
        val b = safeRect()
        val w = viewW()
        val h = viewH()
        placeFromSettings(b, w, h)
        if (rightDocked) params.x = b.right - w
        clamp(b, w, h)
        updateLayout()
        updateSettingsLayout()
    }

    private fun updateLayout() {
        if (!showing) return
        view?.let { v ->
            if (v.isAttachedToWindow) runCatching { wm.updateViewLayout(v, params) }
        }
    }

    companion object {
        private const val TAG = "OverlayController"
        private const val REVISION_POLL_NANOS = 33_000_000L
        private const val IDLE_TICK_NANOS = 200_000_000L
        private const val MIN_TICK_NANOS = 1_000_000L
        private const val CONTROLS_AUTO_HIDE_MS = 5_000L
        private const val EDIT_AUTO_END_MS = 8_000L
        private const val STATUS_REFRESH_MS = 1_000L
        private const val DEFAULT_Y_FRACTION = 0.28
        private const val RIGHT_SNAP_DP = 32f
        private const val SCRIM = 0x99000000.toInt()
    }
}
