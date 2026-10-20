package com.narutotimer.overlay

// Owner: kt-overlay-ui
// Canvas-only timer card for the game overlay. The controller supplies an
// immutable OverlayState; this view only measures, paints and reports touches.
//
//  完整  ┌───────────────────────────┐   迷你  ┌──────────┐
//        │ 对面·右 · 斑      第 1 次 │         │ 我方·自动·左│
//        │          12.3             │         │   12.3    │
//        │ 我方·自动·左 · 决·对局 豆2/3│         └──────────┘
//        └───────────────────────────┘
// Tapping the card opens an action row below it; dragging moves the card only
// while that row is open or the controller's position-edit mode is active.

import android.content.Context
import android.content.res.Configuration
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Path
import android.graphics.PorterDuff
import android.graphics.RectF
import android.graphics.Typeface
import android.text.TextPaint
import android.text.TextUtils
import android.view.MotionEvent
import android.view.View
import android.view.ViewConfiguration
import android.view.accessibility.AccessibilityNodeInfo
import com.narutotimer.tracker.OverlayState
import com.narutotimer.tracker.TimerColors
import kotlin.math.abs
import kotlin.math.ceil
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/** Native Canvas renderer for the full and mini overlay cards. */
class MiniTimerView(context: Context) : View(context) {
    interface Listener {
        fun onSettings()
        fun onSwapSide()
        fun onToggleBothSides()
        fun onToggleMini()
        /** Explicitly stop and hide the game overlay. */
        fun onExit()
        /** Dragging delta in screen pixels (only while [draggable]). */
        fun onDrag(dx: Int, dy: Int)
        /** Dragging ended; controller persists the position. */
        fun onDragEnd()
        /** A plain tap on the card (not on an action button). */
        fun onDismissControls()
        /** A touch anywhere outside this window. */
        fun onOutsideTouch() {}
        /** Any interaction while the action row is open. */
        fun onTouchActivity()
    }

    var listener: Listener? = null

    /** Called when display configuration changes so the controller can reclamp. */
    var onConfigChanged: (() -> Unit)? = null

    /** Whether the action row is painted and hit-testable. */
    var controlsVisible: Boolean = false
        set(value) {
            if (field == value) return
            field = value
            pressed = -1
            updateAccessibilityDescription()
            relayout()
        }

    /** Drags move the card only when the controller allows it. */
    var draggable: Boolean = false

    /** Position-edit mode: highlighted border and a drag hint instead of the caption. */
    var editing: Boolean = false
        set(value) {
            if (field == value) return
            field = value
            relayout()
        }

    /** Last immutable state supplied by Session. */
    var state: OverlayState? = null
        private set

