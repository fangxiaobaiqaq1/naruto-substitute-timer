package com.narutotimer.ui

// Owner: kt-overlay-ui
// Port of: internal/ui/settings.go openSettings / applySettings / sideMode / sideLabel，
//          internal/ui/run.go splitNames，internal/ui/mini.go（迷你不透明度）、internal/ui/appearance.go
//          （窗口不透明度/字号的取值范围与步进）。
//
// 主界面（纯代码布局，无 AndroidX）：
//  - 「开始使用」三步：无障碍服务 → 悬浮计时 → 录屏识别（推荐），每步一个状态点和一个按钮。
//  - 「实时状态」：对局/认边、识别循环、资源、录屏免确认，每秒刷新。
//  - 「对局设置」（Go openSettings）：认边方式/记住认边点选即生效；账号名与对面忍者在"保存设置"时
//    经 session.applySettings 生效。回到前台时只回填用户没有改动过的输入框。
//  - 「高级设置」：悬浮窗外观、采集与调试、区域校准、在线更新、电脑 ADB、账号名图片导入。
// 不移植：窗口置顶、OCR、GPU、诊断录制、真悬浮窗开关（Android 悬浮窗始终是真悬浮窗）。

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.app.AppOpsManager
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Typeface
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.Process
import android.provider.Settings as SystemSettings
import android.text.InputType
import android.util.Log
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.SeekBar
import android.widget.TextView
import android.widget.Toast
import com.narutotimer.AssetLoader
import com.narutotimer.BuildConfig
import com.narutotimer.Housekeeping
import com.narutotimer.IdentityStore
import com.narutotimer.NativeCore
import com.narutotimer.R
import com.narutotimer.Settings
import com.narutotimer.TimerApp
import com.narutotimer.capture.FrameLoop
import com.narutotimer.capture.ProjectionCaptureService
import com.narutotimer.service.TimerAccessibilityService
import com.narutotimer.tracker.GoText
import com.narutotimer.tracker.NinjaRules
import com.narutotimer.tracker.Session
import com.narutotimer.update.UpdateManager
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.math.roundToInt

class MainActivity : Activity() {
    private lateinit var app: TimerApp
    private lateinit var settings: Settings
    private lateinit var session: Session
    private val main = Handler(Looper.getMainLooper())

    /** One setup step: status dot, title, live detail and its contextual action. */
    private class Step(val dot: View, val detail: TextView, val action: Button) {
        var onAction: () -> Unit = {}
        var primary = true
    }

    private lateinit var accessibilityStep: Step
    private lateinit var overlayStep: Step
    private lateinit var projectionStep: Step

    // 状态区。
    private lateinit var matchText: TextView
    private lateinit var loopText: TextView
    private lateinit var nativeText: TextView
    private lateinit var appOpsText: TextView
    private lateinit var restartOverlayBtn: Button
    private lateinit var positionBtn: Button

    // 设置区。
    private lateinit var sideGroup: RadioGroup
    private lateinit var rememberChk: CheckBox
    private lateinit var namesEdit: EditText
    private lateinit var ninjaEdit: EditText
    private lateinit var modeGroup: RadioGroup
    private lateinit var bothChk: CheckBox
    private lateinit var miniOpacityText: TextView
    private lateinit var miniOpacityBar: SeekBar
    private lateinit var opacityText: TextView
    private lateinit var opacityBar: SeekBar
    private lateinit var fontScaleText: TextView
    private lateinit var fontScaleBar: SeekBar
    private lateinit var cardSizeText: TextView
    private lateinit var cardWidthBar: SeekBar
    private lateinit var cardHeightBar: SeekBar
    private lateinit var projectionChk: CheckBox
    private lateinit var debugChk: CheckBox
    private lateinit var updateSourceGroup: RadioGroup
    private lateinit var autoUpdateChk: CheckBox
    private lateinit var identityText: TextView
    private lateinit var identityLabelEdit: EditText
    private lateinit var calibText: TextView
    private lateinit var calibResetBtn: Button

    private val sideIds = IntArray(3)
    private val modeIds = IntArray(2)

    /** 程序化回填控件时为 true：控件回调不得再写回 Session/Settings。 */
    private var syncing = false
    private var resumed = false
    /** Values last written into the text fields; a different value means the user is typing. */
    private var loadedNames: String? = null
    private var loadedNinja: String? = null

    private val statusTick = object : Runnable {
        override fun run() {
            refreshStatus()
            if (resumed) main.postDelayed(this, STATUS_INTERVAL_MS)
        }
    }

    private val nativeListener: () -> Unit = {
        if (resumed) {
            refreshStatus()
            refreshIdentityCount() // A load prunes identity/seen to its cap.
        }
    }

