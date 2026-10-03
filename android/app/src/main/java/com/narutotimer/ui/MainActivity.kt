package com.narutotimer.ui

// Owner: kt-overlay-ui
// Port of: internal/ui/settings.go openSettings / applySettings / sideMode / sideLabel，
//          internal/ui/run.go splitNames，internal/ui/mini.go（迷你不透明度）、internal/ui/appearance.go
//          （窗口不透明度/字号的取值范围与步进）。
//
// 主界面（纯代码布局，无 AndroidX）：
//  - 第 1 步 开启无障碍服务（悬浮窗 + 备用截屏都靠它）。
//  - 第 2 步 授权录屏（推荐；约 60 帧/秒），或依赖无障碍截屏（约 3 帧/秒）。
//  - 第 3 步（可选）adb 命令：PROJECT_MEDIA 免确认、命令行开启无障碍。
//  - 状态：native 资源、无障碍、录屏、appops、FrameLoop.stats()、对局/认边，每秒刷新。
//  - 设置（Go openSettings）：认边方式/记住认边点选即生效（Go OnChanged）；迷你/两边计时/不透明度/字号
//    立即生效（Go 立即预览并保存）；账号名与对面忍者在"保存设置"时经 session.applySettings 生效。
//    每次回到前台都按当前状态重建（Go：Rebuild from current state every time —— 悬浮窗的换边按钮可能改过）。
//  - 账号名图片导入（可选）：复制到 filesDir/identity/<账号名>_<时间>.png|jpg（Go labelFromFile：
//    文件名第一个 '_' 之前即账号名），然后 TimerApp.instance.reloadNative()。
// 不移植：窗口置顶、OCR、GPU、更新、诊断录制、真悬浮窗开关（Android 悬浮窗始终是真悬浮窗）。