    private val density = resources.displayMetrics.density
    private val paint = TextPaint(Paint.ANTI_ALIAS_FLAG or Paint.SUBPIXEL_TEXT_FLAG).apply {
        // Tabular digits: 12.3 → 11.9 never shifts the clock sideways.
        fontFeatureSettings = "tnum"
    }
    private val fill = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }
    private val stroke = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE }
    private val fm = Paint.FontMetrics()
    private val cardRect = RectF()
    private val clip = Path()
    private val regular = Typeface.create(Typeface.DEFAULT, 400, false)
    private val medium = Typeface.create(Typeface.DEFAULT, 500, false)
    private val bold = Typeface.create(Typeface.DEFAULT, 700, false)

    // Layout cache. Width only grows within one layout structure (mode, both
    // sides, dual, controls, scale) so changing digits never resizes the window.
    private var layoutW = 0f
    private var layoutH = 0f
    private var grownW = 0f
    private var structureKey = Long.MIN_VALUE
    private var bodyH = 0f
    private var controlsH = 0f
    private var controlsMinW = 0f

    private val buttonHitRects = Array(MAX_BUTTONS) { RectF() }
    private val buttonVisualRects = Array(MAX_BUTTONS) { RectF() }
    private val buttonActions = IntArray(MAX_BUTTONS)
    private val buttonLabels = arrayOfNulls<String>(MAX_BUTTONS)
    private var buttonCount = 0
    private var pressed = -1

    private val touchSlop = ViewConfiguration.get(context).scaledTouchSlop
    private var downRawX = 0f
    private var downRawY = 0f
    private var lastRawX = 0f
    private var lastRawY = 0f
    private var dragging = false
    private var movedAway = false

    init {
        importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_YES
        isFocusable = true
        updateAccessibilityDescription()
    }

    // ---- State ------------------------------------------------------------

    /** Repaints only when a visible field changed. */
    fun render(s: OverlayState) {
        val previous = state
        state = s
        if (previous != null && sameVisible(previous, s)) {
            updateAccessibilityDescription()
            return
        }
        relayout(force = !isLaidOut)
        updateAccessibilityDescription()
    }

    private fun relayout(force: Boolean = false) {
        val s = state ?: return
        val key = structure(s)
        if (key != structureKey) {
            structureKey = key
            grownW = 0f
        }
        val oldW = layoutW
        val oldH = layoutH
        computeLayout()
        if (force || ceil(layoutW) != ceil(oldW) || ceil(layoutH) != ceil(oldH)) requestLayout()
        invalidate()
    }

    private fun sameVisible(a: OverlayState, b: OverlayState): Boolean =
        a.tagText == b.tagText && a.tagColor == b.tagColor && a.sideModeText == b.sideModeText &&
            a.primaryText == b.primaryText && a.primaryColor == b.primaryColor &&
            a.eventText == b.eventText && a.dual == b.dual &&
            a.altText == b.altText && a.altColor == b.altColor &&
            a.leftText == b.leftText && a.leftColor == b.leftColor &&
            a.rightText == b.rightText && a.rightColor == b.rightColor &&
            a.infoText == b.infoText && a.showBothSides == b.showBothSides &&
            a.mini == b.mini && a.fontScale == b.fontScale

    private fun structure(s: OverlayState): Long {
        var key = java.lang.Float.floatToIntBits(s.fontScale).toLong() shl 8
        if (s.mini) key = key or 1
        if (s.showBothSides) key = key or 2
        if (s.dual) key = key or 4
        if (controlsVisible) key = key or 8
        if (editing) key = key or 16
        return key
    }

    // ---- Metrics ----------------------------------------------------------

    private fun scale(): Float = normalizedOverlayScale((state?.fontScale ?: 1f).toDouble()).toFloat()

    /** Layout units follow the overlay size setting. */
    private fun u(valueDp: Float): Float = valueDp * density * scale()

    private fun setText(sizeSp: Float, face: Typeface, color: Int = 0) {
        // System font size (sp) and the overlay size setting both apply.
        paint.textSize = sizeSp * scale() * resources.displayMetrics.scaledDensity
        paint.typeface = face
        paint.color = color
    }

    private fun textW(text: String, sizeSp: Float, face: Typeface): Float {
        setText(sizeSp, face)
        return paint.measureText(text)
    }

    private fun lineH(sizeSp: Float, face: Typeface): Float {
        setText(sizeSp, face)
        paint.getFontMetrics(fm)
        return fm.descent - fm.ascent
    }

    /** Clock width never shrinks below the widest tenths value. */
    private fun clockW(text: String, sizeSp: Float): Float =
        max(textW(text, sizeSp, bold), textW(CLOCK_TEMPLATE, sizeSp, bold))

    private fun isClock(text: String): Boolean = text.isNotEmpty() && text.all { it.isDigit() || it == '.' }

    private fun drawText(
        canvas: Canvas,
        text: String,
        sizeSp: Float,
        face: Typeface,
        color: Int,
        x: Float,
        top: Float,
        align: Paint.Align,
        maxWidth: Float = Float.MAX_VALUE,
    ) {
        setText(sizeSp, face, color)
        val shown = if (paint.measureText(text) > maxWidth) {
            TextUtils.ellipsize(text, paint, maxWidth.coerceAtLeast(0f), TextUtils.TruncateAt.END).toString()
        } else text
        paint.getFontMetrics(fm)
        val w = paint.measureText(shown)
        val left = when (align) {
            Paint.Align.LEFT -> x
            Paint.Align.RIGHT -> x - w
            else -> x - w / 2f
        }
        canvas.drawText(shown, left, top - fm.ascent, paint)
    }

    // ---- Blocks -----------------------------------------------------------

    private fun caption(s: OverlayState): String = when {
        editing -> "拖动移动 · 点击完成"
        else -> s.sideModeText.ifEmpty { "我方·自动·待认边" }
    }

    private fun clockSize(s: OverlayState) = if (s.mini) MINI_CLOCK else FULL_CLOCK
    private fun columnClockSize(s: OverlayState) = if (s.mini) MINI_COLUMN_CLOCK else FULL_COLUMN_CLOCK
    private fun columnLabelSize(s: OverlayState) = if (s.mini) MINI_COLUMN_LABEL else FULL_COLUMN_LABEL

    /** Columns shown in the clock row: both sides or the Mizukage 15 s/10 s pair. */
    private fun columns(s: OverlayState): List<Triple<String, String, Int>>? = when {
        s.showBothSides -> listOf(Triple("左", s.leftText, s.leftColor), Triple("右", s.rightText, s.rightColor))
        s.dual -> listOf(Triple("15秒", s.primaryText, s.primaryColor), Triple("10秒", s.altText.ifEmpty { "—" }, s.altColor))
        else -> null
    }

    private fun columnW(s: OverlayState, label: String, value: String): Float {
        val valueW = if (isClock(value) || value == "—") clockW(value, columnClockSize(s))
        else textW(value, columnClockSize(s) * STATUS_RATIO, bold)
        return max(textW(label, columnLabelSize(s), medium), valueW)
    }

    private fun clockRowW(s: OverlayState): Float {
        val cols = columns(s)
        if (cols != null) {
            return cols.sumOf { columnW(s, it.first, it.second).toDouble() }.toFloat() + u(COLUMN_GAP_DP) * (cols.size - 1)
        }
        val size = clockSize(s)
        return max(clockW(s.primaryText, size), textW(s.primaryText, size * STATUS_RATIO, bold))
    }

    private fun clockRowH(s: OverlayState): Float {
        if (columns(s) != null) {
            return lineH(columnLabelSize(s), medium) + lineH(columnClockSize(s), bold) * CLOCK_LINE
        }
        return lineH(clockSize(s), bold) * CLOCK_LINE
    }

    private fun headerSize(s: OverlayState) = if (s.mini) MINI_CAPTION else FULL_TAG

    // ---- Layout -----------------------------------------------------------

    private fun prepareButtons(s: OverlayState) {
        val labels = listOf(
            LABEL_SETTINGS to ACTION_SETTINGS,
            LABEL_SWAP to ACTION_SWAP,
            (if (s.showBothSides) LABEL_SINGLE else LABEL_BOTH) to ACTION_BOTH,
            (if (s.mini) LABEL_FULL else LABEL_MINI) to ACTION_MINI,
            LABEL_EXIT to ACTION_EXIT,
        )
        buttonCount = labels.size
        labels.forEachIndexed { i, (label, action) ->
            buttonLabels[i] = label
            buttonActions[i] = action
        }
    }

    private fun computeLayout() {
        val s = state ?: return
        prepareButtons(s)
        val padX = u(PAD_X_DP) + u(ACCENT_DP)
        val padY = u(PAD_Y_DP)
        val gap = u(GAP_DP)
        val maxW = resources.displayMetrics.widthPixels * 0.94f

        // Header: mini shows the player-side caption; full shows the opponent
        // tag on the left and the event count on the right.
        val headerH = lineH(headerSize(s), medium)
        val headerW = if (s.mini) {
            textW(caption(s), MINI_CAPTION, medium)
        } else {
            textW(if (editing) caption(s) else s.tagText, FULL_TAG, medium) + u(COLUMN_GAP_DP) +
                textW(s.eventText, FULL_EVENT, bold)
        }
        var contentW = max(headerW, clockRowW(s))
        var contentH = headerH + gap + clockRowH(s)
        if (!s.mini) {
            val info = infoText(s)
            contentW = max(contentW, textW(info, FULL_INFO, regular))
            contentH += gap + lineH(FULL_INFO, regular)
        }
        bodyH = contentH + 2 * padY

        controlsH = if (controlsVisible) u(BUTTON_HIT_DP) + 2 * u(CONTROLS_PAD_DP) else 0f
        controlsMinW = if (controlsVisible) {
            (0 until buttonCount).fold(0f) { total, i ->
                total + max(u(MIN_BUTTON_DP), textW(buttonLabels[i]!!, BUTTON_TEXT, medium) + 2 * u(BUTTON_PAD_DP))
            } + u(BUTTON_GAP_DP) * (buttonCount - 1) + 2 * u(CONTROLS_PAD_DP)
        } else 0f

        val desired = min(maxW, max(contentW + 2 * padX, controlsMinW))
        grownW = min(maxW, max(grownW, desired))
        layoutW = grownW
        layoutH = bodyH + controlsH
    }

    private fun infoText(s: OverlayState): String =
        if (s.infoText.isEmpty()) s.sideModeText else "${s.sideModeText} · ${s.infoText}"

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        if (state == null) {
            setMeasuredDimension(0, 0)
            return
        }
        computeLayout()
        setMeasuredDimension(
            resolveSize(ceil(layoutW).toInt(), widthMeasureSpec),
            resolveSize(ceil(layoutH).toInt(), heightMeasureSpec),
        )
    }

    // ---- Canvas -----------------------------------------------------------

    private fun accentColor(s: OverlayState): Int = when {
        editing -> TimerColors.CLOCK_LIVE
        s.showBothSides -> listOf(s.leftText to s.leftColor, s.rightText to s.rightColor)
            .firstOrNull { isClock(it.first) && it.second == TimerColors.CLOCK_WARN }?.second
            ?: if (isClock(s.leftText) || isClock(s.rightText)) TimerColors.CLOCK_LIVE else TimerColors.PANEL_BORDER
        isClock(s.primaryText) -> s.primaryColor
        else -> TimerColors.PANEL_BORDER
    }

    override fun onDraw(canvas: Canvas) {
        val s = state ?: return
        val w = width.toFloat()
        val radius = u(RADIUS_DP)
        canvas.drawColor(android.graphics.Color.TRANSPARENT, PorterDuff.Mode.CLEAR)

        // Card body. The window alpha supplies the user-selected opacity.
        cardRect.set(0f, 0f, w, bodyH)
        fill.color = TimerColors.PANEL_BG
        canvas.drawRoundRect(cardRect, radius, radius, fill)
        canvas.save()
        clip.reset()
        clip.addRoundRect(cardRect, radius, radius, Path.Direction.CW)
        canvas.clipPath(clip)
        // State stripe: cyan while the opponent's substitute cools down, amber in
        // the last three seconds, neutral otherwise.
        fill.color = accentColor(s)
        canvas.drawRect(0f, 0f, u(ACCENT_DP), bodyH, fill)
        drawBody(canvas, s, w)
        canvas.restore()
        stroke.strokeWidth = if (editing) u(1.5f) else max(1f, density * 0.75f)
        stroke.color = if (editing) TimerColors.CLOCK_LIVE else TimerColors.PANEL_BORDER
        val inset = stroke.strokeWidth / 2f
        cardRect.set(inset, inset, w - inset, bodyH - inset)
        canvas.drawRoundRect(cardRect, radius, radius, stroke)

        if (controlsVisible) drawControls(canvas, w) else clearButtonRects()
    }

    private fun drawBody(canvas: Canvas, s: OverlayState, w: Float) {
        val padX = u(PAD_X_DP) + u(ACCENT_DP)
        val gap = u(GAP_DP)
        val innerW = w - padX - u(PAD_X_DP)
        val cx = u(ACCENT_DP) + (w - u(ACCENT_DP)) / 2f
        var y = u(PAD_Y_DP)

        if (s.mini) {
            drawText(canvas, caption(s), MINI_CAPTION, medium, TimerColors.TAG_IDLE, cx, y, Paint.Align.CENTER, innerW)
            y += lineH(MINI_CAPTION, medium) + gap
        } else {
            val eventW = textW(s.eventText, FULL_EVENT, bold)
            val tagMax = innerW - eventW - u(COLUMN_GAP_DP)
            val tag = if (editing) caption(s) else s.tagText
            val tagColor = if (editing) TimerColors.CLOCK_LIVE else s.tagColor
            drawText(canvas, tag, FULL_TAG, medium, tagColor, padX, y, Paint.Align.LEFT, tagMax)
            val headerH = lineH(FULL_TAG, medium)
            drawText(
                canvas, s.eventText, FULL_EVENT, bold, TimerColors.TAG_IDLE, w - u(PAD_X_DP),
                y + (headerH - lineH(FULL_EVENT, bold)) / 2f, Paint.Align.RIGHT,
            )
            y += headerH + gap
        }

        drawClockRow(canvas, s, cx, y)
        y += clockRowH(s)

        if (!s.mini) {
            y += gap
            drawText(canvas, infoText(s), FULL_INFO, regular, TimerColors.TAG_IDLE, cx, y, Paint.Align.CENTER, innerW)
        }
    }

    private fun drawValue(canvas: Canvas, value: String, sizeSp: Float, color: Int, cx: Float, top: Float, rowH: Float) {
        if (isClock(value) || value == "—") {
            val h = lineH(sizeSp, bold)
            drawText(canvas, value, sizeSp, bold, if (isClock(value)) color else TimerColors.CLOCK_IDLE, cx, top + (rowH - h) / 2f, Paint.Align.CENTER)
        } else {
            // Waiting states (待替身 / 待识别 …) are text, not a countdown.
            val size = sizeSp * STATUS_RATIO
            val h = lineH(size, bold)
            drawText(canvas, value, size, bold, TimerColors.CLOCK_IDLE, cx, top + (rowH - h) / 2f, Paint.Align.CENTER)
        }
    }

    private fun drawClockRow(canvas: Canvas, s: OverlayState, cx: Float, top: Float) {
        val cols = columns(s)
        if (cols == null) {
            drawValue(canvas, s.primaryText, clockSize(s), s.primaryColor, cx, top, clockRowH(s))
            return
        }
        val labelSize = columnLabelSize(s)
        val labelH = lineH(labelSize, medium)
        val valueH = lineH(columnClockSize(s), bold) * CLOCK_LINE
        val widths = cols.map { columnW(s, it.first, it.second) }
        val gapW = u(COLUMN_GAP_DP)
        var x = cx - (widths.sum() + gapW * (cols.size - 1)) / 2f
        cols.forEachIndexed { i, (label, value, color) ->
            val colCx = x + widths[i] / 2f
            drawText(canvas, label, labelSize, medium, TimerColors.TAG_IDLE, colCx, top, Paint.Align.CENTER)
            drawValue(canvas, value, columnClockSize(s), color, colCx, top + labelH, valueH)
            x += widths[i]
            if (i < cols.size - 1) {
                fill.color = TimerColors.PANEL_BORDER
                canvas.drawRect(x + gapW / 2f - density / 2f, top + labelH * 0.4f, x + gapW / 2f + density / 2f, top + labelH + valueH * 0.85f, fill)
                x += gapW
            }
        }
    }

    private fun drawControls(canvas: Canvas, w: Float) {
        val pad = u(CONTROLS_PAD_DP)
        val top = bodyH + pad
        val gap = u(BUTTON_GAP_DP)
        val buttonW = (w - 2 * pad - gap * (buttonCount - 1)) / buttonCount
        val visualH = u(BUTTON_VISUAL_DP)
        for (i in 0 until buttonCount) {
            val left = pad + i * (buttonW + gap)
            buttonHitRects[i].set(left, top, left + buttonW, top + u(BUTTON_HIT_DP))
            val vy = top + (u(BUTTON_HIT_DP) - visualH) / 2f
            buttonVisualRects[i].set(left, vy, left + buttonW, vy + visualH)
            val exit = buttonActions[i] == ACTION_EXIT
            fill.color = when {
                i == pressed -> TimerColors.PANEL_HOVER
                exit -> TimerColors.EXIT_BG
                else -> TimerColors.PANEL_BTN
            }
            canvas.drawRoundRect(buttonVisualRects[i], visualH / 2f, visualH / 2f, fill)
            val label = buttonLabels[i] ?: ""
            val labelH = lineH(BUTTON_TEXT, medium)
            drawText(
                canvas, label, BUTTON_TEXT, medium,
                if (exit) TimerColors.EXIT_TEXT else TimerColors.TAG_MINE,
                buttonVisualRects[i].centerX(), buttonVisualRects[i].centerY() - labelH / 2f,
                Paint.Align.CENTER, buttonW - u(4f),
            )
        }
        for (i in buttonCount until MAX_BUTTONS) {
            buttonHitRects[i].setEmpty()
            buttonVisualRects[i].setEmpty()
        }
    }

    private fun clearButtonRects() {
        for (i in 0 until MAX_BUTTONS) {
            buttonHitRects[i].setEmpty()
            buttonVisualRects[i].setEmpty()
        }
    }

    // ---- Touch ------------------------------------------------------------

    private fun hit(x: Float, y: Float): Int {
        if (!controlsVisible) return -1
        for (i in 0 until buttonCount) if (buttonHitRects[i].contains(x, y)) return i
        return -1
    }

    override fun onTouchEvent(event: MotionEvent): Boolean {
        val l = listener
        when (event.actionMasked) {
            MotionEvent.ACTION_OUTSIDE -> {
                l?.onOutsideTouch()
                return true
            }
            MotionEvent.ACTION_DOWN -> {
                l?.onTouchActivity()
                downRawX = event.rawX
                downRawY = event.rawY
                lastRawX = downRawX
                lastRawY = downRawY
                dragging = false
                movedAway = false
                pressed = hit(event.x, event.y)
                if (pressed >= 0) invalidate()
                return true
            }
            MotionEvent.ACTION_MOVE -> {
                if (!dragging && !movedAway &&
                    (abs(event.rawX - downRawX) > touchSlop || abs(event.rawY - downRawY) > touchSlop)
                ) {
                    if (draggable) dragging = true else movedAway = true
                    if (pressed >= 0) {
                        pressed = -1
                        invalidate()
                    }
                }
                if (dragging) {
                    val dx = (event.rawX - lastRawX).roundToInt()
                    val dy = (event.rawY - lastRawY).roundToInt()
                    if (dx != 0 || dy != 0) {
                        lastRawX += dx
                        lastRawY += dy
                        l?.onDrag(dx, dy)
                    }
                    l?.onTouchActivity()
                }
                return true
            }
            MotionEvent.ACTION_UP -> {
                l?.onTouchActivity()
                if (dragging) {
                    dragging = false
                    l?.onDragEnd()
                    return true
                }
                val actionIndex = pressed
                pressed = -1
                if (movedAway) return true
                if (actionIndex >= 0) {
                    invalidate()
                    if (hit(event.x, event.y) == actionIndex) {
                        performClick()
                        dispatchAction(buttonActions[actionIndex])
                    }
                } else {
                    performClick()
                    l?.onDismissControls()
                }
                return true
            }
            MotionEvent.ACTION_CANCEL -> {
                if (dragging) l?.onDragEnd()
                dragging = false
                if (pressed >= 0) {
                    pressed = -1
                    invalidate()
                }
                return true
            }
        }
        return super.onTouchEvent(event)
    }

    override fun performClick(): Boolean {
        super.performClick()
        return true
    }

    private fun dispatchAction(action: Int) {
        val l = listener ?: return
        when (action) {
            ACTION_SETTINGS -> l.onSettings()
            ACTION_SWAP -> l.onSwapSide()
            ACTION_BOTH -> l.onToggleBothSides()
            ACTION_MINI -> l.onToggleMini()
            ACTION_EXIT -> l.onExit()
        }
    }

    // ---- Accessibility ----------------------------------------------------

    private fun updateAccessibilityDescription() {
        val s = state
        contentDescription = when {
            s == null -> "计时器悬浮窗"
            controlsVisible -> "替身计时 ${s.primaryText}。操作：${buttonLabels.take(buttonCount).filterNotNull().joinToString("、")}"
            else -> "替身计时 ${s.primaryText}，${s.sideModeText}。点击打开操作"
        }
    }

    override fun onInitializeAccessibilityNodeInfo(info: AccessibilityNodeInfo) {
        super.onInitializeAccessibilityNodeInfo(info)
        info.className = "android.widget.LinearLayout"
        info.contentDescription = contentDescription
        info.isClickable = true
        if (!controlsVisible) {
            info.addAction(AccessibilityNodeInfo.AccessibilityAction(AccessibilityNodeInfo.ACTION_CLICK, "打开操作"))
        } else {
            for (i in 0 until buttonCount) {
                info.addAction(AccessibilityNodeInfo.AccessibilityAction(A11Y_BASE + buttonActions[i], buttonLabels[i]))
            }
        }
    }

    override fun performAccessibilityAction(action: Int, arguments: android.os.Bundle?): Boolean {
        if (action == AccessibilityNodeInfo.ACTION_CLICK && !controlsVisible) {
            performClick()
            listener?.onDismissControls()
            return true
        }
        if (action in A11Y_BASE + ACTION_SETTINGS..A11Y_BASE + ACTION_EXIT) {
            dispatchAction(action - A11Y_BASE)
            return true
        }
        return super.performAccessibilityAction(action, arguments)
    }

    override fun onConfigurationChanged(newConfig: Configuration?) {
        super.onConfigurationChanged(newConfig)
        onConfigChanged?.invoke()
    }

    companion object {
        // Public scale contract shared with MainActivity and the settings panel.
        const val MINIMUM_OVERLAY_SCALE = 0.25
        const val MAXIMUM_OVERLAY_SCALE = 3.00

        fun normalizedOverlayScale(scale: Double): Double =
            scale.coerceIn(MINIMUM_OVERLAY_SCALE, MAXIMUM_OVERLAY_SCALE)

        private const val CLOCK_TEMPLATE = "88.8"
        /** Clock glyphs sit in a slightly taller line so they never touch the header. */
        private const val CLOCK_LINE = 1.0f
        /** Waiting words (待替身 …) are smaller than a running countdown. */
        private const val STATUS_RATIO = 0.55f

        // Text sizes in sp at 100 % overlay size.
        private const val MINI_CAPTION = 10f
        private const val MINI_CLOCK = 26f
        private const val MINI_COLUMN_LABEL = 9f
        private const val MINI_COLUMN_CLOCK = 20f
        private const val FULL_TAG = 12f
        private const val FULL_EVENT = 12f
        private const val FULL_CLOCK = 36f
        private const val FULL_COLUMN_LABEL = 10f
        private const val FULL_COLUMN_CLOCK = 28f
        private const val FULL_INFO = 10.5f
        private const val BUTTON_TEXT = 13f

        // Dimensions in dp at 100 % overlay size.
        private const val PAD_X_DP = 12f
        private const val PAD_Y_DP = 8f
        private const val GAP_DP = 3f
        private const val ACCENT_DP = 3f
        private const val RADIUS_DP = 12f
        private const val COLUMN_GAP_DP = 14f
        private const val CONTROLS_PAD_DP = 4f
        private const val BUTTON_HIT_DP = 44f
        private const val BUTTON_VISUAL_DP = 36f
        private const val BUTTON_PAD_DP = 10f
        private const val BUTTON_GAP_DP = 4f
        private const val MIN_BUTTON_DP = 48f
        private const val MAX_BUTTONS = 5

        private const val LABEL_SETTINGS = "设置"
        private const val LABEL_SWAP = "换边"
        private const val LABEL_BOTH = "两边"
        private const val LABEL_SINGLE = "单边"
        private const val LABEL_MINI = "迷你"
        private const val LABEL_FULL = "完整"
        private const val LABEL_EXIT = "退出"

        private const val ACTION_SETTINGS = 1
        private const val ACTION_SWAP = 2
        private const val ACTION_BOTH = 3
        private const val ACTION_MINI = 4
        private const val ACTION_EXIT = 5

        /** Custom AccessibilityNodeInfo action IDs: A11Y_BASE + ACTION_*. */
        private const val A11Y_BASE = 0x01020000
    }
}
