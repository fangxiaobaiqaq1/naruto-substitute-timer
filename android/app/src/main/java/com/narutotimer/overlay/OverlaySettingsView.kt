package com.narutotimer.overlay

import android.content.Context
import android.content.res.Configuration
import android.graphics.Typeface
import android.text.InputType
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
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
import com.narutotimer.ui.MainActivity
import com.narutotimer.ui.Ui
import java.util.Locale
import kotlin.math.min
import kotlin.math.roundToInt

/**
 * Settings dialog rendered inside the accessibility overlay window. It uses
 * framework widgets only (no Activity). The caller must pass a context themed
 * with R.style.Theme_NarutoTimer so the widgets are drawn for a dark surface.
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
        fun onEditPosition() {}
        fun onResetPosition() {}
        fun onRequestProjection() {}
        fun onStopProjection() {}
    }

    private var syncing = false
    private var initialized = false
    var onConfigChanged: (() -> Unit)? = null
    private var maxW = Int.MAX_VALUE
    private var maxH = Int.MAX_VALUE

    private val tabs = LinearLayout(context)
    private val content = LinearLayout(context)
    private val contentScroll = ScrollView(context)
    private val panels = ArrayList<View>()
    private val statusText = Ui.text(context, "悬浮窗：运行中", 14f)

    private val sideGroup = RadioGroup(context)
    private val remember = CheckBox(context)
    private val both = CheckBox(context)
    private val names = EditText(context)
    private val ninja = EditText(context)
    private val modeGroup = RadioGroup(context)
    private val fullOpacity = SeekBar(context)
    private val miniOpacity = SeekBar(context)
    private val sizeBar = SeekBar(context)
    private val widthBar = SeekBar(context)
    private val heightBar = SeekBar(context)
    private val fullOpacityText = Ui.text(context, "", 13f, Ui.TEXT_2)
    private val miniOpacityText = Ui.text(context, "", 13f, Ui.TEXT_2)
    private val sizeText = Ui.text(context, "", 13f, Ui.TEXT_2)
    private val widthText = Ui.text(context, "", 13f, Ui.TEXT_2)
    private val heightText = Ui.text(context, "", 13f, Ui.TEXT_2)
    private val positionText = Ui.note(context, "")
    private val projection = CheckBox(context)
    private val debug = CheckBox(context)
    private val projectionState = Ui.text(context, "", 13f, Ui.TEXT_2)

    init {
        orientation = VERTICAL
        setPadding(dp(14f), dp(10f), dp(14f), dp(14f))
        background = Ui.rounded(context, Ui.SURFACE, 20f, Ui.OUTLINE)
        elevation = dp(8f).toFloat()
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
            sideGroup.check(sideGroup.getChildAt(SIDES.indexOf(settings.playerSide).coerceAtLeast(0)).id)
            remember.isChecked = settings.rememberSide
            both.isChecked = settings.showBothSides
            modeGroup.check(modeGroup.getChildAt(if (settings.overlayMode == "mini") 1 else 0).id)
            setBar(fullOpacity, settings.windowOpacity, FULL_OPACITY_MIN, OPACITY_STEP)
            setBar(miniOpacity, settings.miniOpacity, MINI_OPACITY_MIN, OPACITY_STEP)
            setBar(sizeBar, settings.fontScale, Settings.FONT_SCALE_MIN, Settings.FONT_SCALE_STEP)
            setBar(widthBar, settings.cardWidth, 1f, Settings.CARD_STRETCH_STEP)
            setBar(heightBar, settings.cardHeight, 1f, Settings.CARD_STRETCH_STEP)
            projection.isChecked = settings.preferProjection
            debug.isChecked = settings.debug
            updateLabels()
        } finally {
            syncing = false
            initialized = true
        }
    }

    /** Caps the dialog to the safe display area supplied by the controller. */
    fun setMaxSize(width: Int, height: Int) {
        maxW = width - dp(24f)
        maxH = height - dp(16f)
        requestLayout()
    }

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        // A fixed-size dialog: switching tabs never makes it jump.
        val w = min(min(maxW, MeasureSpec.getSize(widthMeasureSpec)), dp(MAX_WIDTH_DP))
        val h = min(min(maxH, MeasureSpec.getSize(heightMeasureSpec)), dp(MAX_HEIGHT_DP))
        super.onMeasure(
            MeasureSpec.makeMeasureSpec(w.coerceAtLeast(dp(260f)), MeasureSpec.EXACTLY),
            MeasureSpec.makeMeasureSpec(h.coerceAtLeast(dp(200f)), MeasureSpec.EXACTLY),
        )
    }

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        onConfigChanged?.invoke()
    }

    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.keyCode == KeyEvent.KEYCODE_BACK) {
            if (event.action == KeyEvent.ACTION_UP && !event.isCanceled) callbacks.onClose()
            return true
        }
        return super.dispatchKeyEvent(event)
    }

    /** Controller supplies the live health summary. */
    fun setStatus(text: CharSequence) {
        if (statusText.text.toString() != text.toString()) statusText.text = text
        updateProjectionState()
    }

    private fun buildHeader() {
        val header = LinearLayout(context).apply { gravity = Gravity.CENTER_VERTICAL }
        header.addView(Ui.text(context, "计时器设置", 17f, Ui.TEXT, bold = true), LayoutParams(0, WRAP, 1f))
        header.addView(Ui.button(context, "关闭", Ui.Kind.TONAL) { callbacks.onClose() }, LayoutParams(WRAP, WRAP))
        header.addView(
            Ui.button(context, "保存", Ui.Kind.PRIMARY) { save() },
            LayoutParams(WRAP, WRAP).apply { marginStart = dp(8f) },
        )
        addView(header, LayoutParams(MATCH, WRAP))
    }

    private fun buildBody() {
        val body = LinearLayout(context).apply { orientation = HORIZONTAL }
        tabs.orientation = VERTICAL
        TAB_LABELS.forEachIndexed { i, text ->
            val item = Ui.text(context, text, 14f).apply {
                gravity = Gravity.CENTER
                minHeight = dp(44f)
                isClickable = true
                isFocusable = true
                setOnClickListener { showPanel(i) }
            }
            tabs.addView(item, LayoutParams(MATCH, WRAP).apply { if (i > 0) topMargin = dp(4f) })
        }
        // The rail scrolls too: five tabs do not fit a short landscape screen.
        body.addView(ScrollView(context).apply {
            isVerticalScrollBarEnabled = false
            addView(tabs, ViewGroup.LayoutParams(MATCH, WRAP))
        }, LayoutParams(dp(76f), MATCH))
        content.orientation = VERTICAL
        content.setPadding(dp(14f), dp(4f), dp(4f), dp(12f))
        contentScroll.isFillViewport = true
        contentScroll.addView(content, ViewGroup.LayoutParams(MATCH, WRAP))
        body.addView(contentScroll, LayoutParams(0, MATCH, 1f).apply { marginStart = dp(8f) })
        addView(body, LayoutParams(MATCH, 0, 1f).apply { topMargin = dp(10f) })
        panels += buildGamePanel()
        panels += buildAppearancePanel()
        panels += buildPositionPanel()
        panels += buildCapturePanel()
        panels += buildStatusPanel()
        showPanel(0)
    }

    private fun radio(group: RadioGroup, labels: List<String>) {
        group.orientation = RadioGroup.HORIZONTAL
        labels.forEach { text ->
            val rb = RadioButton(context).apply {
                id = View.generateViewId()
                this.text = text
                minHeight = dp(44f)
                setTextColor(Ui.TEXT)
            }
            group.addView(rb, RadioGroup.LayoutParams(0, WRAP, 1f))
        }
    }

    private fun buildGamePanel(): View {
        val p = panel()
        Ui.add(p, Ui.title(context, "我方位置"), 0f)
        radio(sideGroup, listOf("自动", "左边", "右边"))
        sideGroup.setOnCheckedChangeListener { group, id ->
            if (syncing) return@setOnCheckedChangeListener
            session.selectSideMode(SIDES[group.indexOfChild(group.findViewById(id)).coerceAtLeast(0)])
        }
        Ui.add(p, sideGroup, 2f)
        remember.text = "记住认边方式"
        remember.setOnCheckedChangeListener { _, checked -> if (!syncing) session.setRememberSide(checked) }
        Ui.add(p, remember, 0f)
        both.text = "同时显示两边计时"
        both.setOnCheckedChangeListener { _, checked -> if (!syncing) session.setShowBothSides(checked) }
        Ui.add(p, both, 0f)
        Ui.add(p, Ui.title(context, "我的名字"), 12f)
        names.hint = "账号名，多个用逗号分隔"
        names.inputType = InputType.TYPE_CLASS_TEXT
        names.isSingleLine = true
        Ui.add(p, names, 2f)
        Ui.add(p, Ui.title(context, "对面忍者（可选）"), 10f)
        ninja.hint = "留空自动识别"
        ninja.inputType = InputType.TYPE_CLASS_TEXT
        ninja.isSingleLine = true
        Ui.add(p, ninja, 2f)
        Ui.add(p, Ui.note(context, "名字和忍者点「保存」生效；其余选项立即生效。左/右指我方，计时的是对面。"), 8f)
        return p
    }

    private fun buildAppearancePanel(): View {
        val p = panel()
        Ui.add(p, Ui.title(context, "显示模式"), 0f)
        radio(modeGroup, listOf("完整卡片", "迷你"))
        modeGroup.setOnCheckedChangeListener { group, id ->
            if (!syncing) session.setMiniMode(group.indexOfChild(group.findViewById(id)) == 1)
        }
        Ui.add(p, modeGroup, 2f)
        Ui.add(p, fullOpacityText, 10f); Ui.add(p, fullOpacity, 0f)
        Ui.add(p, miniOpacityText, 8f); Ui.add(p, miniOpacity, 0f)
        Ui.add(p, sizeText, 8f); Ui.add(p, sizeBar, 0f)
        Ui.add(p, widthText, 8f); Ui.add(p, widthBar, 0f)
        Ui.add(p, heightText, 8f); Ui.add(p, heightBar, 0f)
        configureBar(fullOpacity, FULL_OPACITY_MIN, 1f, OPACITY_STEP) { settings.windowOpacity = it }
        configureBar(miniOpacity, MINI_OPACITY_MIN, 1f, OPACITY_STEP) { settings.miniOpacity = it }
        configureBar(sizeBar, Settings.FONT_SCALE_MIN, Settings.FONT_SCALE_MAX, Settings.FONT_SCALE_STEP) { settings.fontScale = it }
        configureBar(widthBar, 1f, Settings.CARD_STRETCH_MAX, Settings.CARD_STRETCH_STEP) { settings.cardWidth = it }
        configureBar(heightBar, 1f, Settings.CARD_STRETCH_MAX, Settings.CARD_STRETCH_STEP) { settings.cardHeight = it }
        Ui.add(p, Ui.button(context, "恢复默认外观") {
            settings.windowOpacity = 1f
            settings.miniOpacity = DEFAULT_MINI_OPACITY
            settings.fontScale = Settings.DEFAULT_FONT_SCALE
            settings.cardWidth = 1f
            settings.cardHeight = 1f
            refresh()
        }, 12f)
        return p
    }

    private fun buildPositionPanel(): View {
        val p = panel()
        Ui.add(p, Ui.title(context, "悬浮窗位置"), 0f)
        Ui.add(p, Ui.note(context, "按住计时卡片直接拖动即可移动。「调整位置」会高亮卡片，方便在游戏画面里找到它。"), 4f)
        Ui.add(p, Ui.row(
            context,
            Ui.button(context, "调整位置", Ui.Kind.PRIMARY) { callbacks.onEditPosition() },
            Ui.button(context, "恢复默认位置") {
                settings.overlayX = Int.MIN_VALUE
                settings.overlayY = Int.MIN_VALUE
                callbacks.onResetPosition()
                updateLabels()
            },
        ), 12f)
        Ui.add(p, positionText, 10f)
        return p
    }

    private fun buildCapturePanel(): View {
        val p = panel()
        Ui.add(p, Ui.title(context, "录屏采集"), 0f)
        Ui.add(p, projectionState, 4f)
        Ui.add(p, Ui.row(
            context,
            Ui.button(context, "授权录屏", Ui.Kind.PRIMARY) { callbacks.onRequestProjection() },
            Ui.button(context, "停止录屏") { callbacks.onStopProjection(); updateProjectionState() },
        ), 10f)
        projection.text = "优先使用录屏采集（MediaProjection）"
        projection.setOnCheckedChangeListener { _, checked -> if (!syncing) settings.preferProjection = checked }
        Ui.add(p, projection, 10f)
        debug.text = "输出调试日志"
        debug.setOnCheckedChangeListener { _, checked -> if (!syncing) settings.debug = checked }
        Ui.add(p, debug, 0f)
        Ui.add(p, Ui.note(context, "录屏约 60 帧/秒，需在系统对话框确认；关闭后自动改用无障碍截屏（约 3 帧/秒）。"), 6f)
        return p
    }

    private fun buildStatusPanel(): View {
        val p = panel()
        Ui.add(p, Ui.title(context, "运行状态"), 0f)
        Ui.add(p, statusText, 8f)
        return p
    }

    private fun save() {
        val side = SIDES[sideGroup.indexOfChild(sideGroup.findViewById(sideGroup.checkedRadioButtonId)).coerceAtLeast(0)]
        // Same parsing as the main screen: commas, 顿号, spaces, normalized and de-duplicated.
        session.applySettings(MainActivity.splitNames(names.text.toString()), ninja.text.toString(), side, remember.isChecked)
        callbacks.onSaved()
        callbacks.onClose()
    }

    private fun showPanel(index: Int) {
        content.removeAllViews()
        content.addView(panels[index], LayoutParams(MATCH, WRAP))
        contentScroll.scrollTo(0, 0)
        for (i in 0 until tabs.childCount) {
            val v = tabs.getChildAt(i) as TextView
            val selected = i == index
            v.setTextColor(if (selected) Ui.ON_ACCENT else Ui.TEXT_2)
            v.typeface = Typeface.create(Typeface.DEFAULT, if (selected) 600 else 400, false)
            v.background = if (selected) Ui.rounded(context, Ui.ACCENT, 12f) else null
        }
        if (index == 2) updateLabels()
    }

    private fun updateProjectionState() {
        projectionState.text = if (ProjectionCaptureService.running) "状态：录屏运行中" else "状态：未运行（使用无障碍截屏）"
    }

    private fun updateLabels() {
        fullOpacityText.text = String.format(Locale.ROOT, "完整卡片不透明度：%.0f%%", settings.windowOpacity * 100)
        miniOpacityText.text = String.format(Locale.ROOT, "迷你卡片不透明度：%.0f%%", settings.miniOpacity * 100)
        sizeText.text = String.format(Locale.ROOT, "悬浮窗大小：%.0f%%", settings.fontScale * 100)
        widthText.text = String.format(Locale.ROOT, "卡片宽度：%.0f%%", settings.cardWidth * 100)
        heightText.text = String.format(Locale.ROOT, "卡片高度：%.0f%%", settings.cardHeight * 100)
        positionText.text = "当前位置：" + if (settings.overlayX == Int.MIN_VALUE) "默认（屏幕上方居中）" else "${settings.overlayX}, ${settings.overlayY}"
        updateProjectionState()
    }

    private fun configureBar(bar: SeekBar, min: Float, max: Float, step: Float, onChange: (Float) -> Unit) {
        bar.max = ((max - min) / step).roundToInt().coerceAtLeast(1)
        bar.setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(b: SeekBar, progress: Int, fromUser: Boolean) {
                if (syncing || !fromUser) return
                onChange(min + progress * step)
                updateLabels()
            }
            override fun onStartTrackingTouch(b: SeekBar) {}
            override fun onStopTrackingTouch(b: SeekBar) {}
        })
    }

    private fun setBar(bar: SeekBar, value: Float, min: Float, step: Float) {
        bar.progress = ((value - min) / step).roundToInt().coerceIn(0, bar.max)
    }

    private fun panel() = LinearLayout(context).apply { orientation = VERTICAL }

    private fun dp(value: Float): Int = Ui.dp(context, value)

    private companion object {
        const val MATCH = ViewGroup.LayoutParams.MATCH_PARENT
        const val WRAP = ViewGroup.LayoutParams.WRAP_CONTENT
        const val MAX_WIDTH_DP = 600f
        const val MAX_HEIGHT_DP = 460f
        const val FULL_OPACITY_MIN = 0.40f
        const val MINI_OPACITY_MIN = 0.20f
        const val OPACITY_STEP = 0.05f
        const val DEFAULT_MINI_OPACITY = 0.85f
        val SIDES = listOf("auto", "left", "right")
        val TAB_LABELS = listOf("对局", "外观", "位置", "录屏", "状态")
    }
}
