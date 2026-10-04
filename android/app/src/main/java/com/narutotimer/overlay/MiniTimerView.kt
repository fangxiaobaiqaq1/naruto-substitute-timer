package com.narutotimer.overlay

// Owner: kt-overlay-ui
// A compact, Canvas-only Android renderer.  It intentionally has no desktop
// widgets or hover assumptions: the controller supplies an immutable
// OverlayState and opens this view only while the action row is needed.

import android.content.Context
import android.content.res.Configuration
import android.graphics.Canvas
import android.graphics.Paint
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

/** Native Canvas renderer for the full and compact mini overlay modes. */
class MiniTimerView(context: Context) : View(context) {
    interface Listener {
        fun onSettings()
        fun onSwapSide()
        fun onToggleBothSides()
        fun onToggleMini()
        /** Explicitly stop and hide the game overlay. */
        fun onExit()
        /** Dragging delta in screen pixels. */
        fun onDrag(dx: Int, dy: Int)
        /** Dragging ended; controller persists and docks the position. */
        fun onDragEnd()
        /** A non-action tap or outside event dismisses the action row. */
        fun onDismissControls()
        /** Any interaction while the action row is open. */
        fun onTouchActivity()
    }

    var listener: Listener? = null

    /** Called when display configuration changes so the controller can reclamp. */
    var onConfigChanged: (() -> Unit)? = null

    /** Whether the compact action row is painted and hit-testable. */
    var controlsVisible: Boolean = false
        set(value) {
            if (field == value) return
            field = value
            pressed = -1
            updateAccessibilityDescription()
            // Mini mode reserves the action-row height, so opening it never
            // moves the countdown. Full mode grows only when the row is open.
            relayout()
        }

    /** Last immutable state supplied by Session. */
    var state: OverlayState? = null
        private set