import android.app.Activity
import android.app.AlertDialog
import android.app.AppOpsManager
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
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
import com.narutotimer.NativeCore
import com.narutotimer.Settings
import com.narutotimer.TimerApp
import com.narutotimer.capture.FrameLoop
import com.narutotimer.capture.ProjectionCaptureService
import com.narutotimer.service.TimerAccessibilityService
import com.narutotimer.tracker.GoText
import com.narutotimer.tracker.NinjaRules
import com.narutotimer.tracker.Session
import com.narutotimer.tracker.TimerColors
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
    private val density get() = resources.displayMetrics.density

    // 状态区。
    private lateinit var nativeText: TextView
    private lateinit var accessibilityText: TextView
    private lateinit var overlayText: TextView
    private lateinit var projectionText: TextView
    private lateinit var appOpsText: TextView
    private lateinit var loopText: TextView
    private lateinit var matchText: TextView
    private lateinit var openAccessibilityBtn: Button
    private lateinit var startProjectionBtn: Button
    private lateinit var stopProjectionBtn: Button
    private lateinit var startOverlayBtn: Button
    private lateinit var stopOverlayBtn: Button
    private lateinit var restartOverlayBtn: Button

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
    private lateinit var projectionChk: CheckBox
    private lateinit var debugChk: CheckBox
    private lateinit var updateSourceGroup: RadioGroup
    private lateinit var autoUpdateChk: CheckBox
    private lateinit var identityText: TextView
    private lateinit var identityLabelEdit: EditText

    private val sideIds = IntArray(3)
    private val modeIds = IntArray(2)

    /** 程序化回填控件时为 true：控件回调不得再写回 Session/Settings。 */
    private var syncing = false
    private var resumed = false
    private var autoUpdateChecked = false

    private val statusTick = object : Runnable {
        override fun run() {
            refreshStatus()
            if (resumed) main.postDelayed(this, STATUS_INTERVAL_MS)
        }
    }

    private val nativeListener: () -> Unit = { if (resumed) refreshStatus() }

    /** 悬浮窗按钮（换边/两边计时/迷你）或其它入口修改设置时同步控件（Go syncSettingsSide）。 */
    private val settingsListener: (String) -> Unit = { key ->
        main.post { if (resumed) onSettingChanged(key) }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        app = application as TimerApp
        settings = app.settings
        session = app.session
        title = getString(com.narutotimer.R.string.app_name)
        setContentView(buildContent())
    }

    override fun onResume() {
        super.onResume()
        resumed = true
        settings.addListener(settingsListener)
        app.addNativeListener(nativeListener)
        loadFromSettings()
        main.removeCallbacks(statusTick)
        statusTick.run()
        if (!autoUpdateChecked && settings.autoCheckUpdates) {
            autoUpdateChecked = true
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
            setPadding(dp(16), dp(14), dp(16), dp(28))
        }

        fun add(parent: ViewGroup, child: View, top: Int = 8) {
            parent.addView(child, LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ).apply { topMargin = dp(top) })
        }

        add(content, text(getString(com.narutotimer.R.string.main_title), 21f, bold = true).apply {
            setTextColor(TimerColors.CLOCK_LIVE)
        }, 0)
        add(content, note("先开启无障碍，再按需授权录屏。状态和常用操作集中在上方。"), 2)

        // 状态卡：一眼确认资源、服务和识别循环是否正常。
        val statusCard = card()
        add(statusCard, text("运行状态", 17f, bold = true).apply { setTextColor(TimerColors.CLOCK_LIVE) }, 0)
        nativeText = text("", 14f).also { add(statusCard, it, 8) }
        accessibilityText = text("", 14f).also { add(statusCard, it, 4) }
        overlayText = text("", 14f).also { add(statusCard, it, 4) }
        projectionText = text("", 14f).also { add(statusCard, it, 4) }
        appOpsText = text("", 13f).also { add(statusCard, it, 4) }
        loopText = text("", 13f).also { add(statusCard, it, 4) }
        matchText = text("", 14f).also { add(statusCard, it, 4) }
        add(content, statusCard, 14)

        // 三个主操作保持大触控区域，避免用户在手机上寻找按钮。
        val actionsCard = card()
        add(actionsCard, text("快速操作", 17f, bold = true).apply { setTextColor(TimerColors.CLOCK_LIVE) }, 0)
        openAccessibilityBtn = button(getString(com.narutotimer.R.string.action_open_accessibility)) { openAccessibilitySettings() }
        startOverlayBtn = button("启动悬浮窗") {
            TimerAccessibilityService.instance?.startOverlay() ?: toast("请先开启并连接无障碍服务")
            main.postDelayed({ refreshStatus() }, 300)
        }
        stopOverlayBtn = button("停止悬浮窗") {
            TimerAccessibilityService.instance?.stopOverlay() ?: toast("无障碍服务未连接")
            main.postDelayed({ refreshStatus() }, 300)
        }
        restartOverlayBtn = button("重启悬浮窗") {
            TimerAccessibilityService.instance?.restartOverlay() ?: toast("请先开启并连接无障碍服务")
            main.postDelayed({ refreshStatus() }, 500)
        }
        startProjectionBtn = button(getString(com.narutotimer.R.string.action_start_projection)) {
            ProjectionPermissionActivity.launch(this)
        }
        stopProjectionBtn = button(getString(com.narutotimer.R.string.action_stop_projection)) {
            ProjectionCaptureService.stop(this)
            main.postDelayed({ refreshStatus() }, 300)
        }
        val actionRow = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
        }
        add(actionRow, openAccessibilityBtn, 8)
        add(actionRow, startOverlayBtn, 6)
        add(actionRow, stopOverlayBtn, 6)
        add(actionRow, restartOverlayBtn, 6)
        add(actionRow, startProjectionBtn, 6)
        add(actionRow, stopProjectionBtn, 6)
        add(actionsCard, actionRow, 0)
        add(actionsCard, button("调整显示位置") {
            TimerAccessibilityService.instance?.beginPositionEdit()
                ?: toast("请先开启并连接无障碍服务")
        }, 6)
        add(actionsCard, button("权限指引") { showPermissionGuide() }, 6)
        add(content, actionsCard, 10)

        // 最常用的设置不折叠：手机竖屏下仍能快速切换认边方式。
        val basicsCard = card()
        add(basicsCard, text("对局设置", 17f, bold = true).apply { setTextColor(TimerColors.CLOCK_LIVE) }, 0)
        add(basicsCard, text("我方所在边", 14f, bold = true), 10)
        sideGroup = RadioGroup(this).apply { orientation = RadioGroup.HORIZONTAL }
        SIDE_LABELS.forEachIndexed { i, label ->
            val rb = RadioButton(this).apply {
                text = label
                id = View.generateViewId()
                minHeight = dp(48)
                setPadding(dp(2), 0, dp(10), 0)
            }
            sideIds[i] = rb.id
            sideGroup.addView(rb, RadioGroup.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        }
        sideGroup.setOnCheckedChangeListener { _, checkedId ->
            if (syncing) return@setOnCheckedChangeListener
            val i = sideIds.indexOf(checkedId)
            if (i >= 0) session.selectSideMode(sideMode(SIDE_LABELS[i]))
            refreshStatus()
        }
        add(basicsCard, sideGroup, 2)
        rememberChk = check("记住认边方式") { on -> session.setRememberSide(on) }
        add(basicsCard, rememberChk, 2)
        add(basicsCard, text("自动认边未完成时会显示“待认边”。", 12f).apply { alpha = 0.72f }, 0)

        add(basicsCard, text("我的名字", 14f, bold = true), 12)
        namesEdit = EditText(this).apply {
            hint = "账号名（多个用逗号分隔）"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE
            minLines = 1
            maxLines = 2
        }
        add(basicsCard, namesEdit, 2)
        add(basicsCard, text("对面忍者（可选）", 14f, bold = true), 8)
        ninjaEdit = EditText(this).apply {
            hint = "留空自动识别"
            inputType = InputType.TYPE_CLASS_TEXT
            isSingleLine = true
        }
        val pickNinja = button("选择") { pickNinja() }
        val ninjaRow = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        ninjaRow.addView(ninjaEdit, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        ninjaRow.addView(pickNinja, LinearLayout.LayoutParams(dp(92), ViewGroup.LayoutParams.WRAP_CONTENT))
        add(basicsCard, ninjaRow, 2)
        add(basicsCard, button(getString(com.narutotimer.R.string.action_save)) { save() }.apply {
            setTypeface(typeface, Typeface.BOLD)
        }, 10)
        add(content, basicsCard, 10)

        // 高级内容默认收起，保留全部既有控件和 adb/文件选择功能。
        val advancedCard = card()
        lateinit var advancedPanel: LinearLayout
        val advancedToggle = TextView(this).apply {
            text = "高级设置  ▸"
            textSize = 17f
            setTypeface(typeface, Typeface.BOLD)
            setTextColor(TimerColors.CLOCK_LIVE)
            gravity = Gravity.CENTER_VERTICAL
            minHeight = dp(52)
            isClickable = true
            isFocusable = true
            setOnClickListener {
                advancedPanel.visibility = if (advancedPanel.visibility == View.VISIBLE) View.GONE else View.VISIBLE
                text = if (advancedPanel.visibility == View.VISIBLE) "高级设置  ▾" else "高级设置  ▸"
            }
        }
        add(advancedCard, advancedToggle, 0)
        advancedPanel = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            visibility = View.GONE
        }
        add(advancedCard, advancedPanel, 0)

        val appearanceTitle = text("悬浮窗", 15f, bold = true).apply { setTextColor(TimerColors.CLOCK_LIVE) }
        add(advancedPanel, appearanceTitle, 8)
        modeGroup = RadioGroup(this).apply { orientation = RadioGroup.HORIZONTAL }
        MODE_LABELS.forEachIndexed { i, label ->
            val rb = RadioButton(this).apply {
                text = label
                id = View.generateViewId()
                minHeight = dp(48)
            }
            modeIds[i] = rb.id
            modeGroup.addView(rb, RadioGroup.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        }
        modeGroup.setOnCheckedChangeListener { _, checkedId ->
            if (syncing) return@setOnCheckedChangeListener
            val on = checkedId == modeIds[1]
            if (on != (settings.overlayMode == "mini")) session.setMiniMode(on)
        }
        add(advancedPanel, modeGroup, 2)
        bothChk = check("左右两边都显示替身倒计时") { on -> session.setShowBothSides(on) }
        add(advancedPanel, bothChk, 2)
        miniOpacityText = text("", 13f).also { add(advancedPanel, it, 6) }
        miniOpacityBar = slider(MINI_OPACITY_MIN, MINI_OPACITY_MAX, OPACITY_STEP) { v ->
            miniOpacityText.text = String.format(Locale.ROOT, "迷你窗口不透明度：%.0f%%", v * 100)
            settings.miniOpacity = v.toFloat()
        }
        add(advancedPanel, miniOpacityBar, 0)
        opacityText = text("", 13f).also { add(advancedPanel, it, 4) }
        opacityBar = slider(WINDOW_OPACITY_MIN, WINDOW_OPACITY_MAX, OPACITY_STEP) { v ->
            opacityText.text = String.format(Locale.ROOT, "窗口不透明度：%.0f%%", v * 100)
            settings.windowOpacity = v.toFloat()
        }
        add(advancedPanel, opacityBar, 0)
        fontScaleText = text("", 13f).also { add(advancedPanel, it, 4) }
        fontScaleBar = slider(FONT_SCALE_MIN, FONT_SCALE_MAX, FONT_SCALE_STEP) { v ->
            fontScaleText.text = String.format(Locale.ROOT, "悬浮窗大小：%.0f%%", v * 100)
            settings.fontScale = v.toFloat()
        }
        add(advancedPanel, fontScaleBar, 0)
        add(advancedPanel, row(
            button("默认外观") {
                settings.windowOpacity = 1f
                settings.fontScale = 0.75f
                loadFromSettings()
            },
            button("默认位置") {
                settings.overlayX = Int.MIN_VALUE
                settings.overlayY = Int.MIN_VALUE
                toast("悬浮窗已回到默认位置")
            },
        ), 6)

        val captureTitle = text("采集与调试", 15f, bold = true).apply { setTextColor(TimerColors.CLOCK_LIVE) }
        add(advancedPanel, captureTitle, 14)
        projectionChk = check("优先使用录屏采集") { on -> settings.preferProjection = on }
        debugChk = check("输出调试日志") { on -> settings.debug = on }
        add(advancedPanel, projectionChk, 2)
        add(advancedPanel, debugChk, 2)

        val updateTitle = text("在线更新", 15f, bold = true).apply { setTextColor(TimerColors.CLOCK_LIVE) }
        add(advancedPanel, updateTitle, 14)
        add(advancedPanel, note("可选择 Gitee（国内）或 GitHub（海外）。只下载 HTTPS、带 SHA-256 校验的正式 APK。"), 2)
        updateSourceGroup = RadioGroup(this).apply { orientation = RadioGroup.HORIZONTAL }
        val gitee = RadioButton(this).apply { id = View.generateViewId(); text = "Gitee（国内）"; minHeight = dp(48) }
        val github = RadioButton(this).apply { id = View.generateViewId(); text = "GitHub（海外）"; minHeight = dp(48) }
        updateSourceGroup.addView(gitee, RadioGroup.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        updateSourceGroup.addView(github, RadioGroup.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        updateSourceGroup.setOnCheckedChangeListener { _, id ->
            if (!syncing) settings.updateSource = if (id == github.id) "github" else "gitee"
        }
        add(advancedPanel, updateSourceGroup, 2)
        autoUpdateChk = check("启动应用时自动检查更新") { on -> settings.autoCheckUpdates = on }
        add(advancedPanel, autoUpdateChk, 2)
        add(advancedPanel, button("检查更新") { UpdateManager.check(this, settings) }, 4)

        val adbTitle = text("电脑 ADB（可选）", 15f, bold = true).apply { setTextColor(TimerColors.CLOCK_LIVE) }
        add(advancedPanel, adbTitle, 14)
        add(advancedPanel, note("仅在需要免确认录屏或命令开启无障碍时使用。点击命令可复制。"), 2)
        add(advancedPanel, command(appOpsCommand()), 4)
        add(advancedPanel, command(accessibilityCommand()), 4)

        val identityTitle = text("账号名图片（可选）", 15f, bold = true).apply { setTextColor(TimerColors.CLOCK_LIVE) }
        add(advancedPanel, identityTitle, 14)
        add(advancedPanel, note("导入 PNG/JPG 裁剪图可帮助自动认边；对面的图片会自动收集。"), 2)
        identityLabelEdit = EditText(this).apply {
            hint = "图片对应的账号名"
            inputType = InputType.TYPE_CLASS_TEXT
            isSingleLine = true
        }
        add(advancedPanel, identityLabelEdit, 2)
        identityText = text("", 13f).also { add(advancedPanel, it, 4) }
        add(advancedPanel, row(
            button("导入图片") { pickIdentityImages() },
            button("删除已导入") { confirmDeleteIdentity() },
        ), 4)
        add(content, advancedCard, 10)

        return ScrollView(this).apply {
            isFillViewport = true
            isVerticalScrollBarEnabled = true
            addView(content, ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ))
        }
    }

    // ---- 回填（Go openSettings：每次按当前状态重建） ----

    private fun loadFromSettings() {
        syncing = true
        try {
            namesEdit.setText(settings.playerNames.joinToString("，"))
            ninjaEdit.setText(settings.ninjaQuery)
            syncSide()
            rememberChk.isChecked = settings.rememberSide
            syncMode()
            bothChk.isChecked = settings.showBothSides
            setSlider(miniOpacityBar, MINI_OPACITY_MIN, OPACITY_STEP, settings.miniOpacity.toDouble())
            miniOpacityText.text = String.format(Locale.ROOT, "迷你窗口不透明度：%.0f%%", settings.miniOpacity * 100)
            setSlider(opacityBar, WINDOW_OPACITY_MIN, OPACITY_STEP, settings.windowOpacity.toDouble())
            opacityText.text = String.format(Locale.ROOT, "窗口不透明度：%.0f%%", settings.windowOpacity * 100)
            setSlider(fontScaleBar, FONT_SCALE_MIN, FONT_SCALE_STEP, settings.fontScale.toDouble())
            fontScaleText.text = String.format(Locale.ROOT, "悬浮窗大小：%.0f%%", settings.fontScale * 100)
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
            }
        } finally {
            syncing = false
        }
        refreshStatus()
    }

    // ---- 保存（Go applySettings） ----

    private fun save() {
        val names = splitNames(namesEdit.text.toString())
        val ninja = GoText.trimSpace(ninjaEdit.text.toString())
        val i = sideIds.indexOf(sideGroup.checkedRadioButtonId).coerceAtLeast(0)
        session.applySettings(names, ninja, sideMode(SIDE_LABELS[i]), rememberChk.isChecked)
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
        nativeText.text = when (app.nativeStatus) {
            TimerApp.NativeStatus.LOADING -> getString(com.narutotimer.R.string.status_native_loading)
            TimerApp.NativeStatus.READY -> {
                val r = app.assetReport
                getString(com.narutotimer.R.string.status_native_ready) +
                    (if (r != null) "（图片 ${r.images} · 文本 ${r.texts} · ${r.millis}ms）" else "")
            }
            TimerApp.NativeStatus.FAILED -> getString(com.narutotimer.R.string.status_native_failed, app.nativeMessage)
        }
        nativeText.setTextColor(statusColor(app.nativeStatus == TimerApp.NativeStatus.READY, app.nativeStatus == TimerApp.NativeStatus.FAILED))

        val enabled = TimerAccessibilityService.isEnabled(this)
        val connected = TimerAccessibilityService.instance != null
        accessibilityText.text = when {
            connected -> getString(com.narutotimer.R.string.status_accessibility_on)
            enabled -> "无障碍服务：已开启，正在连接…（长时间无反应请关闭后重新开启）"
            else -> getString(com.narutotimer.R.string.status_accessibility_off)
        }
        accessibilityText.setTextColor(statusColor(connected, !enabled))
        openAccessibilityBtn.text = if (connected) "无障碍设置" else getString(com.narutotimer.R.string.action_open_accessibility)
        val overlayRunning = TimerAccessibilityService.instance?.isOverlayRunning == true
        overlayText.text = when {
            overlayRunning -> "悬浮窗：运行中"
            connected -> "悬浮窗：未启动（点击启动）"
            else -> "悬浮窗：等待无障碍服务"
        }
        overlayText.setTextColor(statusColor(overlayRunning, false))
        startOverlayBtn.isEnabled = connected && !overlayRunning
        stopOverlayBtn.isEnabled = overlayRunning
        restartOverlayBtn.isEnabled = connected

        val projecting = ProjectionCaptureService.running
        projectionText.text = getString(
            if (projecting) com.narutotimer.R.string.status_projection_on else com.narutotimer.R.string.status_projection_off,
        )
        projectionText.setTextColor(statusColor(projecting, false))
        startProjectionBtn.isEnabled = !projecting
        stopProjectionBtn.isEnabled = projecting

        val autoGrant = projectMediaAllowed()
        appOpsText.text = if (autoGrant) "录屏免确认（PROJECT_MEDIA）：已允许" else "录屏免确认（PROJECT_MEDIA）：未设置（每次需在系统弹窗确认）"
        appOpsText.setTextColor(statusColor(autoGrant, false))

        loopText.text = if (!FrameLoop.running) {
            "识别：未运行（开启无障碍服务后自动开始）"
        } else {
            val st = FrameLoop.stats()
            val method = when (st.lastMethod) {
                NativeCore.METHOD_PROJECTION -> "录屏"
                NativeCore.METHOD_SCREENSHOT -> "无障碍截屏"
                NativeCore.METHOD_BUFFER -> "内存帧"
                else -> "—"
            }
            val size = if (st.lastWidth > 0) " ${st.lastWidth}×${st.lastHeight}" else ""
            String.format(
                Locale.ROOT, "识别：已分析 %d 帧 · 丢弃 %d · 平均 %.1fms · %s%s",
                st.analyzedFrames, st.droppedFrames, st.avgAnalyzeMillis, method, size,
            )
        }
        loopText.setTextColor(statusColor(FrameLoop.running, false))

        val side = when (session.side) {
            "left" -> "我方在左边"
            "right" -> "我方在右边"
            else -> "待认边"
        }
        matchText.text = (if (session.fighting) "对局中 · " else "未在对局 · ") + side
        matchText.setTextColor(TimerColors.TAG_IDLE)
    }

    private fun statusColor(ok: Boolean, bad: Boolean): Int = when {
        ok -> COLOR_OK
        bad -> COLOR_BAD
        else -> COLOR_WARN
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
        val tv = TextView(this).apply {
            text = cmd
            typeface = Typeface.MONOSPACE
            textSize = 12f
            setTextIsSelectable(true)
            setTextColor(TimerColors.CLOCK_IDLE)
            setBackgroundColor(TimerColors.PANEL_BG)
            setPadding(dp(8), dp(6), dp(8), dp(6))
        }
        val copy = button("复制") {
            val cm = getSystemService(ClipboardManager::class.java)
            cm.setPrimaryClip(ClipData.newPlainText("adb", cmd))
            toast("已复制")
        }
        return LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(tv, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
            addView(copy)
        }
    }

    // ---- 账号名图片 ----

    private fun identityDir(): File = File(filesDir, IDENTITY_DIR)

    private fun countImages(dir: File): Int =
        dir.listFiles()?.count { it.isFile && isIdentityImage(it.name) } ?: 0

    private fun refreshIdentityCount() {
        val dir = identityDir()
        val mine = countImages(dir)
        val seen = countImages(File(dir, "seen"))
        val fight = countImages(File(dir, "fight"))
        identityText.text = "已导入 $mine 张 · 自动收集 $seen 张" + (if (fight > 0) " · 对局内 $fight 张" else "")
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
        if (uris.isEmpty()) return
        val label = pendingIdentityLabel
        val resolver = contentResolver
        val dir = identityDir()
        Thread({
            var ok = 0
            var skipped = 0
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
                    resolver.openInputStream(uri)?.use { input -> out.outputStream().use { input.copyTo(it) } }
                        ?: throw java.io.IOException("open failed")
                    ok++
                } catch (e: Exception) {
                    Log.e(TAG, "import $uri failed", e)
                    out.delete()
                    skipped++
                }
            }
            main.post {
                if (ok > 0) app.reloadNative()
                refreshIdentityCount()
                refreshStatus()
                toast("已导入 $ok 张" + (if (skipped > 0) "，跳过 $skipped 张（只支持 PNG/JPG）" else "") +
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

    // ---- 小部件 ----

    private fun dp(v: Int): Int = (v * density).roundToInt()

    private fun text(s: String, sizeSp: Float, bold: Boolean = false): TextView = TextView(this).apply {
        text = s
        textSize = sizeSp
        if (bold) setTypeface(typeface, Typeface.BOLD)
        setPadding(0, dp(2), 0, dp(2))
    }

    private fun note(s: String): TextView = text(s, 13f).apply { alpha = 0.75f }

    private fun section(s: String): TextView = text(s, 16f, bold = true).apply {
        setPadding(0, dp(18), 0, dp(4))
        setTextColor(TimerColors.CLOCK_LIVE)
    }

    /** A compact, readable surface that remains usable in both portrait and landscape. */
    private fun card(): LinearLayout = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(14), dp(12), dp(14), dp(14))
        background = GradientDrawable().apply {
            setColor(TimerColors.PANEL_BG)
            setStroke(dp(1), TimerColors.PANEL_BORDER)
            cornerRadius = dp(12).toFloat()
        }
    }

    private fun button(label: String, onClick: () -> Unit): Button = Button(this).apply {
        text = label
        isAllCaps = false
        minHeight = dp(52)
        minimumHeight = dp(52)
        setPadding(dp(12), dp(4), dp(12), dp(4))
        setOnClickListener { onClick() }
    }

    private fun row(vararg views: View): View = LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        for ((i, v) in views.withIndex()) {
            addView(v, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
                if (i > 0) marginStart = dp(6)
            })
        }
    }

    private fun check(label: String, onChange: (Boolean) -> Unit): CheckBox = CheckBox(this).apply {
        text = label
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
        private const val IDENTITY_DIR = "identity"
        /** AppOpsManager.OPSTR_PROJECT_MEDIA（隐藏常量）。 */
        private const val OPSTR_PROJECT_MEDIA = "android:project_media"

        // Go settings.go：minimumOverlayOpacity…maximumOverlayOpacity、minimumMiniOpacity…、minimumOverlayScale…
        private const val WINDOW_OPACITY_MIN = 0.40
        private const val WINDOW_OPACITY_MAX = 1.00
        private const val MINI_OPACITY_MIN = 0.20
        private const val MINI_OPACITY_MAX = 1.00
        private const val OPACITY_STEP = 0.05
        private const val FONT_SCALE_MIN = 0.55
        private const val FONT_SCALE_MAX = 1.20
        private const val FONT_SCALE_STEP = 0.05

        private const val COLOR_OK = 0xFF7EE8A0.toInt()
        private const val COLOR_WARN = 0xFFFFD27E.toInt()
        private const val COLOR_BAD = 0xFFFF8A80.toInt()

        /** Go sideSel 选项。 */
        private val SIDE_LABELS = arrayOf("自动认边", "左边", "右边")
        /** Go miniSel 选项。 */
        private val MODE_LABELS = arrayOf("完整浮窗", "迷你窗口")
        /** Go ninjaEntry 选项（除空串外）。 */
        private val NINJA_CHOICES = arrayOf(
            NinjaRules.FIFTH_MIZUKAGE, NinjaRules.HASHIRAMA, NinjaRules.MADARA, NinjaRules.OBITO, NinjaRules.NARUTO,
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
