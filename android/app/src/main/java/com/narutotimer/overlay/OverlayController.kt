package com.narutotimer.overlay

// Owner: kt-overlay-ui
// Android-native floating timer controller.  The card is deliberately passive
// until the handle opens its compact action row, so the game remains touchable.

import android.accessibilityservice.AccessibilityService
import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.PixelFormat
import android.graphics.Rect
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.view.Gravity
import android.view.HapticFeedbackConstants
import android.view.inputmethod.InputMethodManager
import android.view.MotionEvent
import android.view.View
import android.view.ViewConfiguration
import android.view.WindowInsets
import android.view.WindowManager
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import android.widget.FrameLayout
import com.narutotimer.Settings
import com.narutotimer.capture.ProjectionCaptureService
import com.narutotimer.tracker.OverlayState
import com.narutotimer.tracker.Session
import com.narutotimer.tracker.TimerColors
import com.narutotimer.service.TimerAccessibilityService
import com.narutotimer.ui.ProjectionPermissionActivity
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/** Owns the two TYPE_ACCESSIBILITY_OVERLAY windows used by the Android timer. */
class OverlayController(
    private val service: AccessibilityService,
    private val session: Session,
    private val settings: Settings,
) {
    private val wm: WindowManager = service.getSystemService(WindowManager::class.java)
    private val main = Handler(Looper.getMainLooper())
    private val density = service.resources.displayMetrics.density

    private var view: MiniTimerView? = null
    private var handle: HandleView? = null
    private var settingsView: OverlaySettingsView? = null
    private var settingsRoot: FrameLayout? = null
    private val params = overlayParams(touchable = false)
    private val handleParams = overlayParams(touchable = true)
    private val settingsParams = settingsOverlayParams()

    private var showing = false
    private var settingsVisible = false
    private var destroyed = false
    private var controlsVisible = false
    private var positionEditing = false
    private var rightDocked = true
    private var handleOnLeft = true

    // Clock refresh is kept on the main thread.  Revision polling avoids asking
    // Session for state on every 0.1-second repaint when the scene is idle.
    private var lastSessionRevision = Long.MIN_VALUE
    private var clockDeadline = 0L
    private var forceRender = true
    private var lastState: OverlayState? = null
    private var appliedOpacity = -1f
    private var lastErrorLogAt = 0L

    private val tick = Runnable { tickNow() }
    private val autoHide = Runnable { setControls(false) }
    private val settingsListener: (String) -> Unit = { key ->
        // Settings can be changed from MainActivity's thread.  WindowManager is
        // always driven from the main looper.
        main.post { onSettingChanged(key) }
    }

    // ---- Public API -------------------------------------------------------

    /** Adds both overlay windows and starts the refresh loop.  Main-thread API. */
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
            handleParams.alpha = state.opacity
            appliedOpacity = state.opacity
        }
        v.measure(View.MeasureSpec.UNSPECIFIED, View.MeasureSpec.UNSPECIFIED)
        initialPosition(v.measuredWidth, v.measuredHeight)

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

    /** Removes both overlay windows and stops refresh.  Main-thread API. */
    fun hide() {
        if (!showing && !settingsVisible) return
        closeSettings()
        showing = false
        main.removeCallbacks(tick)
        main.removeCallbacks(autoHide)
        settings.removeListener(settingsListener)
        controlsVisible = false
        positionEditing = false
        setTouchable(false)
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

    /**
     * Temporarily makes the display pill draggable from the settings screen.
     * There is deliberately no permanent handle window: display and settings
     * stay separate, and the game is never covered by a control tab.
     */
    fun startPositionEdit() {
        if (!showing) return
        controlsVisible = false
        positionEditing = true
        setTouchable(true)
        view?.controlsVisible = false
        refreshSoon()
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
        handleParams.alpha = opacity
        updateLayouts()
    }

    // ---- Controls ---------------------------------------------------------

    private fun setControls(on: Boolean) {
        main.removeCallbacks(autoHide)
        if (on) main.postDelayed(autoHide, CONTROLS_AUTO_HIDE_MS)
        if (controlsVisible == on) return
        controlsVisible = on
        view?.controlsVisible = on
        handle?.active = on
        setTouchable(on)
        updateLayouts()
        view?.sendAccessibilityEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED)
    }

    /**
     * The compact status pill is always touchable so tapping it opens the
     * action row. Its small bounds are the only game area it intercepts;
     * everything else remains touch-through. The handle is independently
     * touchable for drag/open access.
     */
    private fun setTouchable(on: Boolean) {
        params.flags = params.flags and WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE.inv()
    }

    private fun touchActivity() {
        if (controlsVisible) {
            main.removeCallbacks(autoHide)
            main.postDelayed(autoHide, CONTROLS_AUTO_HIDE_MS)
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
        if (settingsVisible) {
            settingsView?.refresh()
            settingsView?.requestFocus()
            updateSettingsLayout()
            return
        }

        val root = FrameLayout(service).apply {
            setBackgroundColor(0x66000000)
            isFocusable = true
            isFocusableInTouchMode = true
            setOnKeyListener { _, keyCode, event ->
                if (keyCode == android.view.KeyEvent.KEYCODE_BACK &&
                    event.action == android.view.KeyEvent.ACTION_UP
                ) {
                    closeSettings()
                    true
                } else false
            }
        }
        val panel = OverlaySettingsView(
            service,
            session,
            settings,
            object : OverlaySettingsView.Callbacks {
                override fun onClose() = closeSettings()
                override fun onSaved() {
                    refreshSoon()
                    closeSettings()
                }
                override fun onEditPosition() {
                    closeSettings()
                    startPositionEdit()
                }
                override fun onResetPosition() = onPositionSettingChanged()
                override fun onRequestProjection() {
                    runCatching { ProjectionPermissionActivity.launch(service) }
                        .onFailure { Log.e(TAG, "projection permission failed", it) }
                    main.postDelayed({ updateSettingsStatus() }, 500)
                }
                override fun onStopProjection() {
                    runCatching { ProjectionCaptureService.stop(service) }
                        .onFailure { Log.e(TAG, "projection stop failed", it) }
                    updateSettingsStatus()
                }
            },
        )
        panel.onConfigChanged = { main.post { updateSettingsLayout() } }
        root.addView(
            panel,
            FrameLayout.LayoutParams(
                WindowManager.LayoutParams.MATCH_PARENT,
                WindowManager.LayoutParams.WRAP_CONTENT,
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
            layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_NEVER
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
        settingsRoot?.let { root ->
            if (root.isAttachedToWindow) runCatching { wm.updateViewLayout(root, settingsParams) }
        }
        updateSettingsStatus()
    }

    private fun updateSettingsStatus() {
        settingsView?.setStatus(
            "悬浮窗运行中\n" +
                "识别状态：${if (session.fighting) "对局中" else "等待画面"}\n" +
                "录屏采集：${if (ProjectionCaptureService.running) "运行中" else "未运行（可用无障碍截屏）"}",
        )
    }

    private val viewListener = object : MiniTimerView.Listener {
        override fun onSettings() = openSettings()

        override fun onSwapSide() {
            session.swapSide()
            refreshSoon()
        }

        override fun onToggleBothSides() = toggleBothSides()
        override fun onToggleMini() = toggleMini()
        override fun onExit() {
            TimerAccessibilityService.instance?.stopOverlay()
        }
        override fun onDrag(dx: Int, dy: Int) = moveBy(dx, dy)
        override fun onDragEnd() {
            savePosition()
            positionEditing = false
            setTouchable(true)
        }
        override fun onDismissControls() {
            if (positionEditing) return
            // Tap the status pill to expand/collapse its compact action row.
            setControls(!controlsVisible)
        }
        override fun onTouchActivity() = touchActivity()
    }

    // ---- Safe placement ---------------------------------------------------

    /**
     * Returns the display area which is safe for the card and its hit target.
     * Unlike the old maximumWindowMetrics approach this accounts for status /
     * navigation bars, gesture exclusion edges and display cutouts.
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

    private fun px(dp: Float): Int = (dp * density).roundToInt()
    private fun viewW(): Int = view?.width?.takeIf { it > 0 } ?: view?.measuredWidth ?: 0
    private fun viewH(): Int = view?.height?.takeIf { it > 0 } ?: view?.measuredHeight ?: 0

    private fun overlayParams(touchable: Boolean): WindowManager.LayoutParams {
        // The card itself is a small status pill and must receive taps to open
        // the action row. Everything outside its bounds remains game touch-through.
        var flags = WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or
            WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL or
            WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN
        return WindowManager.LayoutParams(
            if (touchable) px(HANDLE_TOUCH_DP) else WindowManager.LayoutParams.WRAP_CONTENT,
            if (touchable) px(HANDLE_TOUCH_DP) else WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
            flags,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.TOP or Gravity.LEFT
            // We clamp against the cutout-aware safe rectangle ourselves.  Do
            // not ask WM to lay the card out under a cutout or outside bounds.
            layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_NEVER
            setFitInsetsTypes(0)
            title = if (touchable) "NarutoTimerHandle" else "NarutoTimerOverlay"
        }
    }

    private fun initialPosition(w: Int, h: Int) {
        val b = safeRect()
        val sx = settings.overlayX
        val sy = settings.overlayY
        if (sx == Int.MIN_VALUE || sy == Int.MIN_VALUE) {
            defaultPosition(b, w, h)
        } else {
            params.x = sx
            params.y = sy
            clamp(b, w, h)
            rightDocked = isNearRight(b, params.x, w)
        }
        placeHandle(b, w, h)
    }

    /**
     * Default to the open upper-middle arena, below the name/bean HUD and away
     * from the right-side skill controls. Users can still drag and persist it.
     */
    private fun defaultPosition(b: Rect, w: Int, h: Int) {
        params.x = b.left + ((b.width() - w) / 2f).roundToInt()
        params.y = b.top + (b.height() * DEFAULT_Y_FRACTION).roundToInt()
        rightDocked = false
        clamp(b, w, h)
    }

    private fun clamp(b: Rect, w: Int, h: Int) {
        params.x = params.x.coerceIn(b.left, max(b.left, b.right - w))
        params.y = params.y.coerceIn(b.top, max(b.top, b.bottom - h))
    }

    private fun isNearRight(b: Rect, x: Int, w: Int): Boolean =
        b.right - (x + w) <= px(RIGHT_SNAP_DP)

    /** Places a 48dp hit target adjacent to the card; only its pill is painted. */
    private fun placeHandle(b: Rect, w: Int, h: Int) {
        val hw = px(HANDLE_TOUCH_DP)
        val hh = px(HANDLE_TOUCH_DP)
        val leftFits = params.x - hw >= b.left
        handleOnLeft = leftFits
        handleParams.x = if (leftFits) params.x - hw else params.x + w
        handleParams.x = handleParams.x.coerceIn(b.left, max(b.left, b.right - hw))
        handleParams.y = params.y + ((h - hh) / 2f).roundToInt()
        handleParams.y = handleParams.y.coerceIn(b.top, max(b.top, b.bottom - hh))
    }

    private fun moveBy(dx: Int, dy: Int) {
        val b = safeRect()
        val w = viewW()
        rightDocked = false
        params.x += dx
        params.y += dy
        clamp(b, w, viewH())
        placeHandle(b, w, viewH())
        updateLayouts()
    }

    /** Keep the exact user-selected coordinates; only clamp to the safe display. */
    private fun savePosition() {
        val b = safeRect()
        val w = viewW()
        val h = viewH()
        clamp(b, w, h)
        rightDocked = isNearRight(b, params.x, w)
        placeHandle(b, w, h)
        updateLayouts()
        settings.overlayX = params.x
        settings.overlayY = params.y
    }

    private fun onPositionSettingChanged() {
        val sx = settings.overlayX
        val sy = settings.overlayY
        val b = safeRect()
        val w = viewW()
        val h = viewH()
        if (sx == Int.MIN_VALUE || sy == Int.MIN_VALUE) {
            defaultPosition(b, w, h)
        } else if (sx != params.x || sy != params.y) {
            params.x = sx
            params.y = sy
            clamp(b, w, h)
            rightDocked = isNearRight(b, params.x, w)
        } else {
            return
        }
        placeHandle(b, w, h)
        updateLayouts()
    }

    private fun onViewResized() {
        if (!showing) return
        val b = safeRect()
        val w = viewW()
        val h = viewH()
        if (rightDocked) params.x = b.right - w
        clamp(b, w, h)
        placeHandle(b, w, h)
        updateLayouts()
    }

    private fun reclamp() {
        if (!showing) return
        val b = safeRect()
        val w = viewW()
        val h = viewH()
        if (settings.overlayX == Int.MIN_VALUE || settings.overlayY == Int.MIN_VALUE) {
            defaultPosition(b, w, h)
        } else {
            if (rightDocked) params.x = b.right - w
            clamp(b, w, h)
        }
        placeHandle(b, w, h)
        updateLayouts()
        updateSettingsLayout()
    }

    private fun updateLayouts() {
        if (!showing) return
        view?.let { v ->
            if (v.isAttachedToWindow) runCatching { wm.updateViewLayout(v, params) }
        }
        handle?.let { h ->
            if (h.isAttachedToWindow) runCatching { wm.updateViewLayout(h, handleParams) }
        }
    }

    // ---- Handle -----------------------------------------------------------

    /** Accessible 48dp target: tap controls, long press mode, drag placement. */
    private inner class HandleView(context: Context) : View(context) {
        var active = false
            set(value) {
                if (field != value) {
                    field = value
                    contentDescription = if (value) "关闭计时器控制" else "打开计时器控制"
                    invalidate()
                }
            }

        private val fill = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }
        private val stroke = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            style = Paint.Style.STROKE
            strokeWidth = density
        }
        private val rect = android.graphics.RectF()
        private val touchSlop = ViewConfiguration.get(context).scaledTouchSlop
        private val longPressMs = ViewConfiguration.getLongPressTimeout().toLong()
        private var downX = 0f
        private var downY = 0f
        private var lastX = 0f
        private var lastY = 0f
        private var dragging = false
        private var longPressed = false
        private val longPress = Runnable {
            if (!dragging) {
                longPressed = true
                performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
                toggleMini()
            }
        }

        init {
            isFocusable = true
            isClickable = true
            isLongClickable = true
            importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_YES
            contentDescription = "打开计时器控制"
        }

        override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
            setMeasuredDimension(px(HANDLE_TOUCH_DP), px(HANDLE_TOUCH_DP))
        }

        override fun onDraw(canvas: Canvas) {
            val pillW = px(HANDLE_PILL_WIDTH_DP).toFloat()
            val pillH = px(HANDLE_PILL_HEIGHT_DP).toFloat()
            val left = if (handleOnLeft) width - pillW else 0f
            val top = (height - pillH) / 2f
            rect.set(left + density / 2f, top + density / 2f, left + pillW - density / 2f, top + pillH - density / 2f)
            fill.color = if (active) TimerColors.PANEL_HOVER else TimerColors.PANEL_BTN
            canvas.drawRoundRect(rect, pillW / 2f, pillW / 2f, fill)
            stroke.color = TimerColors.PANEL_BORDER
            canvas.drawRoundRect(rect, pillW / 2f, pillW / 2f, stroke)
            fill.color = TimerColors.TAG_IDLE
            val cx = rect.centerX()
            val lineW = pillW * 0.22f
            val lineH = density * 1.5f
            for (i in -1..1) {
                val cy = rect.centerY() + i * density * 5f
                canvas.drawRect(cx - lineW, cy - lineH / 2f, cx + lineW, cy + lineH / 2f, fill)
            }
        }

        override fun onTouchEvent(event: MotionEvent): Boolean {
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    downX = event.rawX
                    downY = event.rawY
                    lastX = downX
                    lastY = downY
                    dragging = false
                    longPressed = false
                    postDelayed(longPress, longPressMs)
                    touchActivity()
                }
                MotionEvent.ACTION_MOVE -> {
                    if (!dragging && !longPressed &&
                        (abs(event.rawX - downX) > touchSlop || abs(event.rawY - downY) > touchSlop)
                    ) {
                        dragging = true
                        removeCallbacks(longPress)
                    }
                    if (dragging) {
                        val dx = (event.rawX - lastX).roundToInt()
                        val dy = (event.rawY - lastY).roundToInt()
                        if (dx != 0 || dy != 0) {
                            lastX += dx
                            lastY += dy
                            moveBy(dx, dy)
                        }
                        touchActivity()
                    }
                }
                MotionEvent.ACTION_UP -> {
                    removeCallbacks(longPress)
                    when {
                        dragging -> savePosition()
                        !longPressed -> performClick()
                    }
                    dragging = false
                }
                MotionEvent.ACTION_CANCEL -> {
                    removeCallbacks(longPress)
                    if (dragging) savePosition()
                    dragging = false
                }
            }
            return true
        }

        override fun performClick(): Boolean {
            super.performClick()
            setControls(!controlsVisible)
            return true
        }

        override fun onInitializeAccessibilityNodeInfo(info: AccessibilityNodeInfo) {
            super.onInitializeAccessibilityNodeInfo(info)
            info.className = "android.widget.ImageButton"
            info.isClickable = true
            info.isLongClickable = true
            info.contentDescription = contentDescription
            info.addAction(AccessibilityNodeInfo.AccessibilityAction(
                AccessibilityNodeInfo.ACTION_CLICK,
                if (active) "关闭计时器控制" else "打开计时器控制",
            ))
            info.addAction(AccessibilityNodeInfo.AccessibilityAction(
                AccessibilityNodeInfo.ACTION_LONG_CLICK,
                "切换迷你模式",
            ))
        }

        override fun performAccessibilityAction(action: Int, arguments: android.os.Bundle?): Boolean {
            return when (action) {
                AccessibilityNodeInfo.ACTION_CLICK -> {
                    performClick()
                    true
                }
                AccessibilityNodeInfo.ACTION_LONG_CLICK -> {
                    toggleMini()
                    true
                }
                else -> super.performAccessibilityAction(action, arguments)
            }
        }
    }

    companion object {
        private const val TAG = "OverlayController"
        private const val REVISION_POLL_NANOS = 33_000_000L
        private const val IDLE_TICK_NANOS = 200_000_000L
        private const val MIN_TICK_NANOS = 1_000_000L
        private const val CONTROLS_AUTO_HIDE_MS = 5_000L
        private const val DEFAULT_Y_FRACTION = 0.28
        private const val EDGE_MARGIN_DP = 8f
        private const val RIGHT_SNAP_DP = 32f
        private const val HANDLE_TOUCH_DP = 48f
        private const val HANDLE_PILL_WIDTH_DP = 18f
        private const val HANDLE_PILL_HEIGHT_DP = 40f
    }
}
