package com.narutotimer.overlay

import android.content.Context
import android.content.res.Configuration
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.text.InputType
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.SeekBar
import android.widget.TextView
import com.narutotimer.Settings
import com.narutotimer.capture.ProjectionCaptureService
import com.narutotimer.tracker.Session
import com.narutotimer.tracker.TimerColors
import java.util.Locale

/**
 * Settings card rendered inside the accessibility overlay window.
 *
 * This view deliberately uses framework widgets only so it can be attached to
 * the controller's TYPE_ACCESSIBILITY_OVERLAY window without an Activity.
 */
class OverlaySettingsView(
    context: Context,
    private val session: Session,
    private val settings: Settings,
    var callbacks: Callbacks = object : Callbacks {},
) : LinearLayout(context) {

    interface Callbacks {
        fun onClose() {}
        fun onSaved() {}
        fun onSavedAndClose() {}
        fun onEditPosition() {}
        fun onResetPosition() {}
        fun onRequestProjection() {}
        fun onStopProjection() {}
    }

    private val density = resources.displayMetrics.density
    private var syncing = false
    private var initialized = false
    var onConfigChanged: (() -> Unit)? = null
    private val body = LinearLayout(context)
    private val panelHost = FramePanel(context)
    private val category = LinearLayout(context)
    private val statusText = TextView(context)

    private val sideGroup = RadioGroup(context)
    private val remember = CheckBox(context)
    private val both = CheckBox(context)
    private val names = EditText(context)
    private val ninja = EditText(context)
    private val modeGroup = RadioGroup(context)
    private val fullOpacity = SeekBar(context)
    private val miniOpacity = SeekBar(context)
    private val sizeBar = SeekBar(context)
    private val fullOpacityText = TextView(context)
    private val miniOpacityText = TextView(context)
    private val sizeText = TextView(context)
    private val projection = CheckBox(context)
    private val debug = CheckBox(context)
    private val projectionState = TextView(context)
    private val panels = ArrayList<View>()

    init {
        orientation = VERTICAL
        setPadding(dp(12), dp(10), dp(12), dp(12))
        background = rounded(TimerColors.PANEL_BG, 18f)
        minimumWidth = dp(320)
        buildHeader()
        buildBody()
        refresh()
    }

    /** Re-read persisted values and update all controls. */
    fun refresh(key: String? = null) {
        syncing = true
        try {
            if ((!initialized || key == Settings.KEY_PLAYER_NAMES) && !names.hasFocus()) {
                names.setText(settings.playerNames.joinToString("，"))
            }
            if ((!initialized || key == Settings.KEY_NINJA_QUERY) && !ninja.hasFocus()) {
                ninja.setText(settings.ninjaQuery)
            }
            sideGroup.check(sideId(settings.playerSide))
            remember.isChecked = settings.rememberSide
            both.isChecked = settings.showBothSides
            modeGroup.check(if (settings.overlayMode == "mini") modeGroup.getChildAt(1).id else modeGroup.getChildAt(0).id)
            setBar(fullOpacity, settings.windowOpacity, 0.40f, 1f)
            setBar(miniOpacity, settings.miniOpacity, 0.20f, 1f)
            setBar(sizeBar, settings.fontScale, 0.55f, 1.20f)
            projection.isChecked = settings.preferProjection
            debug.isChecked = settings.debug
            updateSliderLabels()
            updateProjectionState()
        } finally {
            syncing = false
            initialized = true
        }
    }

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        onConfigChanged?.invoke()
    }

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        if (View.MeasureSpec.getMode(heightMeasureSpec) == View.MeasureSpec.AT_MOST) {
            super.onMeasure(
                widthMeasureSpec,
                View.MeasureSpec.makeMeasureSpec(
                    View.MeasureSpec.getSize(heightMeasureSpec),
                    View.MeasureSpec.EXACTLY,
                ),
            )
        } else {
            super.onMeasure(widthMeasureSpec, heightMeasureSpec)
        }
    }

    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.keyCode == KeyEvent.KEYCODE_BACK) {
            if (event.action == KeyEvent.ACTION_UP && !event.isCanceled) callbacks.onClose()
            return true
        }
        return super.dispatchKeyEvent(event)
    }

    /** Controller may provide a richer health summary than this view can infer. */
    fun setStatus(text: CharSequence) { statusText.text = text }

    private fun buildHeader() {
        val header = LinearLayout(context).apply {
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(4), 0, dp(4), 0)
            background = rounded(0xFF2F78BE.toInt(), 14f)
        }
        val title = label("计时器设置", 17f, true).apply { setTextColor(Color.WHITE) }
        header.addView(title, LinearLayout.LayoutParams(0, dp(44), 1f))
        header.addView(
            button("保存") { save() }.apply { textSize = 12f; setPadding(0, 0, 0, 0) },
            LinearLayout.LayoutParams(dp(56), dp(42)),
        )
        header.addView(
            button("保存并退出") { saveAndClose() }.apply { textSize = 12f; setPadding(0, 0, 0, 0) },
            LinearLayout.LayoutParams(dp(78), dp(42)).apply { leftMargin = dp(4) },
        )
        header.addView(
            button("关闭") { callbacks.onClose() }.apply { textSize = 12f; setPadding(0, 0, 0, 0) },
            LinearLayout.LayoutParams(dp(56), dp(42)).apply { leftMargin = dp(4) },
        )
        addView(header, LinearLayout.LayoutParams(MATCH, WRAP))
    }

    private fun buildBody() {
        body.orientation = HORIZONTAL
        body.gravity = Gravity.TOP
        category.orientation = VERTICAL
        category.background = rounded(0xFF102B3B.toInt(), 14f)
        val labels = listOf("对局", "外观", "位置", "录屏", "状态")
        labels.forEachIndexed { i, text ->
            val item = label(text, 14f, i == 0).apply {
                gravity = Gravity.CENTER
                minHeight = dp(48)
                isClickable = true
                setOnClickListener { showPanel(i) }
            }
            category.addView(item, LinearLayout.LayoutParams(dp(78), dp(52)))
        }
        body.addView(category, LinearLayout.LayoutParams(dp(82), MATCH))
        panelHost.orientation = VERTICAL
        panelHost.background = rounded(0xFF123044.toInt(), 16f)
        body.addView(ScrollView(context).apply {
            isFillViewport = true
            isVerticalScrollBarEnabled = true
            overScrollMode = View.OVER_SCROLL_IF_CONTENT_SCROLLS
            addView(panelHost, ViewGroup.LayoutParams(MATCH, WRAP))
        }, LinearLayout.LayoutParams(0, MATCH, 1f).apply { leftMargin = dp(8) })
        addView(body, LinearLayout.LayoutParams(MATCH, 0, 1f).apply { topMargin = dp(8) })
        panels += buildGamePanel()
        panels += buildAppearancePanel()
        panels += buildPositionPanel()
        panels += buildCapturePanel()
        panels += buildStatusPanel()
        showPanel(0)
    }

    private fun buildGamePanel(): View {
        val p = panel()
        add(p, label("我方所在边", 15f, true))
        sideGroup.orientation = RadioGroup.HORIZONTAL
        listOf("自动", "左", "右").forEachIndexed { i, text ->
            val rb = RadioButton(context).apply { id = View.generateViewId(); this.text = text; minHeight = dp(46); setTextColor(Color.WHITE) }
            sideGroup.addView(rb, RadioGroup.LayoutParams(0, WRAP, 1f))
        }
        sideGroup.setOnCheckedChangeListener { _, id ->
            if (syncing) return@setOnCheckedChangeListener
            session.selectSideMode(when (id) { sideGroup.getChildAt(1).id -> "left"; sideGroup.getChildAt(2).id -> "right"; else -> "auto" })
        }
        add(p, sideGroup, 2)
        remember.text = "记住认边方式"
        remember.setTextColor(Color.WHITE)
        remember.setOnCheckedChangeListener { _, checked -> if (!syncing) session.setRememberSide(checked) }
        add(p, remember, 0)
        both.text = "同时显示两边计时"
        both.setTextColor(Color.WHITE)
        both.setOnCheckedChangeListener { _, checked -> if (!syncing) session.setShowBothSides(checked) }
        add(p, both, 0)
        add(p, label("我的名字", 14f, true), 10)
        names.hint = "账号名（多个用逗号分隔）"
        names.setTextColor(Color.WHITE)
        names.setHintTextColor(TimerColors.TAG_IDLE)
        names.inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE
        names.minLines = 1
        add(p, names, 2)
        add(p, label("对手忍者（可选）", 14f, true), 8)
        ninja.hint = "留空自动识别"
        ninja.setTextColor(Color.WHITE)
        ninja.setHintTextColor(TimerColors.TAG_IDLE)
        ninja.inputType = InputType.TYPE_CLASS_TEXT
        ninja.isSingleLine = true
        add(p, ninja, 2)
        add(p, note("边与两边计时立即生效；名字和忍者名点击顶部保存后生效。"), 8)
        return p
    }

    private fun buildAppearancePanel(): View {
        val p = panel()
        add(p, label("显示模式", 15f, true))
        modeGroup.orientation = RadioGroup.HORIZONTAL
        listOf("完整", "迷你").forEach { text ->
            val rb = RadioButton(context).apply { id = View.generateViewId(); this.text = text; minHeight = dp(46); setTextColor(Color.WHITE) }
            modeGroup.addView(rb, RadioGroup.LayoutParams(0, WRAP, 1f))
        }
        modeGroup.setOnCheckedChangeListener { _, id -> if (!syncing) session.setMiniMode(id == modeGroup.getChildAt(1).id) }
        add(p, modeGroup, 2)
        add(p, fullOpacityText, 8); add(p, fullOpacity, 0)
        add(p, miniOpacityText, 8); add(p, miniOpacity, 0)
        add(p, sizeText, 8); add(p, sizeBar, 0)
        configureBar(fullOpacity, 0.40f, 1f) { settings.windowOpacity = it }
        configureBar(miniOpacity, 0.20f, 1f) { settings.miniOpacity = it }
        configureBar(sizeBar, 0.55f, 1.20f) { settings.fontScale = it }
        return p
    }

    private fun buildPositionPanel(): View {
        val p = panel()
        add(p, label("悬浮窗位置", 15f, true))
        add(p, note("拖动计时卡片即可移动；编辑时面板会暂时收起。"), 6)
        add(p, button("编辑当前位置") { callbacks.onEditPosition() }, 12)
        add(p, button("恢复默认位置") {
            settings.overlayX = Int.MIN_VALUE
            settings.overlayY = Int.MIN_VALUE
            callbacks.onResetPosition()
        }, 6)
        add(p, note("当前位置：" + if (settings.overlayX == Int.MIN_VALUE) "默认" else "${settings.overlayX}, ${settings.overlayY}"), 10)
        return p
    }

    private fun buildCapturePanel(): View {
        val p = panel()
        projection.text = "优先使用录屏采集（MediaProjection）"
        projection.setTextColor(Color.WHITE)
        projection.setOnCheckedChangeListener { _, checked -> if (!syncing) settings.preferProjection = checked }
        add(p, projection)
        projectionState.setTextColor(TimerColors.TAG_IDLE)
        add(p, projectionState, 2)
        add(p, button("授权录屏") { callbacks.onRequestProjection() }, 8)
        add(p, button("停止录屏") { callbacks.onStopProjection(); updateProjectionState() }, 4)
        debug.text = "开启调试日志"
        debug.setTextColor(Color.WHITE)
        debug.setOnCheckedChangeListener { _, checked -> if (!syncing) settings.debug = checked }
        add(p, debug, 12)
        add(p, note("录屏由系统授权对话框确认；关闭后可继续使用无障碍截屏。"), 4)
        return p
    }

    private fun buildStatusPanel(): View {
        val p = panel()
        add(p, label("运行状态", 15f, true))
        statusText.setTextColor(Color.WHITE)
        statusText.text = "悬浮窗运行中"
        statusText.minHeight = dp(100)
        statusText.gravity = Gravity.TOP
        add(p, statusText, 10)
        add(p, button("刷新状态") { updateProjectionState() }, 8)
        return p
    }

    private fun save() {
        applySettings()
        callbacks.onSaved()
    }

    private fun saveAndClose() {
        applySettings()
        callbacks.onSavedAndClose()
    }

    private fun applySettings() {
        val values = names.text.toString().split(',', '，', '\n').map { it.trim() }.filter { it.isNotEmpty() }
        val side = when (sideGroup.checkedRadioButtonId) {
            sideGroup.getChildAt(1).id -> "left"
            sideGroup.getChildAt(2).id -> "right"
            else -> "auto"
        }
        session.applySettings(values, ninja.text.toString(), side, remember.isChecked)
    }

    private fun showPanel(index: Int) {
        panelHost.removeAllViews()
        panelHost.addView(panels[index], LayoutParams(MATCH, WRAP))
        for (i in 0 until category.childCount) {
            val v = category.getChildAt(i) as TextView
            v.setTextColor(if (i == index) TimerColors.CLOCK_LIVE else Color.WHITE)
            v.setTypeface(null, if (i == index) Typeface.BOLD else Typeface.NORMAL)
            v.background = rounded(
                if (i == index) TimerColors.PANEL_HOVER else 0x00102030,
                10f,
            )
        }
        if (index == 2) invalidate()
    }

    private fun updateProjectionState() {
        projectionState.text = if (ProjectionCaptureService.running) "录屏状态：运行中" else "录屏状态：未运行"
    }

    private fun updateSliderLabels() {
        fullOpacityText.text = String.format(Locale.ROOT, "完整窗口透明度：%.0f%%", settings.windowOpacity * 100)
        miniOpacityText.text = String.format(Locale.ROOT, "迷你窗口透明度：%.0f%%", settings.miniOpacity * 100)
        sizeText.text = String.format(Locale.ROOT, "悬浮窗大小：%.0f%%", settings.fontScale * 100)
    }

    private fun configureBar(bar: SeekBar, min: Float, max: Float, onChange: (Float) -> Unit) {
        bar.max = 100
        bar.setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(b: SeekBar, progress: Int, fromUser: Boolean) {
                if (!fromUser) return
                val value = min + (max - min) * progress / 100f
                onChange(value)
                updateSliderLabels()
            }
            override fun onStartTrackingTouch(b: SeekBar) {}
            override fun onStopTrackingTouch(b: SeekBar) {}
        })
    }

    private fun setBar(bar: SeekBar, value: Float, min: Float, max: Float) {
        bar.progress = (((value.coerceIn(min, max) - min) / (max - min)) * 100f).toInt()
    }

    private fun sideId(side: String): Int = when (side) {
        "left" -> sideGroup.getChildAt(1).id
        "right" -> sideGroup.getChildAt(2).id
        else -> sideGroup.getChildAt(0).id
    }

    private fun panel() = LinearLayout(context).apply {
        orientation = VERTICAL
        setPadding(dp(14), dp(14), dp(14), dp(18))
    }

    private fun add(parent: ViewGroup, child: View, top: Int = 6) {
        parent.addView(child, LayoutParams(MATCH, WRAP).apply { topMargin = dp(top) })
    }

    private fun label(text: String, size: Float, bold: Boolean = false) = TextView(context).apply {
        this.text = text
        textSize = size
        setTextColor(Color.WHITE)
        if (bold) setTypeface(typeface, Typeface.BOLD)
        gravity = Gravity.CENTER_VERTICAL
    }

    private fun note(text: String) = label(text, 12f).apply { alpha = 0.72f }

    private fun button(text: String, click: () -> Unit) = Button(context).apply {
        this.text = text
        setTextColor(Color.WHITE)
        setAllCaps(false)
        background = rounded(0xFF2F78BE.toInt(), 8f)
        minHeight = dp(42)
        setOnClickListener { click() }
    }

    private fun rounded(color: Int, radiusDp: Float) = GradientDrawable().apply {
        setColor(color)
        cornerRadius = dp(radiusDp).toFloat()
        setStroke(dp(1), TimerColors.PANEL_BORDER)
    }

    private fun dp(value: Float): Int = (value * density + 0.5f).toInt()
    private fun dp(value: Int): Int = (value * density + 0.5f).toInt()

    private class FramePanel(context: Context) : LinearLayout(context)

    private companion object {
        const val MATCH = ViewGroup.LayoutParams.MATCH_PARENT
        const val WRAP = ViewGroup.LayoutParams.WRAP_CONTENT
    }
}
