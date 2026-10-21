package com.narutotimer.ui

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.DashPathEffect
import android.graphics.Matrix
import android.graphics.Paint
import android.graphics.Rect
import android.graphics.RectF
import android.graphics.Typeface
import android.util.TypedValue
import android.view.MotionEvent
import android.view.View
import android.view.ViewConfiguration
import kotlin.math.hypot
import kotlin.math.max
import kotlin.math.min

/**
 * Region-calibration board (docs/区域校准.md): one captured frame fitted to the view
 * (aspect kept) and the four boxes in frame fractions (0–1, top-left origin).
 *
 * One finger edits the selected box: inside = move, edges/corners (generous outer
 * margin) = resize, elsewhere = draw a new box. A tap on another box selects it. Two
 * fingers zoom and pan; a double tap toggles zoom. Edits are reported on finger up.
 *
 * Under the boxes, [autoRegions] shows what the engine actually used on this frame (thin
 * dashed white, not editable): sampled bead centres, the name search rect and the avatar
 * window of each side. A box marked automatic is labelled 「…（自动）」.
 */
class CalibrationView(context: Context) : View(context) {
    /**
     * The engine's regions on this frame (docs/区域校准.md 每帧几何) in frame fractions; index 0
     * left, 1 right. Rects may reach past the frame (the avatar window often does).
     */
    class AutoRegions(
        /** Sampled bead centres per side: x0, y0, x1, y1, … */
        val beads: Array<FloatArray>,
        val names: Array<RectF?>,
        val avatars: Array<RectF?>,
    )

    interface Listener {
        /** A box was drawn, moved or resized. */
        fun onBoxEdited(index: Int) {}
        /** A tap landed on another box. */
        fun onBoxTapped(index: Int) {}
        fun onZoomChanged(zoomed: Boolean) {}
    }

    var listener: Listener? = null

    var image: Bitmap? = null
        private set

    private val boxes = arrayOfNulls<RectF>(COUNT)
    /** The box shows the automatic position (label 「…（自动）」). */
    private val automatic = BooleanArray(COUNT)

    var autoRegions: AutoRegions? = null
        set(value) {
            field = value
            invalidate()
        }

    /** 「显示自动识别区域」 */
    var showAuto = true
        set(value) {
            field = value
            invalidate()
        }

    var selected = 0
        set(value) {
            field = value.coerceIn(0, COUNT - 1)
            updateExclusion()
            invalidate()
        }

    private val density = resources.displayMetrics.density
    private val touchSlop = ViewConfiguration.get(context).scaledTouchSlop.toFloat()
    private val doubleTapMs = ViewConfiguration.getDoubleTapTimeout().toLong()
    private val handleSlop = 24f * density
    private val fitPad = 12f * density
    private val overscroll = 56f * density

    // Screen = translate + frame pixel * scale.
    private var fitScale = 1f
    private var scale = 1f
    private var tx = 0f
    private var ty = 0f
    private var reportedZoom = false

    private var mode = MODE_NONE
    private var hit = HIT_NONE
    private var downX = 0f
    private var downY = 0f
    private var downAt = 0L
    private var moved = false
    private var before: RectF? = null
    private var anchorX = 0f
    private var anchorY = 0f
    private var pinchDist = 1f
    private var pinchX = 0f
    private var pinchY = 0f
    private var lastTapAt = Long.MIN_VALUE / 2
    private var lastTapX = 0f
    private var lastTapY = 0f