    private val density = resources.displayMetrics.density
    private val paint = TextPaint(Paint.ANTI_ALIAS_FLAG or Paint.SUBPIXEL_TEXT_FLAG).apply {
        // Keep tenths aligned while the card is being redrawn.
        fontFeatureSettings = "tnum"
    }
    private val fill = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }
    private val border = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE }
    private val fm = Paint.FontMetrics()

    // Layout cache. Width is monotonic only within one layout structure, which
    // prevents tenth/name jitter without allowing a mode change to retain a
    // desktop-sized card forever.
    private var layoutW = 0f
    private var layoutH = 0f
    private var grownW = 0f
    private var structureKey = Long.MIN_VALUE

    private val buttonHitRects = Array(5) { RectF() }
    private val buttonVisualRects = Array(5) { RectF() }
    private val buttonActions = IntArray(5)
    private val buttonLabels = arrayOfNulls<String>(5)
    private var buttonCount = 0
    private var buttonPanel = RectF()
    private var pressed = -1
    private var controlsTop = 0f

    // Touch state. The card is touchable only while controlsVisible is true.
    private val touchSlop = ViewConfiguration.get(context).scaledTouchSlop
    private var downRawX = 0f
    private var downRawY = 0f
    private var lastRawX = 0f
    private var lastRawY = 0f
    private var dragging = false

    // Full-mode blocks.
    private var tagH = 0f
    private var clockRowW = 0f
    private var clockRowH = 0f
    private var infoH = 0f
    private var controlsW = 0f
    private var controlsH = 0f

    init {
        importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_YES
        isFocusable = true
        updateAccessibilityDescription()
    }

    // ---- State and dimensions --------------------------------------------

    /** Render only when a visible field changed; countdown text remains stable in width. */
    fun render(s: OverlayState) {
        val previous = state
        if (previous != null && sameVisible(previous, s)) {
            state = s
            updateAccessibilityDescription()
            return
        }
        state = s
        val key = structure(s)
        if (key != structureKey) {
            structureKey = key
            grownW = 0f
        }
        val oldW = layoutW
        val oldH = layoutH
        computeLayout()
        if (ceil(layoutW) != ceil(oldW) || ceil(layoutH) != ceil(oldH) || !isLaidOut) {
            requestLayout()
        }
        updateAccessibilityDescription()
        invalidate()
    }

    private fun relayout() {
        val s = state ?: return
        val key = structure(s)
        if (key != structureKey) {
            structureKey = key
            grownW = 0f
        }
        val oldW = layoutW
        val oldH = layoutH
        computeLayout()
        if (ceil(layoutW) != ceil(oldW) || ceil(layoutH) != ceil(oldH)) requestLayout()
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
        return key
    }

    private fun dp(value: Float): Float = value * density

    /** Same scale contract as the desktop port, but expressed in dp/sp-like pixels. */
    private fun textSize(baseDp: Float): Float {
        val scale = state?.fontScale?.toDouble() ?: 1.0
        return (Math.round(baseDp * normalizedOverlayScale(scale) * 10) / 10.0).toFloat()
    }

    private fun setText(sizeDp: Float, bold: Boolean, color: Int = 0) {
        // Canvas text follows the system scaledDensity (sp), then the app's
        // overlay fontScale is applied by textSize().
        paint.textSize = sizeDp * resources.displayMetrics.scaledDensity
        paint.typeface = if (bold) Typeface.DEFAULT_BOLD else Typeface.DEFAULT
        paint.color = color
    }

    private fun textW(text: String, sizeDp: Float, bold: Boolean): Float {
        setText(sizeDp, bold)
        return paint.measureText(text)
    }

    private fun textH(sizeDp: Float, bold: Boolean): Float {
        setText(sizeDp, bold)
        paint.getFontMetrics(fm)
        return fm.descent - fm.ascent
    }

    private fun clockW(text: String, sizeDp: Float): Float =
        max(textW(text, sizeDp, true), textW(CLOCK_TEMPLATE, sizeDp, true))

    private fun drawTextCentered(
        canvas: Canvas,
        text: String,
        sizeDp: Float,
        bold: Boolean,
        color: Int,
        centerX: Float,
        top: Float,
        maxWidth: Float = Float.MAX_VALUE,
    ) {
        setText(sizeDp, bold, color)
        val rendered = if (paint.measureText(text) > maxWidth) {
            TextUtils.ellipsize(text, paint, maxWidth.coerceAtLeast(0f), TextUtils.TruncateAt.END).toString()
        } else text
        paint.getFontMetrics(fm)
        canvas.drawText(rendered, centerX - paint.measureText(rendered) / 2f, top - fm.ascent, paint)
    }

    private fun buttonMinW(label: String): Float =
        max(dp(MIN_BUTTON_WIDTH_DP), textW(label, textSize(BUTTON_TEXT), false) + 2 * dp(BUTTON_PAD_DP))

    private fun collapsedText(s: OverlayState): String {
        val scene = s.infoText.substringBefore("  豆").ifBlank { "未识别场景" }
        val opponent = s.tagText.substringBefore(" · ").ifBlank { "对面·待认边" }
        val value = s.primaryText.takeIf { it.isNotEmpty() && it != "—" }
        if (s.showBothSides) {
            val left = s.leftText.takeIf { it.isNotEmpty() && it != "—" } ?: "—"
            val right = s.rightText.takeIf { it.isNotEmpty() && it != "—" } ?: "—"
            return "$opponent · 左 $left / 右 $right"
        }
        if (s.dual) {
            val alternate = s.altText.takeIf { it.isNotEmpty() } ?: "—"
            return if (value == null) "$scene · $opponent" else "$opponent · 15秒 $value / 10秒 $alternate"
        }
        return value?.let { "$opponent · $it" } ?: "$scene · $opponent"
    }

    private fun prepareButtons(s: OverlayState) {
        val both = if (s.showBothSides) LABEL_SINGLE else LABEL_BOTH
        if (s.mini) {
            setButtons(
                LABEL_SETTINGS to ACTION_SETTINGS,
                LABEL_SWAP to ACTION_SWAP,
                both to ACTION_BOTH,
                LABEL_EXIT to ACTION_EXIT,
            )
        } else {
            setButtons(
                LABEL_SWAP to ACTION_SWAP,
                LABEL_SETTINGS to ACTION_SETTINGS,
                both to ACTION_BOTH,
                LABEL_EXIT to ACTION_EXIT,
            )
        }
    }

    private fun setButtons(vararg buttons: Pair<String, Int>) {
        buttonCount = buttons.size
        for (i in buttons.indices) {
            buttonLabels[i] = buttons[i].first
            buttonActions[i] = buttons[i].second
        }
    }

    private fun computeLayout() {
        val s = state ?: return
        prepareButtons(s)
        val pad = dp(PAD_DP)
        val gap = dp(GAP_DP)
        val maxContentW = (dp(MAX_CARD_WIDTH_DP) - 2 * pad).coerceAtLeast(dp(160f))
        var contentW: Float
        var contentH: Float

        controlsW = 2 * pad + (0 until buttonCount).fold(0f) { total, index ->
            total + buttonMinW(buttonLabels[index]!!)
        } + gap * (buttonCount - 1).coerceAtLeast(0)
        controlsH = dp(BUTTON_HIT_DP)

        // The normal state is intentionally a thin status pill. The detailed
        // timer card and actions are only shown after the user taps the pill.
        // This keeps the game HUD unobstructed while retaining a native overlay.
        if (!controlsVisible) {
            val label = collapsedText(s)
            val size = textSize(COLLAPSED_TEXT)
            val desired = min(dp(COLLAPSED_WIDTH_DP), maxContentW)
            layoutW = desired + 2 * pad
            layoutH = textH(size, false) + 2 * pad
            grownW = desired
            return
        }

        if (s.showBothSides) {
            val labelSize = if (s.mini) MINI_BOTH_LABEL else BOTH_LABEL
            val labelH = textH(labelSize, false)
            val cdSize = textSize(if (s.mini) 32f else 36f)
            val colL = max(textW("左", labelSize, false), clockW(s.leftText, cdSize))
            val colR = max(textW("右", labelSize, false), clockW(s.rightText, cdSize))
            clockRowW = colL + textW(BOTH_SEPARATOR, labelSize, false) + colR
            clockRowH = labelH + gap + textH(cdSize, true)
        } else {
            val cdSize = when {
                s.mini -> textSize(38f)
                s.dual -> textSize(32f)
                else -> textSize(44f)
            }
            var primaryW = clockW(s.primaryText, cdSize)
            var primaryH = textH(cdSize, true)
            if (s.dual) {
                val labelSize = textSize(10f)
                val labelH = textH(labelSize, false)
                primaryW = max(primaryW, textW(PRIMARY_LABEL, labelSize, false))
                primaryH += labelH + gap
            }
            var rowW = primaryW
            var rowH = primaryH
            if (s.dual) {
                val separatorSize = textSize(24f)
                val separatorW = textW(ALT_SEPARATOR, separatorSize, false)
                val altSize = textSize(32f)
                val altLabelSize = textSize(10f)
                val altW = max(textW(ALT_LABEL, altLabelSize, false), clockW(s.altText, altSize))
                val altH = textH(altLabelSize, false) + gap + textH(altSize, true)
                rowW += gap + separatorW + gap + altW
                rowH = max(rowH, altH)
            }
            if (!s.mini) {
                val eventSize = textSize(EVENT_TEXT)
                rowW += gap + textW(s.eventText, eventSize, true)
                rowH = max(rowH, textH(eventSize, true))
            }
            clockRowW = rowW
            clockRowH = rowH
        }

        if (controlsVisible) {
            val label = collapsedText(s)
            val size = textSize(COLLAPSED_TEXT)
            val contentW = max(textW(label, size, false), controlsW)
            val contentH = textH(size, false) + gap + controlsH
            val desired = min(contentW, maxContentW)
            grownW = desired
            layoutW = desired + 2 * pad
            layoutH = contentH + 2 * pad
            return
        }

        if (s.mini) {
            tagH = textH(MINI_TAG_TEXT, false)
            infoH = textH(MINI_INFO_TEXT, false)
            val tagW = textW(s.tagText, MINI_TAG_TEXT, false)
            val infoW = textW(s.infoText, MINI_INFO_TEXT, false)
            contentW = max(max(clockRowW, tagW), max(infoW, controlsW))
            contentH = tagH + gap + clockRowH + gap + infoH + gap + controlsH
        } else {
            tagH = textH(TAG_TEXT, false)
            infoH = textH(INFO_TEXT, false)
            contentW = max(clockRowW, max(textW(s.tagText, TAG_TEXT, false), textW(s.infoText, INFO_TEXT, false)))
            contentH = tagH + gap + clockRowH + gap + infoH
            if (controlsVisible) contentH += gap + controlsH
            contentW = max(contentW, if (controlsVisible) controlsW else 0f)
        }

        // Keep the card compact on a 1920x1080 landscape display.  A long
        // label grows only to this cap and is ellipsized at draw time.
        val desired = min(contentW, maxContentW)
        grownW = min(maxContentW, max(grownW, desired))
        layoutW = grownW + 2 * pad
        layoutH = contentH + 2 * pad
    }

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

    override fun onDraw(canvas: Canvas) {
        val s = state ?: return
        val w = width.toFloat()
        val h = height.toFloat()
        val pad = dp(PAD_DP)
        val gap = dp(GAP_DP)

        // Rounded card: transparent corners, opaque dark glass inside. Window
        // alpha supplies the user-selected overall translucency.
        canvas.drawColor(android.graphics.Color.TRANSPARENT, PorterDuff.Mode.CLEAR)
        val card = RectF(0.5f, 0.5f, w - 0.5f, h - 0.5f)
        fill.color = TimerColors.PANEL_BG
        canvas.drawRoundRect(card, dp(CARD_RADIUS_DP), dp(CARD_RADIUS_DP), fill)
        border.color = TimerColors.PANEL_BORDER
        border.strokeWidth = dp(BORDER_DP)
        canvas.drawRoundRect(
            RectF(dp(BORDER_DP) / 2f, dp(BORDER_DP) / 2f, w - dp(BORDER_DP) / 2f, h - dp(BORDER_DP) / 2f),
            dp(CARD_RADIUS_DP),
            dp(CARD_RADIUS_DP),
            border,
        )

        // Keep long labels and clock rows inside the rounded card rather than
        // allowing text to paint into its transparent corner pixels.
        canvas.save()
        canvas.clipRect(dp(2f), dp(2f), w - dp(2f), h - dp(2f))

        // Both states use the same compact pill. Expanded state adds the
        // circular-style action row below it; it never restores the desktop
        // panel-sized card.
        val compactSize = textSize(COLLAPSED_TEXT)
        val compact = collapsedText(s)
        drawTextCentered(canvas, compact, compactSize, false, s.tagColor, w / 2f, pad, w - 2 * pad)
        if (controlsVisible) {
            controlsTop = pad + textH(compactSize, false) + gap
            drawControls(canvas, w, controlsTop, pad, gap)
        } else {
            clearButtonRects()
        }
        canvas.restore()
        return

        if (s.mini) {
            var y = pad
            val innerW = w - 2 * pad
            drawTextCentered(canvas, s.tagText, MINI_TAG_TEXT, false, s.tagColor, w / 2f, y, innerW)
            y += tagH + gap
            drawClockRow(canvas, s, w / 2f, y, gap)
            y += clockRowH + gap
            drawTextCentered(canvas, s.infoText, MINI_INFO_TEXT, false, TimerColors.TAG_IDLE, w / 2f, y, innerW)
            y += infoH + gap
            controlsTop = y
            if (controlsVisible) drawControls(canvas, w, y, pad, gap) else clearButtonRects()
            canvas.restore()
            return
        }

        var y = pad
        val innerW = w - 2 * pad
        drawTextCentered(canvas, s.tagText, TAG_TEXT, false, s.tagColor, w / 2f, y, innerW)
        y += tagH + gap
        drawClockRow(canvas, s, w / 2f, y, gap)
        y += clockRowH + gap
        drawTextCentered(canvas, s.infoText, INFO_TEXT, false, TimerColors.TAG_IDLE, w / 2f, y, innerW)
        y += infoH
        if (controlsVisible) {
            y += gap
            controlsTop = y
            drawControls(canvas, w, y, pad, gap)
        } else {
            clearButtonRects()
        }
        canvas.restore()
    }

    private fun drawClockRow(canvas: Canvas, s: OverlayState, centerX: Float, top: Float, gap: Float) {
        val left0 = centerX - clockRowW / 2f
        if (s.showBothSides) {
            val labelSize = if (s.mini) MINI_BOTH_LABEL else BOTH_LABEL
            val labelH = textH(labelSize, false)
            val cdSize = textSize(if (s.mini) 32f else 36f)
            val colL = max(textW("左", labelSize, false), clockW(s.leftText, cdSize))
            val separatorW = textW(BOTH_SEPARATOR, labelSize, false)
            val colR = max(textW("右", labelSize, false), clockW(s.rightText, cdSize))
            val lx = left0 + colL / 2f
            val rx = left0 + colL + separatorW + colR / 2f
            drawTextCentered(canvas, "左", labelSize, false, TimerColors.TAG_IDLE, lx, top)
            drawTextCentered(canvas, s.leftText, cdSize, true, s.leftColor, lx, top + labelH + gap)
            drawTextCentered(canvas, "右", labelSize, false, TimerColors.TAG_IDLE, rx, top)
            drawTextCentered(canvas, s.rightText, cdSize, true, s.rightColor, rx, top + labelH + gap)
            return
        }

        val cdSize = when {
            s.mini -> textSize(38f)
            s.dual -> textSize(32f)
            else -> textSize(44f)
        }
        var x = left0
        var primaryW = clockW(s.primaryText, cdSize)
        var primaryH = textH(cdSize, true)
        val labelSize = textSize(10f)
        val labelH = textH(labelSize, false)
        if (s.dual) {
            primaryW = max(primaryW, textW(PRIMARY_LABEL, labelSize, false))
            primaryH += labelH + gap
        }
        var py = top + (clockRowH - primaryH) / 2f
        val primaryCenter = x + primaryW / 2f
        if (s.dual) {
            drawTextCentered(canvas, PRIMARY_LABEL, labelSize, false, TimerColors.TAG_IDLE, primaryCenter, py)
            py += labelH + gap
        }
        drawTextCentered(canvas, s.primaryText, cdSize, true, s.primaryColor, primaryCenter, py)
        x += primaryW

        if (s.dual) {
            x += gap
            val separatorSize = textSize(24f)
            val separatorW = textW(ALT_SEPARATOR, separatorSize, false)
            drawTextCentered(
                canvas,
                ALT_SEPARATOR,
                separatorSize,
                false,
                TimerColors.TAG_IDLE,
                x + separatorW / 2f,
                top + (clockRowH - textH(separatorSize, false)) / 2f,
            )
            x += separatorW + gap
            val altSize = textSize(32f)
            val altLabelSize = textSize(10f)
            val altW = max(textW(ALT_LABEL, altLabelSize, false), clockW(s.altText, altSize))
            val altH = textH(altLabelSize, false) + gap + textH(altSize, true)
            val altTop = top + (clockRowH - altH) / 2f
            val altCenter = x + altW / 2f
            drawTextCentered(canvas, ALT_LABEL, altLabelSize, false, TimerColors.TAG_IDLE, altCenter, altTop)
            drawTextCentered(canvas, s.altText, altSize, true, s.altColor, altCenter, altTop + textH(altLabelSize, false) + gap)
            x += altW
        }
        if (!s.mini) {
            x += gap
            val eventSize = textSize(EVENT_TEXT)
            val eventW = textW(s.eventText, eventSize, true)
            drawTextCentered(
                canvas,
                s.eventText,
                eventSize,
                true,
                TimerColors.TAG_IDLE,
                x + eventW / 2f,
                top + (clockRowH - textH(eventSize, true)) / 2f,
            )
        }
    }

    private fun drawControls(canvas: Canvas, w: Float, top: Float, pad: Float, gap: Float) {
        val panelTop = top - dp(CONTROLS_PANEL_PAD_DP)
        val panelBottom = top + controlsH + dp(CONTROLS_PANEL_PAD_DP)
        buttonPanel.set(pad - dp(2f), panelTop, w - pad + dp(2f), panelBottom)
        fill.color = if (state?.mini == true) TimerColors.MINI_CONTROLS_BG else TimerColors.PANEL_BG
        canvas.drawRoundRect(buttonPanel, dp(7f), dp(7f), fill)
        border.color = TimerColors.PANEL_BORDER
        border.strokeWidth = dp(0.75f)
        canvas.drawRoundRect(buttonPanel, dp(7f), dp(7f), border)

        val rowW = w - 2 * pad
        val columnW = (rowW - (buttonCount - 1) * gap) / buttonCount
        for (i in 0 until buttonCount) {
            val left = pad + i * (columnW + gap)
            buttonHitRects[i].set(left, top, left + columnW, top + controlsH)
            val insetX = dp(2f)
            val insetY = (controlsH - dp(BUTTON_VISUAL_H_DP)) / 2f
            buttonVisualRects[i].set(left + insetX, top + insetY, left + columnW - insetX, top + insetY + dp(BUTTON_VISUAL_H_DP))
            drawButton(canvas, i)
        }
        for (i in buttonCount until buttonHitRects.size) {
            buttonHitRects[i].setEmpty()
            buttonVisualRects[i].setEmpty()
        }
    }

    private fun drawButton(canvas: Canvas, index: Int) {
        val visual = buttonVisualRects[index]
        val highImportance = buttonActions[index] == ACTION_MINI && state?.mini == true
        fill.color = when {
            index == pressed -> TimerColors.PANEL_HOVER
            highImportance -> TimerColors.CLOCK_LIVE
            else -> TimerColors.PANEL_BTN
        }
        canvas.drawRoundRect(visual, dp(6f), dp(6f), fill)
        val textColor = if (highImportance && index != pressed) TimerColors.GLASS_BG else TimerColors.CLOCK_LIVE
        drawTextCentered(
            canvas,
            buttonLabels[index] ?: "",
            textSize(BUTTON_TEXT),
            false,
            textColor,
            visual.centerX(),
            visual.centerY() - textH(textSize(BUTTON_TEXT), false) / 2f,
            (visual.width() - dp(6f)).coerceAtLeast(0f),
        )
    }

    private fun clearButtonRects() {
        for (i in buttonHitRects.indices) {
            buttonHitRects[i].setEmpty()
            buttonVisualRects[i].setEmpty()
        }
        buttonPanel.setEmpty()
    }

    // ---- Touch and accessibility -----------------------------------------

    private fun hit(x: Float, y: Float): Int {
        for (i in 0 until buttonCount) if (buttonHitRects[i].contains(x, y)) return i
        return -1
    }

    override fun onTouchEvent(event: MotionEvent): Boolean {
        val l = listener
        when (event.actionMasked) {
            MotionEvent.ACTION_OUTSIDE -> {
                l?.onDismissControls()
                return true
            }
            MotionEvent.ACTION_DOWN -> {
                l?.onTouchActivity()
                downRawX = event.rawX
                downRawY = event.rawY
                lastRawX = downRawX
                lastRawY = downRawY
                dragging = false
                pressed = if (controlsVisible) hit(event.x, event.y) else -1
                if (pressed >= 0) invalidate()
                return true
            }
            MotionEvent.ACTION_MOVE -> {
                if (!dragging &&
                    (abs(event.rawX - downRawX) > touchSlop || abs(event.rawY - downRawY) > touchSlop)
                ) {
                    dragging = true
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
                if (actionIndex >= 0) {
                    invalidate()
                    if (hit(event.x, event.y) == actionIndex) {
                        performClick()
                        dispatchAction(buttonActions[actionIndex])
                    }
                } else {
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

    private fun updateAccessibilityDescription() {
        val s = state
        contentDescription = if (s == null) {
            "计时器悬浮窗"
        } else if (controlsVisible) {
            "计时器，${s.primaryText}。操作：${buttonLabels.take(buttonCount).filterNotNull().joinToString("、")}"
        } else {
            "计时器，${collapsedText(s)}。点击打开控制"
        }
    }

    override fun onInitializeAccessibilityNodeInfo(info: AccessibilityNodeInfo) {
        super.onInitializeAccessibilityNodeInfo(info)
        info.className = "android.widget.LinearLayout"
        info.contentDescription = contentDescription
        info.isClickable = true
        if (!controlsVisible) {
            info.addAction(AccessibilityNodeInfo.AccessibilityAction(
                AccessibilityNodeInfo.ACTION_CLICK,
                "打开控制",
            ))
        }
        if (controlsVisible) {
            info.addAction(AccessibilityNodeInfo.AccessibilityAction(A11Y_SETTINGS, LABEL_SETTINGS))
            info.addAction(AccessibilityNodeInfo.AccessibilityAction(A11Y_SWAP, LABEL_SWAP))
            info.addAction(AccessibilityNodeInfo.AccessibilityAction(A11Y_BOTH, buttonLabels.getOrNull(2) ?: LABEL_BOTH))
            info.addAction(AccessibilityNodeInfo.AccessibilityAction(A11Y_EXIT, buttonLabels.getOrNull(3) ?: LABEL_EXIT))
        }
    }

    override fun performAccessibilityAction(action: Int, arguments: android.os.Bundle?): Boolean {
        return when (action) {
            AccessibilityNodeInfo.ACTION_CLICK -> {
                if (!controlsVisible) {
                    performClick()
                    listener?.onDismissControls()
                    true
                } else {
                    super.performAccessibilityAction(action, arguments)
                }
            }
            A11Y_SETTINGS -> { dispatchAction(ACTION_SETTINGS); true }
            A11Y_SWAP -> { dispatchAction(ACTION_SWAP); true }
            A11Y_BOTH -> { dispatchAction(ACTION_BOTH); true }
            A11Y_EXIT -> { dispatchAction(ACTION_EXIT); true }
            else -> super.performAccessibilityAction(action, arguments)
        }
    }

    override fun onConfigurationChanged(newConfig: Configuration?) {
        super.onConfigurationChanged(newConfig)
        onConfigChanged?.invoke()
    }

    companion object {
        // Public scale contract used by MainActivity and overlay tests.
        const val MINIMUM_OVERLAY_SCALE = 0.55
        const val MAXIMUM_OVERLAY_SCALE = 1.20

        fun normalizedOverlayScale(scale: Double): Double =
            if (scale < MINIMUM_OVERLAY_SCALE || scale > MAXIMUM_OVERLAY_SCALE) 1.0 else scale

        private const val CLOCK_TEMPLATE = "88.8"
        private const val COLLAPSED_TEXT = 13f
        private const val PRIMARY_LABEL = "15秒"
        private const val ALT_LABEL = "10秒"
        private const val ALT_SEPARATOR = " / "
        private const val BOTH_SEPARATOR = "  "
        private const val TAG_TEXT = 13f
        private const val INFO_TEXT = 11f
        private const val EVENT_TEXT = 13f
        private const val MINI_TAG_TEXT = 11f
        private const val MINI_INFO_TEXT = 10f
        private const val BOTH_LABEL = 14f
        private const val MINI_BOTH_LABEL = 11f
        private const val BUTTON_TEXT = 14f
        private const val BUTTON_PAD_DP = 8f
        private const val BUTTON_HIT_DP = 48f
        private const val BUTTON_VISUAL_H_DP = 36f
        private const val MIN_BUTTON_WIDTH_DP = 48f
        private const val CARD_RADIUS_DP = 10f
        private const val BORDER_DP = 1f
        private const val CONTROLS_PANEL_PAD_DP = 3f
        private const val MAX_CARD_WIDTH_DP = 360f
        private const val COLLAPSED_WIDTH_DP = 320f
        private const val PAD_DP = 9f
        private const val GAP_DP = 5f

        private const val LABEL_SETTINGS = "设置"
        private const val LABEL_SWAP = "换边"
        private const val LABEL_BOTH = "两边计时"
        private const val LABEL_SINGLE = "单边计时"
        private const val LABEL_EXIT = "退出"
        private const val LABEL_MINI = "迷你"

        private const val ACTION_SETTINGS = 1
        private const val ACTION_SWAP = 2
        private const val ACTION_BOTH = 3
        private const val ACTION_MINI = 4
        private const val ACTION_EXIT = 5

        // Custom AccessibilityNodeInfo action IDs.
        private const val A11Y_SETTINGS = 0x01020001
        private const val A11Y_SWAP = 0x01020002
        private const val A11Y_BOTH = 0x01020003
        private const val A11Y_EXIT = 0x01020004
    }
}