    /** 悬浮窗按钮（换边/两边计时/迷你）或其它入口修改设置时同步控件（Go syncSettingsSide）。 */
    private val settingsListener: (String) -> Unit = { key ->
        main.post { if (resumed) onSettingChanged(key) }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        app = application as TimerApp
        settings = app.settings
        session = app.session
        pendingIdentityLabel = savedInstanceState?.getString(STATE_IDENTITY_LABEL).orEmpty()
        title = getString(R.string.app_name)
        setContentView(buildContent())
        // Android 13+: without this the recognition service's notification (and
        // its stop button) is silently hidden.
        if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQUEST_NOTIFICATIONS)
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putString(STATE_IDENTITY_LABEL, pendingIdentityLabel)
    }

    override fun onResume() {
        super.onResume()
        resumed = true
        settings.addListener(settingsListener)
        app.addNativeListener(nativeListener)
        loadFromSettings()
        main.removeCallbacks(statusTick)
        statusTick.run()
        UpdateManager.resumePendingInstall(this)
        if (!app.autoUpdateChecked && settings.autoCheckUpdates) {
            app.autoUpdateChecked = true
            UpdateManager.check(this, settings, quiet = true)
        }
    }

    override fun onPause() {
        super.onPause()
        resumed = false
        main.removeCallbacks(statusTick)
        settings.removeListener(settingsListener)
        app.removeNativeListener(nativeListener)
    }

    // ---- 布局 ----

    private fun buildContent(): View {
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(18), dp(16), dp(32))
        }

        // 头部：应用名 + 版本。
        val titleRow = LinearLayout(this).apply { gravity = Gravity.CENTER_VERTICAL }
        titleRow.addView(Ui.text(this, getString(R.string.app_name), 24f, Ui.TEXT, bold = true),
            LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        titleRow.addView(Ui.text(this, "v" + BuildConfig.VERSION_NAME, 12f, Ui.ACCENT).apply {
            background = Ui.rounded(this@MainActivity, Ui.SURFACE_2, 10f)
            setPadding(dp(10), dp(4), dp(10), dp(4))
        })
        Ui.add(content, titleRow, 0f)
        Ui.add(content, Ui.note(this, "火影忍者手游 · 对面替身冷却悬浮计时"), 2f)

        // 开始使用：每一步一个状态点、一句状态和一个按钮。
        val setup = Ui.card(this)
        Ui.add(setup, Ui.title(this, "开始使用"), 0f)
        accessibilityStep = step(setup, "1", "无障碍服务", "悬浮窗和备用截屏都需要它")
        overlayStep = step(setup, "2", "悬浮计时", "在游戏画面上显示替身冷却")
        projectionStep = step(setup, "3", "录屏识别（推荐）", "约 60 帧/秒；不开启时用无障碍截屏")
        restartOverlayBtn = Ui.button(this, "重启悬浮窗") {
            TimerAccessibilityService.instance?.restartOverlay() ?: toast("请先开启并连接无障碍服务")
            main.postDelayed({ refreshStatus() }, 500)
        }
        positionBtn = Ui.button(this, "调整位置") {
            TimerAccessibilityService.instance?.beginPositionEdit() ?: toast("请先开启并连接无障碍服务")
            if (TimerAccessibilityService.instance?.isOverlayRunning == true) toast("回到游戏后拖动计时卡片，点击卡片完成")
        }
        Ui.add(setup, Ui.row(this, restartOverlayBtn, positionBtn, Ui.button(this, "权限指引") { showPermissionGuide() }), 14f)
        Ui.add(content, setup, 16f)

        // 实时状态。
        val status = Ui.card(this)
        Ui.add(status, Ui.title(this, "实时状态"), 0f)
        matchText = Ui.text(this, "", 15f, Ui.TEXT, bold = true).also { Ui.add(status, it, 8f) }
        loopText = Ui.text(this, "", 13f, Ui.TEXT_2).also { Ui.add(status, it, 4f) }
        nativeText = Ui.text(this, "", 13f, Ui.TEXT_2).also { Ui.add(status, it, 4f) }
        appOpsText = Ui.text(this, "", 13f, Ui.TEXT_2).also { Ui.add(status, it, 4f) }
        Ui.add(content, status, 12f)

        // 对局设置：最常用的设置不折叠。
        val basics = Ui.card(this)
        Ui.add(basics, Ui.title(this, "对局设置"), 0f)
        Ui.add(basics, Ui.text(this, "我方所在边", 13f, Ui.TEXT_2), 10f)
        sideGroup = RadioGroup(this).apply { orientation = RadioGroup.HORIZONTAL }
        SIDE_LABELS.forEachIndexed { i, label ->
            val rb = radio(label)
            sideIds[i] = rb.id
            sideGroup.addView(rb, RadioGroup.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        }
        sideGroup.setOnCheckedChangeListener { _, checkedId ->
            if (syncing) return@setOnCheckedChangeListener
            val i = sideIds.indexOf(checkedId)
            if (i >= 0) session.selectSideMode(sideMode(SIDE_LABELS[i]))
            refreshStatus()
        }
        Ui.add(basics, sideGroup, 0f)
        rememberChk = check("记住认边方式（下次启动沿用）") { on -> session.setRememberSide(on) }
        Ui.add(basics, rememberChk, 0f)
        Ui.add(basics, Ui.note(this, "选的是我方；悬浮窗计时的是另一边。自动认边未完成时显示“待认边”。"), 2f)

        Ui.add(basics, Ui.text(this, "我的名字", 13f, Ui.TEXT_2), 14f)
        namesEdit = EditText(this).apply {
            hint = "账号名（多个用逗号分隔）"
            inputType = InputType.TYPE_CLASS_TEXT
            isSingleLine = true
        }
        Ui.add(basics, namesEdit, 0f)
        Ui.add(basics, Ui.note(this, "填好就能自动认边：对局中本机读取血条上方「忍者名(账号)」里的账号（不联网、不上传），与这里的名字逐字相同就认出我方，" +
            "账号末尾被支援忍者头像挡住时看得见的前半段也算。游戏里的装饰符号（如 ღ）可照抄也可省略。不需要导入图片；读不出时可在上面选左边/右边。"), 2f)
        Ui.add(basics, Ui.text(this, "对面忍者（可选）", 13f, Ui.TEXT_2), 10f)
        ninjaEdit = EditText(this).apply {
            hint = "留空自动识别"
            inputType = InputType.TYPE_CLASS_TEXT
            isSingleLine = true
        }
        val ninjaRow = LinearLayout(this).apply { gravity = Gravity.CENTER_VERTICAL }
        ninjaRow.addView(ninjaEdit, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        ninjaRow.addView(Ui.button(this, "选择") { pickNinja() },
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply { marginStart = dp(8) })
        Ui.add(basics, ninjaRow, 0f)
        Ui.add(basics, Ui.button(this, getString(R.string.action_save), Ui.Kind.PRIMARY) { save() }, 14f)
        Ui.add(content, basics, 12f)

        // 高级内容默认收起，保留全部既有控件和 adb/文件选择功能。
        val advanced = Ui.card(this)
        lateinit var advancedPanel: LinearLayout
        val advancedToggle = Ui.text(this, "高级设置", 16f, Ui.TEXT, bold = true).apply {
            gravity = Gravity.CENTER_VERTICAL
            minHeight = dp(44)
            isClickable = true
            isFocusable = true
            setOnClickListener {
                val open = advancedPanel.visibility != View.VISIBLE
                advancedPanel.visibility = if (open) View.VISIBLE else View.GONE
                text = if (open) "高级设置  ▾" else "高级设置  ▸"
            }
            text = "高级设置  ▸"
        }
        Ui.add(advanced, advancedToggle, 0f)
        advancedPanel = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            visibility = View.GONE
        }
        Ui.add(advanced, advancedPanel, 0f)

        section(advancedPanel, "悬浮窗外观", 6f)
        modeGroup = RadioGroup(this).apply { orientation = RadioGroup.HORIZONTAL }
        MODE_LABELS.forEachIndexed { i, label ->
            val rb = radio(label)
            modeIds[i] = rb.id
            modeGroup.addView(rb, RadioGroup.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        }
        modeGroup.setOnCheckedChangeListener { _, checkedId ->
            if (syncing) return@setOnCheckedChangeListener
            val on = checkedId == modeIds[1]
            if (on != (settings.overlayMode == "mini")) session.setMiniMode(on)
        }
        Ui.add(advancedPanel, modeGroup, 0f)
        bothChk = check("左右两边都显示替身倒计时") { on -> session.setShowBothSides(on) }
        Ui.add(advancedPanel, bothChk, 0f)
        opacityText = Ui.text(this, "", 13f, Ui.TEXT_2).also { Ui.add(advancedPanel, it, 8f) }
        opacityBar = slider(WINDOW_OPACITY_MIN, WINDOW_OPACITY_MAX, OPACITY_STEP) { v ->
            settings.windowOpacity = v.toFloat()
            updateAppearanceLabels()
        }
        Ui.add(advancedPanel, opacityBar, 0f)
        miniOpacityText = Ui.text(this, "", 13f, Ui.TEXT_2).also { Ui.add(advancedPanel, it, 6f) }
        miniOpacityBar = slider(MINI_OPACITY_MIN, MINI_OPACITY_MAX, OPACITY_STEP) { v ->
            settings.miniOpacity = v.toFloat()
            updateAppearanceLabels()
        }
        Ui.add(advancedPanel, miniOpacityBar, 0f)
        fontScaleText = Ui.text(this, "", 13f, Ui.TEXT_2).also { Ui.add(advancedPanel, it, 6f) }
        fontScaleBar = slider(
            Settings.FONT_SCALE_MIN.toDouble(),
            Settings.FONT_SCALE_MAX.toDouble(),
            Settings.FONT_SCALE_STEP.toDouble(),
        ) { v ->
            settings.fontScale = v.toFloat()
            updateAppearanceLabels()
        }
        Ui.add(advancedPanel, fontScaleBar, 0f)
        cardSizeText = Ui.text(this, "", 13f, Ui.TEXT_2).also { Ui.add(advancedPanel, it, 6f) }
        cardWidthBar = slider(1.0, Settings.CARD_STRETCH_MAX.toDouble(), Settings.CARD_STRETCH_STEP.toDouble()) { v ->
            settings.cardWidth = v.toFloat()
            updateAppearanceLabels()
        }
        cardHeightBar = slider(1.0, Settings.CARD_STRETCH_MAX.toDouble(), Settings.CARD_STRETCH_STEP.toDouble()) { v ->
            settings.cardHeight = v.toFloat()
            updateAppearanceLabels()
        }
        Ui.add(advancedPanel, cardWidthBar, 0f)
        Ui.add(advancedPanel, cardHeightBar, 0f)
        Ui.add(advancedPanel, Ui.row(
            this,
            Ui.button(this, "默认外观") {
                settings.windowOpacity = 1f
                settings.miniOpacity = DEFAULT_MINI_OPACITY.toFloat()
                settings.fontScale = Settings.DEFAULT_FONT_SCALE
                settings.cardWidth = 1f
                settings.cardHeight = 1f
                loadFromSettings()
            },
            Ui.button(this, "默认位置") {
                settings.overlayX = Int.MIN_VALUE
                settings.overlayY = Int.MIN_VALUE
                toast("悬浮窗已回到默认位置")
            },
        ), 8f)

        section(advancedPanel, "采集与调试", 18f)
        projectionChk = check("优先使用录屏采集") { on -> settings.preferProjection = on }
        debugChk = check("输出调试日志") { on -> settings.debug = on }
        Ui.add(advancedPanel, projectionChk, 0f)
        Ui.add(advancedPanel, debugChk, 0f)

        section(advancedPanel, "区域校准（可选）", 18f)
        Ui.add(advancedPanel, Ui.note(this, "平板、带鱼屏或模拟器窗口等少见分辨率下，豆或头像位置识别不准时，可在一帧游戏画面上手动框出。"), 2f)
        calibText = Ui.text(this, "", 13f, Ui.TEXT_2).also { Ui.add(advancedPanel, it, 4f) }
        calibResetBtn = Ui.button(this, "恢复自动", Ui.Kind.DANGER) { confirmResetCalibration() }
        Ui.add(advancedPanel, Ui.row(this, Ui.button(this, "区域校准") { showCalibrationGuide() }, calibResetBtn), 6f)

        section(advancedPanel, "在线更新", 18f)
        Ui.add(advancedPanel, Ui.note(this, "可选 Gitee（国内）或 GitHub（海外）。只下载 HTTPS、带 SHA-256 校验的正式 APK。"), 2f)
        updateSourceGroup = RadioGroup(this).apply { orientation = RadioGroup.HORIZONTAL }
        val gitee = radio("Gitee（国内）")
        val github = radio("GitHub（海外）")
        updateSourceGroup.addView(gitee, RadioGroup.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        updateSourceGroup.addView(github, RadioGroup.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        updateSourceGroup.setOnCheckedChangeListener { _, id ->
            if (!syncing) settings.updateSource = if (id == github.id) "github" else "gitee"
        }
        Ui.add(advancedPanel, updateSourceGroup, 0f)
        autoUpdateChk = check("启动应用时自动检查更新") { on -> settings.autoCheckUpdates = on }
        Ui.add(advancedPanel, autoUpdateChk, 0f)
        Ui.add(advancedPanel, Ui.button(this, "检查更新") { UpdateManager.check(this, settings) }, 4f)

        section(advancedPanel, "电脑 ADB（可选）", 18f)
        Ui.add(advancedPanel, Ui.note(this, "仅在需要免确认录屏或命令开启无障碍时使用。第二条会替换已开启的其它无障碍服务列表。"), 2f)
        Ui.add(advancedPanel, command(appOpsCommand()), 6f)
        Ui.add(advancedPanel, command(accessibilityCommand()), 6f)

        section(advancedPanel, "账号名图片（可选）", 18f)
        Ui.add(advancedPanel, Ui.note(
            this,
            "一般不需要：填好「我的名字」后，对局中会用本机文字识别读血条上方的账号自动认边。" +
                "图片只是补充，可以在对战加载（VS）画面就认出我方：① 在 VS 画面用这台手机截屏；② 裁下画面底部自己账号名那一行字" +
                "（贴着文字裁，不带头像和段位，不要缩放）；③ 下面填与「我的名字」完全相同的账号名，点「导入图片」。" +
                "导入时会按本机屏幕换算成识别用的尺寸。认出我方后会自动收集对面的账号名图片（只保留最近 ${IdentityStore.SEEN_CAP} 张，可随时清除）。",
        ), 2f)
        identityLabelEdit = EditText(this).apply {
            hint = "图片对应的账号名"
            inputType = InputType.TYPE_CLASS_TEXT
            isSingleLine = true
        }
        Ui.add(advancedPanel, identityLabelEdit, 2f)
        identityText = Ui.text(this, "", 13f, Ui.TEXT_2).also { Ui.add(advancedPanel, it, 4f) }
        Ui.add(advancedPanel, Ui.row(
            this,
            Ui.button(this, "导入图片") { pickIdentityImages() },
            Ui.button(this, "删除已导入", Ui.Kind.DANGER) { confirmDeleteIdentity() },
        ), 6f)
        Ui.add(advancedPanel, Ui.button(this, "清除自动收集", Ui.Kind.DANGER) { confirmClearSeen() }, 6f)
        Ui.add(content, advanced, 12f)

        return ScrollView(this).apply {
            isFillViewport = true
            isVerticalScrollBarEnabled = true
            setBackgroundColor(Ui.BG)
            addView(content, ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ))
        }
    }

    private fun step(parent: LinearLayout, number: String, title: String, hint: String): Step {
        val row = LinearLayout(this).apply {
            gravity = Gravity.CENTER_VERTICAL
            minimumHeight = dp(56)
        }
        val badge = Ui.text(this, number, 13f, Ui.TEXT_2, bold = true).apply {
            gravity = Gravity.CENTER
            background = Ui.rounded(this@MainActivity, Ui.SURFACE_2, 14f)
        }
        row.addView(badge, LinearLayout.LayoutParams(dp(28), dp(28)))
        val texts = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val titleRow = LinearLayout(this).apply { gravity = Gravity.CENTER_VERTICAL }
        val dot = Ui.dot(this)
        titleRow.addView(Ui.text(this, title, 15f, Ui.TEXT, bold = true))
        titleRow.addView(dot, LinearLayout.LayoutParams(dp(8), dp(8)).apply { marginStart = dp(8) })
        texts.addView(titleRow)
        val detail = Ui.text(this, hint, 12.5f, Ui.TEXT_2)
        texts.addView(detail)
        row.addView(texts, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
            marginStart = dp(12)
            marginEnd = dp(8)
        })
        lateinit var step: Step
        val action = Ui.button(this, "开启", Ui.Kind.PRIMARY) { step.onAction() }
        row.addView(action, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        step = Step(dot, detail, action)
        Ui.add(parent, row, 10f)
        return step
    }

    private fun setStep(s: Step, color: Int, detail: String, label: String, primary: Boolean, enabled: Boolean = true, action: () -> Unit) {
        Ui.setDot(s.dot, color)
        s.detail.text = detail
        s.action.text = label
        s.action.isEnabled = enabled
        if (s.primary != primary) {
            Ui.style(s.action, if (primary) Ui.Kind.PRIMARY else Ui.Kind.TONAL)
            s.primary = primary
        }
        s.onAction = action
    }

    private fun section(parent: LinearLayout, title: String, topDp: Float) {
        Ui.add(parent, Ui.text(this, title, 14f, Ui.ACCENT, bold = true), topDp)
    }

    // ---- 回填（Go openSettings：每次按当前状态重建） ----

    private fun loadFromSettings() {
        syncing = true
        try {
            // Keep what the user is typing (e.g. after returning from the consent
            // dialog or file picker); refill only fields that still show old values.
            val names = settings.playerNames.joinToString("，")
            if (loadedNames == null || namesEdit.text.toString() == loadedNames) {
                namesEdit.setText(names)
                loadedNames = names
            }
            val ninja = settings.ninjaQuery
            if (loadedNinja == null || ninjaEdit.text.toString() == loadedNinja) {
                ninjaEdit.setText(ninja)
                loadedNinja = ninja
            }
            syncSide()
            rememberChk.isChecked = settings.rememberSide
            syncMode()
            bothChk.isChecked = settings.showBothSides
            setSlider(miniOpacityBar, MINI_OPACITY_MIN, OPACITY_STEP, settings.miniOpacity.toDouble())
            setSlider(opacityBar, WINDOW_OPACITY_MIN, OPACITY_STEP, settings.windowOpacity.toDouble())
            setSlider(
                fontScaleBar,
                Settings.FONT_SCALE_MIN.toDouble(),
                Settings.FONT_SCALE_STEP.toDouble(),
                settings.fontScale.toDouble(),
            )
            setSlider(cardWidthBar, 1.0, Settings.CARD_STRETCH_STEP.toDouble(), settings.cardWidth.toDouble())
            setSlider(cardHeightBar, 1.0, Settings.CARD_STRETCH_STEP.toDouble(), settings.cardHeight.toDouble())
            updateAppearanceLabels()
            projectionChk.isChecked = settings.preferProjection
            debugChk.isChecked = settings.debug
            if (updateSourceGroup.childCount >= 2) {
                updateSourceGroup.check(if (settings.updateSource == "github") updateSourceGroup.getChildAt(1).id else updateSourceGroup.getChildAt(0).id)
            }
            autoUpdateChk.isChecked = settings.autoCheckUpdates
            if (identityLabelEdit.text.isNullOrEmpty()) {
                settings.playerNames.firstOrNull()?.let { identityLabelEdit.setText(it) }
            }
        } finally {
            syncing = false
        }
        refreshIdentityCount()
        refreshCalibration()
    }

    private fun updateAppearanceLabels() {
        opacityText.text = String.format(Locale.ROOT, "完整卡片不透明度：%.0f%%", settings.windowOpacity * 100)
        miniOpacityText.text = String.format(Locale.ROOT, "迷你卡片不透明度：%.0f%%", settings.miniOpacity * 100)
        fontScaleText.text = String.format(Locale.ROOT, "悬浮窗大小：%.0f%%", settings.fontScale * 100)
        cardSizeText.text = String.format(
            Locale.ROOT, "卡片宽度 %.0f%% · 高度 %.0f%%（上下两条）", settings.cardWidth * 100, settings.cardHeight * 100,
        )
    }

    private fun syncSide() {
        val label = sideLabel(settings.playerSide)
        val i = SIDE_LABELS.indexOf(label).coerceAtLeast(0)
        if (sideGroup.checkedRadioButtonId != sideIds[i]) sideGroup.check(sideIds[i])
    }

    private fun syncMode() {
        val id = if (settings.overlayMode == "mini") modeIds[1] else modeIds[0]
        if (modeGroup.checkedRadioButtonId != id) modeGroup.check(id)
    }

    private fun onSettingChanged(key: String) {
        syncing = true
        try {
            when (key) {
                Settings.KEY_PLAYER_SIDE -> syncSide()
                Settings.KEY_REMEMBER_SIDE -> rememberChk.isChecked = settings.rememberSide
                Settings.KEY_OVERLAY_MODE -> syncMode()
                Settings.KEY_SHOW_BOTH -> bothChk.isChecked = settings.showBothSides
                Settings.KEY_CALIB_PROFILE, in Settings.KEY_CALIB_RECTS -> refreshCalibration()
            }
        } finally {
            syncing = false
        }
        refreshStatus()
    }

    // ---- 区域校准（docs/区域校准.md） ----

    private fun refreshCalibration() {
        calibText.text = "当前：" + CalibrationActivity.summary(settings)
        // Also for saved boxes that are not used (size limits): the summary names them, this clears them.
        calibResetBtn.isEnabled = settings.hasStoredCalibration()
    }

    /** This screen covers the game, so live frames are taken from the in-game panel. */
    private fun showCalibrationGuide() {
        AlertDialog.Builder(this)
            .setTitle("区域校准")
            .setMessage(
                "推荐在游戏里校准：进入对局或训练场，点计时卡片 → 设置 → 位置 →「区域校准」，" +
                    "会截取当前游戏画面并打开校准页。\n\n" +
                    "也可以载入一张对局或训练场的整屏截图（不要裁剪），再手动选择训练场或决斗场。",
            )
            .setPositiveButton("载入截图") { _, _ ->
                runCatching { CalibrationActivity.openFile(this) }
                    .onFailure { Log.e(TAG, "open calibration failed", it) }
            }
            .setNegativeButton("知道了", null)
            .show()
    }

    private fun confirmResetCalibration() {
        AlertDialog.Builder(this)
            .setTitle("恢复自动")
            .setMessage("清除已保存的豆和头像框，改回自动定位？")
            .setPositiveButton("恢复自动") { _, _ ->
                for (key in Settings.KEY_CALIB_RECTS) settings.setCalibRect(key, null)
                refreshCalibration()
                toast("已恢复自动")
            }
            .setNegativeButton("取消", null)
            .show()
    }

    // ---- 保存（Go applySettings） ----

    private fun save() {
        val names = splitNames(namesEdit.text.toString())
        val ninja = GoText.trimSpace(ninjaEdit.text.toString())
        val i = sideIds.indexOf(sideGroup.checkedRadioButtonId).coerceAtLeast(0)
        session.applySettings(names, ninja, sideMode(SIDE_LABELS[i]), rememberChk.isChecked)
        loadedNames = null
        loadedNinja = null
        loadFromSettings()
        refreshStatus()
        toast("设置已保存")
    }

    private fun pickNinja() {
        val labels = arrayOf("留空（自动识别）") + NINJA_CHOICES
        AlertDialog.Builder(this)
            .setTitle("指定对面忍者")
            .setItems(labels) { _, which ->
                ninjaEdit.setText(if (which == 0) "" else NINJA_CHOICES[which - 1])
            }
            .show()
    }

    // ---- 状态 ----

    private fun refreshStatus() {
        val enabled = TimerAccessibilityService.isEnabled(this)
        val connected = TimerAccessibilityService.instance != null
        setStep(
            accessibilityStep,
            if (connected) Ui.OK else if (enabled) Ui.WARN else Ui.BAD,
            when {
                connected -> "已连接"
                enabled -> "已开启，正在连接…（长时间无反应请关闭后重新开启）"
                else -> "未开启 · 悬浮窗和备用截屏都需要它"
            },
            if (connected) "设置" else "去开启",
            primary = !connected,
        ) { openAccessibilitySettings() }

        val overlayRunning = TimerAccessibilityService.instance?.isOverlayRunning == true
        setStep(
            overlayStep,
            if (overlayRunning) Ui.OK else if (connected) Ui.WARN else Ui.TEXT_2,
            when {
                overlayRunning -> "运行中 · 点击游戏里的计时卡片打开操作"
                connected -> "未启动"
                else -> "等待无障碍服务"
            },
            if (overlayRunning) "停止" else "启动",
            primary = !overlayRunning && connected,
            enabled = connected,
        ) {
            val service = TimerAccessibilityService.instance
            when {
                service == null -> toast("请先开启并连接无障碍服务")
                overlayRunning -> service.stopOverlay()
                else -> service.startOverlay()
            }
            main.postDelayed({ refreshStatus() }, 300)
        }
        restartOverlayBtn.isEnabled = connected
        positionBtn.isEnabled = overlayRunning

        val projecting = ProjectionCaptureService.running
        setStep(
            projectionStep,
            if (projecting) Ui.OK else Ui.WARN,
            if (projecting) "运行中 · 约 60 帧/秒" else "未运行 · 正在用无障碍截屏（约 3 帧/秒）",
            if (projecting) "停止" else "开始",
            primary = !projecting && overlayRunning,
        ) {
            if (projecting) ProjectionCaptureService.stop(this) else ProjectionPermissionActivity.launch(this)
            main.postDelayed({ refreshStatus() }, 300)
        }

        val side = when (session.side) {
            "left" -> "我方在左边"
            "right" -> "我方在右边"
            else -> "待认边" + sideHint()
        }
        matchText.text = (if (session.fighting) "对局中 · " else "未在对局 · ") + side
        matchText.setTextColor(if (session.fighting) Ui.OK else Ui.TEXT)

        loopText.text = if (!FrameLoop.running) {
            "识别：未运行（启动悬浮计时或录屏后开始）"
        } else {
            val st = FrameLoop.stats()
            val method = when (st.lastMethod) {
                NativeCore.METHOD_PROJECTION -> "录屏"
                NativeCore.METHOD_SCREENSHOT -> "无障碍截屏"
                NativeCore.METHOD_BUFFER -> "内存帧"
                else -> "—"
            }
            val size = if (st.lastWidth > 0) " ${st.lastWidth}×${st.lastHeight}" else ""
            val detail = if (settings.debug) {
                String.format(Locale.ROOT, " · scene=%s · gate=%.3f%s", st.lastScene.ifEmpty { "—" }, st.lastGateScore, if (st.lastUncertain) " · uncertain" else "")
            } else ""
            String.format(
                Locale.ROOT, "识别：已分析 %d 帧 · 丢弃 %d · 平均 %.1fms · %s%s%s",
                st.analyzedFrames, st.droppedFrames, st.avgAnalyzeMillis, method, size, detail,
            )
        }

        nativeText.text = when (app.nativeStatus) {
            TimerApp.NativeStatus.LOADING -> getString(R.string.status_native_loading)
            TimerApp.NativeStatus.READY -> {
                val r = app.assetReport
                getString(R.string.status_native_ready) +
                    (if (r != null) "（图片 ${r.images} · 文本 ${r.texts} · ${r.millis}ms）" else "")
            }
            TimerApp.NativeStatus.FAILED -> getString(R.string.status_native_failed, app.nativeMessage)
        }
        nativeText.setTextColor(if (app.nativeStatus == TimerApp.NativeStatus.FAILED) Ui.BAD else Ui.TEXT_2)

        val autoGrant = projectMediaAllowed()
        appOpsText.text = if (autoGrant) "录屏免确认（PROJECT_MEDIA）：已允许" else "录屏免确认：未设置（每次需在系统弹窗确认，可用电脑 ADB 设置）"
    }

    /** adb shell appops set <pkg> PROJECT_MEDIA allow 之后，系统录屏授权页会直接放行。 */
    private fun projectMediaAllowed(): Boolean = try {
        val ops = getSystemService(AppOpsManager::class.java)
        ops.unsafeCheckOpNoThrow(OPSTR_PROJECT_MEDIA, Process.myUid(), packageName) == AppOpsManager.MODE_ALLOWED
    } catch (e: RuntimeException) {
        false
    }

    private fun openAccessibilitySettings() {
        try {
            startActivity(Intent(SystemSettings.ACTION_ACCESSIBILITY_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        } catch (e: RuntimeException) {
            Log.e(TAG, "open accessibility settings failed", e)
            toast("无法打开无障碍设置，请手动进入系统设置 → 无障碍")
        }
    }

    private fun showPermissionGuide() {
        val enabled = TimerAccessibilityService.isEnabled(this)
        val connected = TimerAccessibilityService.instance != null
        val state = when {
            connected -> "当前状态：无障碍已连接。悬浮窗可以正常显示。"
            enabled -> "当前状态：无障碍开关已开，但服务尚未连接。请返回桌面重新打开本应用；若仍无效，在系统无障碍详情页关闭后再开启。"
            else -> "当前状态：无障碍未开启。"
        }
        AlertDialog.Builder(this)
            .setTitle("权限指引（不需要 root）")
            .setMessage(
                "1. 无障碍服务：用于原生悬浮窗和备用截屏，不读取游戏文字。\n" +
                    "2. 录屏授权：推荐开启 MediaProjection，帧率更高、延迟更低；每次授权只截取你选择的游戏画面。\n" +
                    "3. 不需要 SYSTEM_ALERT_WINDOW，也不需要 root。\n\n" + state +
                    "\n\n如果看到“此服务出现故障”，先关闭该服务，返回应用后重新打开，再回到这里开启；不要重复点击录屏授权。",
            )
            .setPositiveButton("打开无障碍") { _, _ -> openAccessibilitySettings() }
            .setNeutralButton("授权录屏") { _, _ -> ProjectionPermissionActivity.launch(this) }
            .setNegativeButton("知道了", null)
            .show()
    }

    // ---- adb 命令 ----

    private fun serviceComponent(): String = "$packageName/${TimerAccessibilityService::class.java.name}"

    private fun appOpsCommand(): String = "adb shell appops set $packageName PROJECT_MEDIA allow"

    private fun accessibilityCommand(): String =
        "adb shell settings put secure enabled_accessibility_services ${serviceComponent()}\n" +
            "adb shell settings put secure accessibility_enabled 1"

    private fun command(cmd: String): View {
        val tv = Ui.text(this, cmd, 12f, Ui.TEXT).apply {
            typeface = Typeface.MONOSPACE
            setTextIsSelectable(true)
            background = Ui.rounded(this@MainActivity, Ui.BG, 10f, Ui.OUTLINE)
            setPadding(dp(10), dp(8), dp(10), dp(8))
        }
        val copy = Ui.button(this, "复制") {
            val cm = getSystemService(ClipboardManager::class.java)
            cm.setPrimaryClip(ClipData.newPlainText("adb", cmd))
            toast("已复制")
        }
        return LinearLayout(this).apply {
            gravity = Gravity.CENTER_VERTICAL
            addView(tv, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(copy, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply { marginStart = dp(8) })
        }
    }

    // ---- 账号名图片 ----

    private fun identityDir(): File = File(filesDir, IDENTITY_DIR)

    private fun countImages(dir: File): Int =
        dir.listFiles()?.count { it.isFile && isIdentityImage(it.name) } ?: 0

    /** Labels of the imported account-name images (refreshIdentityCount): which typed names auto side detection can use. */
    private var identityLabels: Set<String> = emptySet()

    private fun refreshIdentityCount() {
        val dir = identityDir()
        val mine = countImages(dir)
        val seen = countImages(File(dir, "seen"))
        val fight = countImages(File(dir, "fight"))
        identityLabels = IdentityStore.importedLabels(filesDir)
        val missing = IdentityStore.namesWithoutImage(settings.playerNames, identityLabels)
        identityText.text = "已导入 $mine 张 · 自动收集 $seen 张" + (if (fight > 0) " · 对局内 $fight 张" else "") +
            (if (missing.isNotEmpty() && mine > 0) "\n这些名字没有图片（仍可靠文字识别认边）：" + missing.joinToString("，") else "")
    }

    /**
     * Why the side is still unknown (status line), for the automatic mode only. A typed name is
     * enough: the account in the battle HUD title is read on the phone (NativeCore.textStatus).
     */
    private fun sideHint(): String {
        if (settings.playerSide == "left" || settings.playerSide == "right") return ""
        if (settings.playerNames.isEmpty()) return "（未填「我的名字」：填上后对局中自动认边，或在对局设置里选左边/右边）"
        val text = if (app.nativeStatus == TimerApp.NativeStatus.READY) NativeCore.textStatus() else null
        return when {
            app.nativeStatus != TimerApp.NativeStatus.READY -> ""
            text == null -> "（本机文字识别不可用：请在对局设置里选左边/右边）"
            text.state == "unavailable" -> "（文字识别暂不可用：${text.error.substringAfter(": ", text.error)}；可选左边/右边）"
            !session.fighting -> "（进入对局后读取血条上方的账号名）"
            text.state == "reading" || text.state == "pending" -> "（正在读取对局顶部的账号名）"
            else -> "（对局顶部没读到「我的名字」：确认名字与游戏里一致，或选左边/右边）"
        }
    }

    private fun pickIdentityImages() {
        val label = GoText.normalizeName(identityLabelEdit.text.toString())
            .ifEmpty { splitNames(namesEdit.text.toString()).firstOrNull() ?: "" }
        if (label.isEmpty() || label == "seen") {
            toast("请先填写图片对应的账号名")
            return
        }
        pendingIdentityLabel = label
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .setType("image/*")
            .putExtra(Intent.EXTRA_MIME_TYPES, arrayOf("image/png", "image/jpeg"))
            .putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
        try {
            @Suppress("DEPRECATION")
            startActivityForResult(intent, REQUEST_IMPORT)
        } catch (e: RuntimeException) {
            Log.e(TAG, "open document failed", e)
            toast("没有可用的文件选择器")
        }
    }

    private var pendingIdentityLabel = ""

    @Deprecated("Activity result API without androidx")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != REQUEST_IMPORT || resultCode != RESULT_OK || data == null) return
        val uris = ArrayList<Uri>()
        val clip = data.clipData
        if (clip != null) {
            for (i in 0 until clip.itemCount) clip.getItemAt(i).uri?.let { uris += it }
        } else {
            data.data?.let { uris += it }
        }
        val label = pendingIdentityLabel
        if (uris.isEmpty() || label.isEmpty()) return
        val resolver = contentResolver
        val dir = identityDir()
        // A crop of this phone's own screenshot is brought to the 1080p HUD scale the engine reads it at.
        val screen = getSystemService(WindowManager::class.java).maximumWindowMetrics.bounds
        val scale = IdentityStore.importScale(screen.width(), screen.height())
        Thread({
            var ok = 0
            var skipped = 0
            var reason = "只支持 PNG/JPG"
            val stamp = SimpleDateFormat("yyyyMMdd-HHmmss", Locale.ROOT).format(Date())
            dir.mkdirs()
            uris.forEachIndexed { i, uri ->
                // Go 只认 png/jpg/jpeg。
                val ext = when (resolver.getType(uri)) {
                    "image/png" -> "png"
                    "image/jpeg", "image/jpg" -> "jpg"
                    else -> null
                }
                if (ext == null) {
                    skipped++
                    return@forEachIndexed
                }
                // Go labelFromFile：第一个 '_' 之前是账号名。
                val out = File(dir, "${label}_$stamp-$i.$ext")
                try {
                    // 大小上限：一张超大照片会让引擎每次启动都加载失败（AND-06）。
                    val fits = resolver.openInputStream(uri)?.use { input ->
                        Housekeeping.copyLimited(input, out, IdentityStore.MAX_FILE_BYTES)
                    } ?: throw java.io.IOException("open failed")
                    val problem = if (fits) AssetLoader.identityImageProblem(out) else IdentityStore.FILE_TOO_BIG
                    if (problem != null) {
                        out.delete()
                        skipped++
                        reason = problem
                        return@forEachIndexed
                    }
                    AssetLoader.scaleIdentityImage(out, scale)
                    ok++
                } catch (e: Exception) {
                    Log.e(TAG, "import $uri failed", e)
                    out.delete()
                    skipped++
                }
            }
            main.post {
                if (ok > 0) app.reloadNative()
                if (isDestroyed) return@post
                refreshIdentityCount()
                refreshStatus()
                toast("已导入 $ok 张" + (if (skipped > 0) "，跳过 $skipped 张：$reason" else "") +
                    (if (ok > 0 && scale != 1.0) String.format(Locale.ROOT, "（按本机屏幕缩放 ×%.2f）", scale) else "") +
                    (if (ok > 0) "，正在重新加载识别资源" else ""))
            }
        }, "nt-import").start()
    }

    private fun confirmDeleteIdentity() {
        val files = identityDir().listFiles()?.filter { it.isFile && isIdentityImage(it.name) }.orEmpty()
        if (files.isEmpty()) {
            toast("没有已导入的图片")
            return
        }
        AlertDialog.Builder(this)
            .setTitle("删除已导入图片")
            .setMessage("删除 ${files.size} 张手动导入的账号名图片（不影响自动收集的 seen 目录）？")
            .setPositiveButton("删除") { _, _ ->
                files.forEach { it.delete() }
                app.reloadNative()
                refreshIdentityCount()
                refreshStatus()
            }
            .setNegativeButton("取消", null)
            .show()
    }

    /** 对面账号名的自动收集（identity/seen）全部删除，并重建引擎让内存里的也不再用（AND-05）。 */
    private fun confirmClearSeen() {
        val count = countImages(IdentityStore.seenDir(filesDir))
        if (count == 0) {
            toast("没有自动收集的图片")
            return
        }
        AlertDialog.Builder(this)
            .setTitle("清除自动收集")
            .setMessage("删除自动收集的 $count 张对面账号名图片（不影响手动导入的图片）？之后对局中仍会重新收集。")
            .setPositiveButton("清除") { _, _ ->
                Thread({
                    val n = IdentityStore.clearSeen(filesDir)
                    main.post {
                        app.reloadNative()
                        if (isDestroyed) return@post
                        refreshIdentityCount()
                        refreshStatus()
                        toast("已清除 $n 张，正在重新加载识别资源")
                    }
                }, "nt-clear-seen").start()
            }
            .setNegativeButton("取消", null)
            .show()
    }

    // ---- 小部件 ----

    private fun dp(v: Int): Int = Ui.dp(this, v.toFloat())

    private fun radio(label: String): RadioButton = RadioButton(this).apply {
        text = label
        id = View.generateViewId()
        minHeight = dp(44)
        setTextColor(Ui.TEXT)
    }

    private fun check(label: String, onChange: (Boolean) -> Unit): CheckBox = CheckBox(this).apply {
        text = label
        minHeight = dp(44)
        setTextColor(Ui.TEXT)
        setOnCheckedChangeListener { _, on -> if (!syncing) onChange(on) }
    }

    /** Go widget.Slider(min, max) + Step：SeekBar 0..steps。拖动中即应用（Go 立即预览）。 */
    private fun slider(min: Double, max: Double, step: Double, onValue: (Double) -> Unit): SeekBar =
        SeekBar(this).apply {
            this.max = ((max - min) / step).roundToInt()
            setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
                override fun onProgressChanged(bar: SeekBar, progress: Int, fromUser: Boolean) {
                    if (!syncing && fromUser) onValue(min + progress * step)
                }

                override fun onStartTrackingTouch(bar: SeekBar) {}
                override fun onStopTrackingTouch(bar: SeekBar) {}
            })
        }

    private fun setSlider(bar: SeekBar, min: Double, step: Double, value: Double) {
        bar.progress = ((value - min) / step).roundToInt().coerceIn(0, bar.max)
    }

    private fun toast(s: String) = Toast.makeText(this, s, Toast.LENGTH_SHORT).show()

    companion object {
        private const val TAG = "MainActivity"
        private const val STATUS_INTERVAL_MS = 1_000L
        private const val REQUEST_IMPORT = 41
        private const val REQUEST_NOTIFICATIONS = 42
        private const val STATE_IDENTITY_LABEL = "identityLabel"
        private const val IDENTITY_DIR = "identity"
        /** AppOpsManager.OPSTR_PROJECT_MEDIA（隐藏常量）。 */
        private const val OPSTR_PROJECT_MEDIA = "android:project_media"

        // Go settings.go：minimumOverlayOpacity…maximumOverlayOpacity、minimumMiniOpacity…
        private const val WINDOW_OPACITY_MIN = 0.40
        private const val WINDOW_OPACITY_MAX = 1.00
        private const val MINI_OPACITY_MIN = 0.20
        private const val MINI_OPACITY_MAX = 1.00
        private const val DEFAULT_MINI_OPACITY = 0.85
        private const val OPACITY_STEP = 0.05

        /** Go sideSel 选项。 */
        private val SIDE_LABELS = arrayOf("自动认边", "左边", "右边")
        /** Go miniSel 选项。 */
        private val MODE_LABELS = arrayOf("完整卡片", "迷你卡片")
        /** Go ninjaEntry 选项（除空串外）。 */
        private val NINJA_CHOICES = arrayOf(
            NinjaRules.FIFTH_MIZUKAGE, NinjaRules.HASHIRAMA, NinjaRules.MADARA, NinjaRules.OBITO, NinjaRules.NARUTO,
            NinjaRules.HANZO_EDO,
        )

        /** Go sideMode。 */
        fun sideMode(label: String): String = when (label) {
            "左边", "left" -> "left"
            "右边", "right" -> "right"
            else -> "auto"
        }

        /** Go sideLabel。 */
        fun sideLabel(mode: String): String = when (mode) {
            "left" -> "左边"
            "right" -> "右边"
            else -> "自动认边"
        }

        /** Go splitNames（另把换行也当分隔符）：全角逗号、顿号、空格 → ','，NormalizeName，去空去重保序。 */
        fun splitNames(s: String): List<String> {
            val t = s.replace("，", ",").replace("、", ",").replace(" ", ",").replace("\n", ",")
            val out = ArrayList<String>()
            val seen = HashSet<String>()
            for (p in t.split(',')) {
                val n = GoText.normalizeName(p)
                if (n.isEmpty() || !seen.add(n)) continue
                out += n
            }
            return out
        }

        private fun isIdentityImage(name: String): Boolean {
            val low = name.lowercase(Locale.ROOT)
            return low.endsWith(".png") || low.endsWith(".jpg") || low.endsWith(".jpeg")
        }
    }
}