    private val matrix = Matrix()
    private val bitmapPaint = Paint(Paint.FILTER_BITMAP_FLAG)
    private val stroke = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE }
    private val fill = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }
    private val labelPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        textSize = sp(11f)
        typeface = Typeface.create(Typeface.DEFAULT, 600, false)
    }
    private val numberPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        textSize = sp(9.5f)
        textAlign = Paint.Align.CENTER
        typeface = Typeface.create(Typeface.DEFAULT, 700, false)
    }
    private val labelMetrics = Paint.FontMetrics()
    private val screen = RectF()
    private val pill = RectF()
    private val autoStroke = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeCap = Paint.Cap.BUTT
    }
    private val autoDash = DashPathEffect(floatArrayOf(5f * density, 3.5f * density), 0f)
    private val autoLabelPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        textSize = sp(9.5f)
        color = AUTO
    }
    private var autoLines = FloatArray(0)

    fun setImage(bitmap: Bitmap?) {
        image = bitmap
        mode = MODE_NONE
        fit()
    }

    fun box(index: Int): RectF? = boxes[index]?.let { RectF(it) }

    /** [automatic]: the box is the automatic position, labelled 「…（自动）」. */
    fun setBox(index: Int, rect: RectF?, automatic: Boolean = false) {
        boxes[index] = rect?.let { RectF(it) }
        this.automatic[index] = automatic && rect != null
        updateExclusion()
        invalidate()
    }

    val zoomed: Boolean get() = image != null && scale > fitScale * 1.02f

    /** Whole frame visible, centred. */
    fun fit() {
        val b = image
        if (b != null && width > 0 && height > 0) {
            fitScale = min((width - 2 * fitPad).coerceAtLeast(1f) / b.width, (height - 2 * fitPad).coerceAtLeast(1f) / b.height)
            scale = fitScale
            tx = (width - b.width * scale) / 2f
            ty = (height - b.height * scale) / 2f
        }
        viewChanged()
    }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh)
        fit()
    }

    // ---- Mapping ----------------------------------------------------------

    private fun sx(fx: Float) = tx + fx * image!!.width * scale
    private fun sy(fy: Float) = ty + fy * image!!.height * scale
    private fun fx(x: Float) = (x - tx) / (image!!.width * scale)
    private fun fy(y: Float) = (y - ty) / (image!!.height * scale)

    private fun toScreen(r: RectF, out: RectF): RectF {
        out.set(sx(r.left), sy(r.top), sx(r.right), sy(r.bottom))
        return out
    }

    private fun zoomAt(factor: Float, x: Float, y: Float) {
        if (image == null) return
        val next = (scale * factor).coerceIn(fitScale, fitScale * MAX_ZOOM)
        val k = next / scale
        tx = x - (x - tx) * k
        ty = y - (y - ty) * k
        scale = next
        clampPan()
    }

    private fun clampPan() {
        val b = image ?: return
        val w = b.width * scale
        val h = b.height * scale
        // Zoomed in, the frame may move a little past the view edge so boxes at the frame
        // border (avatars) can be grabbed away from the screen edge.
        tx = if (w + 2 * fitPad <= width) (width - w) / 2f else clamp(tx, width - w - overscroll, overscroll)
        ty = if (h + 2 * fitPad <= height) (height - h) / 2f else clamp(ty, height - h - overscroll, overscroll)
    }

    private fun viewChanged() {
        val z = zoomed
        if (z != reportedZoom) {
            reportedZoom = z
            listener?.onZoomChanged(z)
        }
        updateExclusion()
        invalidate()
    }

    /** Keeps the system back gesture off the HUD area being edited (the system caps it at 200 dp). */
    private fun updateExclusion() {
        if (width <= 0 || height <= 0) return
        val span = (EXCLUSION_DP * density).toInt().coerceAtMost(height)
        val r = boxes[selected]
        val centre = when {
            image == null -> height / 2
            r != null -> ((sy(r.top) + sy(r.bottom)) / 2f).toInt()
            else -> sy(0.08f).toInt()
        }
        val top = (centre - span / 2).coerceIn(0, height - span)
        systemGestureExclusionRects = listOf(Rect(0, top, width, top + span))
    }

    // ---- Touch ------------------------------------------------------------

    override fun onTouchEvent(e: MotionEvent): Boolean {
        if (image == null) return false
        when (e.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                parent?.requestDisallowInterceptTouchEvent(true)
                downX = e.x
                downY = e.y
                downAt = e.eventTime
                moved = false
                before = boxes[selected]?.let { RectF(it) }
                anchorX = fx(e.x).coerceIn(0f, 1f)
                anchorY = fy(e.y).coerceIn(0f, 1f)
                hit = hitTest(e.x, e.y)
                mode = when (hit) {
                    HIT_NONE -> MODE_DRAW
                    HIT_MOVE -> MODE_MOVE
                    else -> MODE_RESIZE
                }
            }
            MotionEvent.ACTION_POINTER_DOWN -> {
                // A second finger means zoom/pan: undo whatever the first finger started.
                if (editing()) revert()
                mode = MODE_PINCH
                startPinch(e, -1)
            }
            MotionEvent.ACTION_MOVE -> when (mode) {
                MODE_PINCH -> pinch(e)
                MODE_DRAW, MODE_MOVE, MODE_RESIZE -> drag(e.x, e.y)
            }
            MotionEvent.ACTION_POINTER_UP -> if (mode == MODE_PINCH) {
                if (e.pointerCount > 2) startPinch(e, e.actionIndex) else mode = MODE_IGNORE
            }
            MotionEvent.ACTION_UP -> {
                // The lift position can differ from the last move.
                if (editing()) drag(e.x, e.y)
                when (mode) {
                    MODE_DRAW -> if (moved) commitDraw() else tap(e)
                    MODE_MOVE, MODE_RESIZE -> if (moved) commit() else tap(e)
                }
                mode = MODE_NONE
            }
            MotionEvent.ACTION_CANCEL -> {
                if (editing()) revert()
                mode = MODE_NONE
            }
        }
        return true
    }

    private fun editing() = mode == MODE_DRAW || mode == MODE_MOVE || mode == MODE_RESIZE

    private fun hitTest(x: Float, y: Float): Int {
        val r = boxes[selected] ?: return HIT_NONE
        val s = toScreen(r, screen)
        if (x < s.left - handleSlop || x > s.right + handleSlop || y < s.top - handleSlop || y > s.bottom + handleSlop) {
            return HIT_NONE
        }
        // Outside the box the whole margin grabs an edge; inside only a quarter of the
        // size does, so even a thin bead box keeps a middle band for moving.
        val inX = min(handleSlop, s.width() / 4f)
        val inY = min(handleSlop, s.height() / 4f)
        var flags = 0
        if (x <= s.left + inX) flags = flags or EDGE_LEFT else if (x >= s.right - inX) flags = flags or EDGE_RIGHT
        if (y <= s.top + inY) flags = flags or EDGE_TOP else if (y >= s.bottom - inY) flags = flags or EDGE_BOTTOM
        return if (flags == 0) HIT_MOVE else flags
    }

    private fun drag(x: Float, y: Float) {
        val b = image ?: return
        if (!moved) {
            if (hypot(x - downX, y - downY) < touchSlop) return
            moved = true
        }
        val dx = (x - downX) / (b.width * scale)
        val dy = (y - downY) / (b.height * scale)
        val minW = MIN_PX / b.width
        val minH = MIN_PX / b.height
        boxes[selected] = when (mode) {
            MODE_DRAW -> {
                val px = fx(x).coerceIn(0f, 1f)
                val py = fy(y).coerceIn(0f, 1f)
                RectF(min(anchorX, px), min(anchorY, py), max(anchorX, px), max(anchorY, py))
            }
            MODE_MOVE -> {
                val s = before ?: return
                val mx = clamp(dx, -s.left, 1f - s.right)
                val my = clamp(dy, -s.top, 1f - s.bottom)
                RectF(s.left + mx, s.top + my, s.right + mx, s.bottom + my)
            }
            else -> {
                val s = before ?: return
                RectF(s).apply {
                    if (hit and EDGE_LEFT != 0) left = clamp(s.left + dx, 0f, s.right - minW)
                    if (hit and EDGE_RIGHT != 0) right = clamp(s.right + dx, s.left + minW, 1f)
                    if (hit and EDGE_TOP != 0) top = clamp(s.top + dy, 0f, s.bottom - minH)
                    if (hit and EDGE_BOTTOM != 0) bottom = clamp(s.bottom + dy, s.top + minH, 1f)
                }
            }
        }
        invalidate()
    }

    private fun commitDraw() {
        val b = image ?: return
        val r = boxes[selected]
        if (r == null || r.width() * b.width < MIN_PX || r.height() * b.height < MIN_PX) {
            revert()
            return
        }
        commit()
    }

    private fun commit() {
        updateExclusion()
        listener?.onBoxEdited(selected)
    }

    private fun revert() {
        boxes[selected] = before?.let { RectF(it) }
        invalidate()
    }

    private fun tap(e: MotionEvent) {
        if (downAt - lastTapAt <= doubleTapMs && hypot(e.x - lastTapX, e.y - lastTapY) < 48f * density) {
            lastTapAt = Long.MIN_VALUE / 2
            if (zoomed) fit() else {
                zoomAt(DOUBLE_TAP_ZOOM, e.x, e.y)
                viewChanged()
            }
            return
        }
        lastTapAt = e.eventTime
        lastTapX = e.x
        lastTapY = e.y
        val i = boxAt(e.x, e.y)
        if (i >= 0 && i != selected) listener?.onBoxTapped(i)
    }

    /** Smallest box under the point (with a small margin), or -1. */
    private fun boxAt(x: Float, y: Float): Int {
        val m = 8f * density
        var best = -1
        var bestArea = Float.MAX_VALUE
        for (i in 0 until COUNT) {
            val r = boxes[i] ?: continue
            val s = toScreen(r, screen)
            if (x < s.left - m || x > s.right + m || y < s.top - m || y > s.bottom + m) continue
            val area = s.width() * s.height()
            if (area < bestArea) {
                best = i
                bestArea = area
            }
        }
        return best
    }

    private fun startPinch(e: MotionEvent, skip: Int) {
        var n = 0
        var x0 = 0f
        var y0 = 0f
        var x1 = 0f
        var y1 = 0f
        for (i in 0 until e.pointerCount) {
            if (i == skip) continue
            if (n == 0) {
                x0 = e.getX(i); y0 = e.getY(i)
            } else {
                x1 = e.getX(i); y1 = e.getY(i)
            }
            if (++n == 2) break
        }
        if (n < 2) {
            mode = MODE_IGNORE
            return
        }
        pinchDist = hypot(x1 - x0, y1 - y0).coerceAtLeast(1f)
        pinchX = (x0 + x1) / 2f
        pinchY = (y0 + y1) / 2f
    }

    private fun pinch(e: MotionEvent) {
        if (e.pointerCount < 2) return
        val x0 = e.getX(0)
        val y0 = e.getY(0)
        val x1 = e.getX(1)
        val y1 = e.getY(1)
        val d = hypot(x1 - x0, y1 - y0).coerceAtLeast(1f)
        val cx = (x0 + x1) / 2f
        val cy = (y0 + y1) / 2f
        zoomAt(d / pinchDist, cx, cy)
        tx += cx - pinchX
        ty += cy - pinchY
        clampPan()
        pinchDist = d
        pinchX = cx
        pinchY = cy
        viewChanged()
    }

    // ---- Drawing ----------------------------------------------------------

    override fun onDraw(canvas: Canvas) {
        val b = image ?: return
        matrix.setScale(scale, scale)
        matrix.postTranslate(tx, ty)
        // Zoomed far in, show crisp frame pixels so edges can be placed exactly.
        bitmapPaint.isFilterBitmap = scale < PIXEL_ZOOM
        canvas.drawBitmap(b, matrix, bitmapPaint)
        stroke.color = Ui.OUTLINE
        stroke.strokeWidth = density
        canvas.drawRect(tx, ty, tx + b.width * scale, ty + b.height * scale, stroke)
        if (showAuto) drawAuto(canvas, b)
        for (i in 0 until COUNT) if (i != selected) drawBox(canvas, i, false)
        drawBox(canvas, selected, true)
    }

    // ---- Automatic regions (what the engine used; not editable) -------------

    private fun drawAuto(canvas: Canvas, b: Bitmap) {
        val a = autoRegions ?: return
        // Clipped to the frame: the engine crops to it too.
        val l = max(tx, 0f)
        val t = max(ty, 0f)
        val r = min(tx + b.width * scale, width.toFloat())
        val bottom = min(ty + b.height * scale, height.toFloat())
        if (r <= l || bottom <= t) return
        canvas.save()
        canvas.clipRect(l, t, r, bottom)
        for (side in 0 until 2) {
            a.names.getOrNull(side)?.let { dashedRect(canvas, toScreen(it, screen)) }
            a.avatars.getOrNull(side)?.let { dashedRect(canvas, toScreen(it, screen)) }
            a.beads.getOrNull(side)?.let { drawAutoBeads(canvas, it) }
        }
        for (side in 0 until 2) {
            val right = side == 1
            a.names.getOrNull(side)?.let { autoLabel(canvas, "名字", toScreen(it, screen), right, l, t, r, bottom) }
            a.avatars.getOrNull(side)?.let { autoLabel(canvas, "头像窗口", toScreen(it, screen), right, l, t, r, bottom) }
        }
        canvas.restore()
    }

    /** Thin dashed white over a dark line: readable on bright and dark HUDs. */
    private fun dashedRect(canvas: Canvas, s: RectF) {
        autoStroke.pathEffect = null
        autoStroke.color = AUTO_HALO
        autoStroke.strokeWidth = 2.4f * density
        canvas.drawRect(s, autoStroke)
        autoStroke.pathEffect = autoDash
        autoStroke.color = AUTO
        autoStroke.strokeWidth = 1.1f * density
        canvas.drawRect(s, autoStroke)
        autoStroke.pathEffect = null
    }

    /** Open crosshairs on the sampled pixels: the centre stays visible for the box's own dots. */
    private fun drawAutoBeads(canvas: Canvas, beads: FloatArray) {
        val b = image ?: return
        val n = beads.size / 2
        if (n == 0) return
        var pitch = Float.MAX_VALUE
        for (k in 1 until n) {
            val d = hypot(
                (beads[2 * k] - beads[2 * k - 2]) * b.width * scale,
                (beads[2 * k + 1] - beads[2 * k - 1]) * b.height * scale,
            )
            if (d > 0f) pitch = min(pitch, d)
        }
        // A third of the pitch: neighbours stay apart at any zoom, the bead itself stays visible.
        val outer = if (pitch == Float.MAX_VALUE) 6f * density else (pitch * 0.36f).coerceIn(3f * density, 16f * density)
        val inner = outer * 0.4f
        if (autoLines.size < n * 16) autoLines = FloatArray(n * 16)
        val lines = autoLines
        for (k in 0 until n) {
            val cx = sx(beads[2 * k])
            val cy = sy(beads[2 * k + 1])
            val o = k * 16
            lines[o] = cx - outer; lines[o + 1] = cy; lines[o + 2] = cx - inner; lines[o + 3] = cy
            lines[o + 4] = cx + inner; lines[o + 5] = cy; lines[o + 6] = cx + outer; lines[o + 7] = cy
            lines[o + 8] = cx; lines[o + 9] = cy - outer; lines[o + 10] = cx; lines[o + 11] = cy - inner
            lines[o + 12] = cx; lines[o + 13] = cy + inner; lines[o + 14] = cx; lines[o + 15] = cy + outer
        }
        autoStroke.color = AUTO_HALO
        autoStroke.strokeWidth = 2.8f * density
        canvas.drawLines(lines, 0, n * 16, autoStroke)
        autoStroke.color = AUTO
        autoStroke.strokeWidth = 1.2f * density
        canvas.drawLines(lines, 0, n * 16, autoStroke)
    }

    /** Small label inside the region's top corner (outer corner of each side), kept on the visible frame. */
    private fun autoLabel(
        canvas: Canvas, text: String, s: RectF, alignRight: Boolean,
        l: Float, t: Float, r: Float, bottom: Float,
    ) {
        if (s.right < l || s.left > r || s.bottom < t || s.top > bottom) return
        val padX = 3.5f * density
        val padY = 1.5f * density
        val inset = 2f * density
        val fm = labelMetrics.also { autoLabelPaint.getFontMetrics(it) }
        val w = autoLabelPaint.measureText(text) + 2 * padX
        val h = fm.descent - fm.ascent + 2 * padY
        val x = clamp(if (alignRight) s.right - inset - w else s.left + inset, l + inset, r - inset - w)
        val y = clamp(s.top + inset, t + inset, bottom - inset - h)
        pill.set(x, y, x + w, y + h)
        fill.color = AUTO_LABEL_BG
        canvas.drawRoundRect(pill, 3f * density, 3f * density, fill)
        canvas.drawText(text, x + padX, y + padY - fm.ascent, autoLabelPaint)
    }

    private fun drawBox(canvas: Canvas, i: Int, active: Boolean) {
        val r = boxes[i] ?: return
        val color = COLORS[i]
        val s = toScreen(r, screen)
        // Zoomed in, a box can be entirely off screen: no clamped label without its box.
        if (s.right < 0f || s.left > width || s.bottom < 0f || s.top > height) return
        if (active) {
            fill.color = (color and 0x00FFFFFF) or 0x26000000
            canvas.drawRect(s, fill)
        }
        // Lines sit just outside the box so the beads/portrait inside stay visible.
        val w = (if (active) 2f else 1.5f) * density
        val halo = 1.2f * density
        stroke.color = HALO
        stroke.strokeWidth = halo
        drawOutset(canvas, s, w + halo / 2f)
        stroke.color = color
        stroke.strokeWidth = w
        drawOutset(canvas, s, w / 2f)
        if (i == LEFT_BEADS || i == RIGHT_BEADS) drawBeadCentres(canvas, i, s, color)
        drawLabel(canvas, if (automatic[i]) LABELS[i] + "（自动）" else LABELS[i], s, color, w + halo)
        if (active) drawHandles(canvas, s, color, w + halo)
    }

    private fun drawOutset(canvas: Canvas, s: RectF, by: Float) {
        canvas.drawRect(s.left - by, s.top - by, s.right + by, s.bottom + by, stroke)
    }

    /** Bead i centre: (i + 0.5)/4 of the width from the outer end (left side: left end). */
    private fun drawBeadCentres(canvas: Canvas, i: Int, s: RectF, color: Int) {
        val pitch = s.width() / 4f
        val cy = s.centerY()
        val ring = min(pitch * 0.32f, s.height() * 0.5f)
        val numbers = pitch >= 16f * density
        for (k in 0 until 4) {
            val cx = if (i == LEFT_BEADS) s.left + (k + 0.5f) * pitch else s.right - (k + 0.5f) * pitch
            if (ring >= 4f * density) {
                stroke.color = color
                stroke.strokeWidth = density
                canvas.drawCircle(cx, cy, ring, stroke)
            }
            fill.color = HALO
            canvas.drawCircle(cx, cy, 2.6f * density, fill)
            fill.color = color
            canvas.drawCircle(cx, cy, 1.6f * density, fill)
            if (numbers) {
                val y = s.top - 6f * density
                numberPaint.style = Paint.Style.STROKE
                numberPaint.strokeWidth = 2.5f * density
                numberPaint.color = HALO
                canvas.drawText("${k + 1}", cx, y, numberPaint)
                numberPaint.style = Paint.Style.FILL
                numberPaint.color = color
                canvas.drawText("${k + 1}", cx, y, numberPaint)
            }
        }
    }

    private fun drawLabel(canvas: Canvas, text: String, s: RectF, color: Int, gap: Float) {
        val padX = 5f * density
        val padY = 2.5f * density
        val fm = labelMetrics.also { labelPaint.getFontMetrics(it) }
        val h = fm.descent - fm.ascent + 2 * padY
        val w = labelPaint.measureText(text) + 2 * padX
        var y = s.bottom + gap + 2f * density
        if (y + h > height) y = s.top - gap - 2f * density - h
        val x = clamp(s.left - gap, 0f, width - w)
        y = clamp(y, 0f, height - h)
        pill.set(x, y, x + w, y + h)
        fill.color = color
        canvas.drawRoundRect(pill, 4f * density, 4f * density, fill)
        labelPaint.color = LABEL_TEXT
        canvas.drawText(text, x + padX, y + padY - fm.ascent, labelPaint)
    }

    /** Small squares outside the corners (and edge midpoints when there is room). */
    private fun drawHandles(canvas: Canvas, s: RectF, color: Int, gap: Float) {
        val k = 7f * density
        fun square(cx: Float, cy: Float) {
            fill.color = HALO
            canvas.drawRect(cx - k / 2f - 1f, cy - k / 2f - 1f, cx + k / 2f + 1f, cy + k / 2f + 1f, fill)
            fill.color = color
            canvas.drawRect(cx - k / 2f, cy - k / 2f, cx + k / 2f, cy + k / 2f, fill)
        }
        val o = gap + k / 2f
        square(s.left - o, s.top - o)
        square(s.right + o, s.top - o)
        square(s.left - o, s.bottom + o)
        square(s.right + o, s.bottom + o)
        if (s.width() >= 4 * k) {
            square(s.centerX(), s.top - o)
            square(s.centerX(), s.bottom + o)
        }
        if (s.height() >= 4 * k) {
            square(s.left - o, s.centerY())
            square(s.right + o, s.centerY())
        }
    }

    private fun sp(v: Float) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_SP, v, resources.displayMetrics)

    companion object {
        /** Indices follow Settings.KEY_CALIB_RECTS: left beads, right beads, left avatar, right avatar. */
        const val LEFT_BEADS = 0
        const val RIGHT_BEADS = 1
        const val LEFT_AVATAR = 2
        const val RIGHT_AVATAR = 3
        const val COUNT = 4
        val LABELS = arrayOf("左侧豆", "右侧豆", "左侧头像", "右侧头像")
        val COLORS = intArrayOf(Ui.ACCENT, Ui.WARN, Ui.OK, 0xFFFF8AD8.toInt())

        private const val HALO = 0xB3000000.toInt()
        private const val LABEL_TEXT = 0xFF03161D.toInt()
        /** Automatic regions: semi-transparent white on a dark line, never a box colour. */
        private const val AUTO = 0xE6FFFFFF.toInt()
        private const val AUTO_HALO = 0x8C000000.toInt()
        private const val AUTO_LABEL_BG = 0x99000000.toInt()
        private const val MODE_NONE = 0
        private const val MODE_DRAW = 1
        private const val MODE_MOVE = 2
        private const val MODE_RESIZE = 3
        private const val MODE_PINCH = 4
        private const val MODE_IGNORE = 5
        private const val EDGE_LEFT = 1
        private const val EDGE_TOP = 2
        private const val EDGE_RIGHT = 4
        private const val EDGE_BOTTOM = 8
        private const val HIT_NONE = -1
        private const val HIT_MOVE = 16
        /** Smallest box side in frame pixels. */
        private const val MIN_PX = 3f
        private const val MAX_ZOOM = 12f
        private const val DOUBLE_TAP_ZOOM = 4f
        /** Screen pixels per frame pixel above which the frame is drawn unfiltered. */
        private const val PIXEL_ZOOM = 4f
        private const val EXCLUSION_DP = 200f

        private fun clamp(v: Float, lo: Float, hi: Float): Float = if (hi < lo) lo else v.coerceIn(lo, hi)
    }
}
