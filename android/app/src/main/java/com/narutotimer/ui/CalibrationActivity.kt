package com.narutotimer.ui

// 区域校准（docs/区域校准.md）：在一帧游戏画面上框出左右两侧的豆和头像。
//
// 画面来源：
//  - live：游戏内设置 → 位置 →「区域校准」。OverlayController 先关掉面板、藏起计时卡片，
//    再用 FrameLoop.requestSnapshot 取下一帧（录屏或无障碍截屏），连同该帧识别出的布局打开本页。
//  - file：主界面高级设置 →「区域校准」→ 载入截图；布局由用户选（训练场 / 决斗场）。
// 画面存成 cacheDir/calibration 下的文件，路径在 Intent 里，旋转/进程重建后照常恢复；
// 未保存的框在 onSaveInstanceState 里。「重新取一帧」把未保存的框带给下一次截取。

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.RectF
import android.os.Bundle
import android.util.Log
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.view.WindowManager
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import com.narutotimer.Settings
import com.narutotimer.TimerApp
import com.narutotimer.capture.FrameLoop
import com.narutotimer.service.TimerAccessibilityService
import com.narutotimer.ui.CalibrationView.Companion.COUNT
import com.narutotimer.ui.CalibrationView.Companion.LABELS
import java.io.File

class CalibrationActivity : Activity() {
    private lateinit var settings: Settings
    private lateinit var board: CalibrationView
    private lateinit var message: TextView
    private lateinit var fitButton: Button
    private lateinit var profileText: TextView
    private lateinit var profileGroup: RadioGroup
    private lateinit var campRadio: RadioButton
    private lateinit var duelRadio: RadioButton
    private lateinit var regionHint: TextView
    private lateinit var clearButton: Button
    private val chips = arrayOfNulls<LinearLayout>(COUNT)
    private val chipStates = arrayOfNulls<TextView>(COUNT)

    private var source = SOURCE_LIVE
    private var framePath: String? = null
    /** Profile analysed on the live frame ("" = not recognised as a match). */
    private var detected = ""
    /** Profile the boxes will be saved with: detected, or picked by the user. */
    private var choice = ""
    private var choiceByUser = false
    /** Saved boxes (x,y,w,h); untouched ones are kept on save. */
    private val stored = arrayOfNulls<FloatArray>(COUNT)
    private val touched = BooleanArray(COUNT)
    private var selected = 0
    private var frameReady = false
    private var loadRun = 0
    private var syncing = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        settings = (application as TimerApp).settings
        source = savedInstanceState?.getString(STATE_SOURCE) ?: intent.getStringExtra(EXTRA_SOURCE) ?: SOURCE_LIVE
        framePath = savedInstanceState?.getString(STATE_FRAME) ?: intent.getStringExtra(EXTRA_FRAME)
        detected = profileOrEmpty(savedInstanceState?.getString(STATE_DETECTED) ?: intent.getStringExtra(EXTRA_PROFILE))
        readStored()
        // Full screen like the game: the frame gets every pixel; cutouts are padded below.
        @Suppress("DEPRECATION") // Edge-to-edge only becomes the default at targetSdk 35.
        window.setDecorFitsSystemWindows(false)
        window.attributes = window.attributes.apply {
            layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES
        }
        setContentView(buildContent())
        hideSystemBars()

        for (i in 0 until COUNT) board.setBox(i, stored[i]?.let(::toRect))
        choice = if (source == SOURCE_LIVE) detected else ""
        if (savedInstanceState != null) {
            readEdits(savedInstanceState)
            choice = profileOrEmpty(savedInstanceState.getString(STATE_CHOICE))
            choiceByUser = savedInstanceState.getBoolean(STATE_CHOICE_BY_USER)
        } else {
            intent.getBundleExtra(EXTRA_DRAFT)?.let { draft ->
                readEdits(draft)
                // A new frame's own profile wins; otherwise keep what the user picked before.
                if (choice.isEmpty() && draft.getBoolean(STATE_CHOICE_BY_USER)) {
                    choice = profileOrEmpty(draft.getString(STATE_CHOICE))
                    choiceByUser = choice.isNotEmpty()
                }
            }
        }
        board.selected = selected
        updateRegions()

        val path = framePath
        when {
            path != null -> loadFrame(path)
            source == SOURCE_FILE -> {
                showMessage("请选择一张对局或训练场的整屏截图")
                if (savedInstanceState == null) pickFile()
            }
            else -> showMessage("没有画面，请点「重新取一帧」")
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putString(STATE_SOURCE, source)
        outState.putString(STATE_FRAME, framePath)
        outState.putString(STATE_DETECTED, detected)
        writeEdits(outState)
    }

    override fun onStart() {
        super.onStart()
        // The in-game card is an overlay above this screen; keep it off the frame.
        TimerAccessibilityService.setCalibrationEditorShown(true)
    }

    override fun onStop() {
        super.onStop()
        TimerAccessibilityService.setCalibrationEditorShown(false)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus) hideSystemBars()
    }

    override fun onDestroy() {
        super.onDestroy()
        if (isFinishing) framePath?.let(CalibrationFrames::discard)
    }

    @Deprecated("Activity back API without androidx")
    override fun onBackPressed() = leave()

    // ---- Layout -----------------------------------------------------------

    private fun buildContent(): View {
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            setBackgroundColor(Ui.BG)
            setOnApplyWindowInsetsListener { v, insets ->
                // Bars are hidden; keep the board and panel clear of cutouts.
                val i = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout())
                v.setPadding(i.left, i.top, i.right, i.bottom)
                WindowInsets.CONSUMED
            }
        }

        val area = FrameLayout(this)
        board = CalibrationView(this).apply { listener = boardListener }
        area.addView(board, FrameLayout.LayoutParams(MATCH, MATCH))
        message = Ui.text(this, "", 14f, Ui.TEXT_2).apply {
            gravity = Gravity.CENTER
            setPadding(dp(24f), 0, dp(24f), 0)
        }
        area.addView(message, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.CENTER))
        val gestures = Ui.text(this, "拖动画框 · 拖边角微调 · 双指缩放 · 双击放大", 12f, Ui.TEXT).apply {
            background = Ui.rounded(this@CalibrationActivity, HINT_BG, 10f)
            setPadding(dp(10f), dp(5f), dp(10f), dp(5f))
        }
        area.addView(gestures, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.BOTTOM or Gravity.START).apply {
            setMargins(dp(12f), 0, 0, dp(10f))
        })
        fitButton = Ui.button(this, "适应屏幕") { board.fit() }.apply { visibility = View.GONE }
        area.addView(fitButton, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.BOTTOM or Gravity.END).apply {
            setMargins(0, 0, dp(12f), dp(8f))
        })
        root.addView(area, LinearLayout.LayoutParams(0, MATCH, 1f))

        val panel = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(12f), dp(10f), dp(12f), dp(10f))
            setBackgroundColor(Ui.SURFACE)
        }
        val body = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        Ui.add(body, Ui.title(this, "区域校准"), 0f)
        profileText = Ui.text(this, "", 12.5f, Ui.TEXT_2)
        Ui.add(body, profileText, 4f)
        campRadio = radio("训练场")
        duelRadio = radio("决斗场")
        profileGroup = RadioGroup(this).apply {
            orientation = RadioGroup.HORIZONTAL
            addView(campRadio, RadioGroup.LayoutParams(0, WRAP, 1f))
            addView(duelRadio, RadioGroup.LayoutParams(0, WRAP, 1f))
            setOnCheckedChangeListener { _, id ->
                if (syncing) return@setOnCheckedChangeListener
                choice = when (id) {
                    campRadio.id -> "camp"
                    duelRadio.id -> "duel"
                    else -> ""
                }
                choiceByUser = true
                updateProfile()
            }
        }
        Ui.add(body, profileGroup, 0f)
        Ui.add(body, Ui.text(this, "要框的区域", 13f, Ui.TEXT_2), 6f)
        for (i in 0 until COUNT) chips[i] = chip(i)
        Ui.add(body, Ui.row(this, chips[0]!!, chips[1]!!, gapDp = 6f), 4f)
        Ui.add(body, Ui.row(this, chips[2]!!, chips[3]!!, gapDp = 6f), 6f)
        regionHint = Ui.note(this, "")
        Ui.add(body, regionHint, 8f)
        clearButton = Ui.button(this, "清除所选") { clearSelected() }
        Ui.add(body, Ui.row(
            this,
            Ui.button(this, if (source == SOURCE_FILE) "换一张截图" else "重新取一帧") { retake() },
            clearButton,
            gapDp = 6f,
        ), 10f)
        Ui.add(body, Ui.button(this, "全部恢复自动", Ui.Kind.DANGER) { confirmResetAll() }, 6f)
        panel.addView(ScrollView(this).apply {
            isVerticalScrollBarEnabled = false
            addView(body, ViewGroup.LayoutParams(MATCH, WRAP))
        }, LinearLayout.LayoutParams(MATCH, 0, 1f))
        panel.addView(Ui.row(
            this,
            Ui.button(this, "返回") { leave() },
            Ui.button(this, "保存", Ui.Kind.PRIMARY) { save() },
            gapDp = 6f,
        ), LinearLayout.LayoutParams(MATCH, WRAP).apply { topMargin = dp(8f) })
        root.addView(panel, LinearLayout.LayoutParams(dp(PANEL_WIDTH_DP), MATCH))
        return root
    }

    private fun chip(i: Int): LinearLayout {
        val state = Ui.text(this, "", 11.5f, Ui.TEXT_2)
        chipStates[i] = state
        return LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_VERTICAL
            minimumHeight = dp(48f)
            setPadding(dp(9f), dp(5f), dp(6f), dp(5f))
            isClickable = true
            isFocusable = true
            addView(Ui.text(this@CalibrationActivity, LABELS[i], 13.5f, CalibrationView.COLORS[i], bold = true))
            addView(state)
            setOnClickListener { select(i) }
        }
    }

    private fun radio(label: String) = RadioButton(this).apply {
        id = View.generateViewId()
        text = label
        minHeight = dp(44f)
        setTextColor(Ui.TEXT)
    }

    private fun hideSystemBars() {
        window.insetsController?.let {
            it.systemBarsBehavior = WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            it.hide(WindowInsets.Type.systemBars())
        }
    }

    // ---- State ------------------------------------------------------------

    private fun readStored() {
        for (i in 0 until COUNT) stored[i] = settings.calibRect(Settings.KEY_CALIB_RECTS[i])
    }

    /** Unsaved edits (also the draft carried across 重新取一帧). */
    private fun writeEdits(out: Bundle) {
        out.putInt(STATE_SELECTED, selected)
        out.putBooleanArray(STATE_TOUCHED, touched.copyOf())
        for (i in 0 until COUNT) {
            val r = board.box(i)
            if (touched[i] && r != null) out.putFloatArray(STATE_RECT + i, floatArrayOf(r.left, r.top, r.right, r.bottom))
        }
        out.putString(STATE_CHOICE, choice)
        out.putBoolean(STATE_CHOICE_BY_USER, choiceByUser)
    }

    private fun readEdits(state: Bundle) {
        selected = state.getInt(STATE_SELECTED, 0).coerceIn(0, COUNT - 1)
        val t = state.getBooleanArray(STATE_TOUCHED) ?: return
        for (i in 0 until COUNT) {
            if (i >= t.size || !t[i]) continue
            touched[i] = true
            val v = state.getFloatArray(STATE_RECT + i)
            board.setBox(i, if (v != null && v.size == 4) RectF(v[0], v[1], v[2], v[3]) else null)
        }
    }

    private fun dirty() = touched.any { it } || choiceByUser

    private fun select(i: Int) {
        selected = i
        board.selected = i
        updateRegions()
    }

    private fun updateRegions() {
        for (i in 0 until COUNT) {
            val color = CalibrationView.COLORS[i]
            chips[i]?.background = if (i == selected) {
                Ui.rounded(this, (color and 0x00FFFFFF) or 0x33000000, 10f, color)
            } else {
                Ui.rounded(this, Ui.SURFACE_2, 10f)
            }
            val has = board.box(i) != null
            val (text, tint) = when {
                !has && touched[i] && stored[i] != null -> "将恢复自动" to Ui.WARN
                !has -> "自动" to Ui.TEXT_2
                touched[i] -> "已修改" to Ui.WARN
                else -> "已校准" to Ui.OK
            }
            chipStates[i]?.text = text
            chipStates[i]?.setTextColor(tint)
        }
        regionHint.text = REGION_HINTS[selected]
        clearButton.isEnabled = board.box(selected) != null
        updateProfile()
    }

    /** Saved boxes the user has not redrawn or cleared here; they keep their own layout. */
    private fun untouchedStored(): List<Int> = (0 until COUNT).filter { !touched[it] && stored[it] != null }

    private fun updateProfile() {
        syncing = true
        profileGroup.check(
            when (choice) {
                "camp" -> campRadio.id
                "duel" -> duelRadio.id
                else -> -1
            },
        )
        syncing = false
        val (text, color) = when {
            source == SOURCE_FILE && choice.isEmpty() -> "这张截图是哪种画面？请选择：" to Ui.WARN
            source == SOURCE_FILE -> "画面：${profileName(choice)}（手动选择）" to Ui.TEXT_2
            choice.isEmpty() -> "未识别为对局画面。请在对局或训练场画面上校准，或手动选择：" to Ui.BAD
            detected.isEmpty() -> "画面：${profileName(choice)}（手动选择）" to Ui.WARN
            choice == detected -> "画面：${profileName(choice)}（自动识别）" to Ui.OK
            else -> "画面：${profileName(choice)}（手动选择，识别为${profileName(detected)}）" to Ui.WARN
        }
        val savedProfile = settings.calibProfile
        val note = if (choice.isNotEmpty() && savedProfile != choice && untouchedStored().isNotEmpty()) {
            "\n已保存的框画在${profileName(savedProfile)}画面上。"
        } else ""
        profileText.text = text + note
        profileText.setTextColor(color)
    }

    private val boardListener = object : CalibrationView.Listener {
        override fun onBoxEdited(index: Int) {
            touched[index] = true
            updateRegions()
        }

        override fun onBoxTapped(index: Int) = select(index)

        override fun onZoomChanged(zoomed: Boolean) {
            fitButton.visibility = if (zoomed) View.VISIBLE else View.GONE
        }
    }

    // ---- Frame ------------------------------------------------------------

    private fun showMessage(text: String) {
        message.text = text
        message.visibility = View.VISIBLE
    }

    private fun loadFrame(path: String) {
        val run = ++loadRun
        frameReady = false
        board.setImage(null)
        showMessage("正在载入画面…")
        CalibrationFrames.held(path)?.let {
            onFrame(path, it)
            return
        }
        Thread({
            val bitmap = CalibrationFrames.decode(File(path))
            runOnUiThread { if (!isDestroyed && run == loadRun) onFrame(path, bitmap) }
        }, "nt-calib-load").start()
    }

    private fun onFrame(path: String, bitmap: Bitmap?) {
        if (bitmap == null) {
            showMessage(if (source == SOURCE_FILE) "无法读取这张截图，请换一张" else "画面已失效，请点「重新取一帧」")
            return
        }
        framePath = path
        frameReady = true
        message.visibility = View.GONE
        board.setImage(bitmap)
    }

    private fun pickFile() {
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .setType("image/*")
        try {
            @Suppress("DEPRECATION")
            startActivityForResult(intent, REQUEST_PICK)
        } catch (e: RuntimeException) {
            Log.e(TAG, "open document failed", e)
            toast("没有可用的文件选择器")
            if (framePath == null) finish()
        }
    }

    @Deprecated("Activity result API without androidx")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != REQUEST_PICK) return
        val uri = data?.data
        if (resultCode != RESULT_OK || uri == null) {
            if (framePath == null) finish()
            return
        }
        val run = ++loadRun
        showMessage("正在载入截图…")
        Thread({
            val file = CalibrationFrames.import(this, uri)
            val bitmap = file?.let(CalibrationFrames::decode)
            runOnUiThread {
                if (isDestroyed || run != loadRun) {
                    file?.delete()
                    return@runOnUiThread
                }
                if (file == null || bitmap == null) {
                    file?.delete()
                    if (frameReady) {
                        message.visibility = View.GONE
                        toast("无法读取这张图片，请换一张截图")
                    } else {
                        showMessage("无法读取这张图片，请换一张截图")
                    }
                    return@runOnUiThread
                }
                CalibrationFrames.keepOnly(this, file.path)
                onFrame(file.path, bitmap)
            }
        }, "nt-calib-import").start()
    }

    // ---- Actions ----------------------------------------------------------

    private fun retake() {
        if (source == SOURCE_FILE) {
            pickFile()
            return
        }
        val service = TimerAccessibilityService.instance
        if (service == null || !service.isOverlayRunning || !FrameLoop.running) {
            AlertDialog.Builder(this)
                .setTitle("识别没有运行")
                .setMessage("请先在主界面启动悬浮计时，进入对局或训练场后，从计时卡片的 设置 → 位置 →「区域校准」重新打开。")
                .setPositiveButton("知道了", null)
                .show()
            return
        }
        val draft = Bundle().also(::writeEdits)
        // This screen covers the game: close it first, the overlay captures once the game is back.
        if (service.startCalibration(draft, RETAKE_DELAY_MS)) finish() else toast("无法截取游戏画面，请稍后重试")
    }

    private fun clearSelected() {
        if (board.box(selected) == null) return
        board.setBox(selected, null)
        touched[selected] = true
        updateRegions()
    }

    private fun confirmResetAll() {
        AlertDialog.Builder(this)
            .setTitle("全部恢复自动")
            .setMessage("清除已保存的 4 个框，豆和头像改回自动定位？")
            .setPositiveButton("恢复自动") { _, _ -> resetAll() }
            .setNegativeButton("取消", null)
            .show()
    }

    private fun resetAll() {
        for (key in Settings.KEY_CALIB_RECTS) settings.setCalibRect(key, null)
        readStored()
        for (i in 0 until COUNT) {
            board.setBox(i, null)
            touched[i] = false
        }
        updateRegions()
        toast("已全部恢复自动")
    }

    private fun save() {
        if (!frameReady) {
            toast("还没有画面")
            return
        }
        val profile = choice
        if (profile != "camp" && profile != "duel") {
            AlertDialog.Builder(this)
                .setTitle("请在对局或训练场画面上校准")
                .setMessage(
                    if (source == SOURCE_FILE) {
                        "先选择这张截图是训练场还是决斗场。"
                    } else {
                        "这一帧没有识别为对局或训练场。回到对局后点「重新取一帧」；如果确实是对局画面，可手动选择训练场或决斗场。"
                    },
                )
                .setPositiveButton("知道了", null)
                .show()
            return
        }
        // One profile covers all boxes: untouched ones drawn on the other layout cannot stay.
        val savedProfile = settings.calibProfile
        val stale = untouchedStored()
        if (stale.isNotEmpty() && savedProfile != profile) {
            AlertDialog.Builder(this)
                .setTitle("布局不同")
                .setMessage(
                    "${stale.joinToString("、") { LABELS[it] }}是在${profileName(savedProfile)}画面上框的，" +
                        "与这一帧（${profileName(profile)}）不同。保存后会恢复自动，可在这一帧上重新框。",
                )
                .setPositiveButton("保存") { _, _ -> commit(profile, stale) }
                .setNegativeButton("取消", null)
                .show()
            return
        }
        commit(profile, emptyList())
    }

    private fun commit(profile: String, clear: List<Int>) {
        // Profile first: listeners reacting to a box already see the matching layout.
        settings.calibProfile = profile
        for (i in 0 until COUNT) {
            val key = Settings.KEY_CALIB_RECTS[i]
            when {
                touched[i] -> settings.setCalibRect(key, board.box(i)?.let(::toStored))
                i in clear -> settings.setCalibRect(key, null)
            }
        }
        toast("区域校准已保存")
        finish()
    }

    private fun leave() {
        if (!dirty()) {
            finish()
            return
        }
        AlertDialog.Builder(this)
            .setTitle("放弃修改？")
            .setMessage("还没保存的框不会生效。")
            .setPositiveButton("放弃") { _, _ -> finish() }
            .setNegativeButton("继续编辑", null)
            .show()
    }

    private fun toast(s: String) = Toast.makeText(this, s, Toast.LENGTH_SHORT).show()

    private fun dp(v: Float): Int = Ui.dp(this, v)

    companion object {
        private const val TAG = "CalibrationActivity"
        const val EXTRA_SOURCE = "com.narutotimer.calibration.SOURCE"
        const val EXTRA_FRAME = "com.narutotimer.calibration.FRAME"
        const val EXTRA_PROFILE = "com.narutotimer.calibration.PROFILE"
        const val EXTRA_DRAFT = "com.narutotimer.calibration.DRAFT"
        const val SOURCE_LIVE = "live"
        const val SOURCE_FILE = "file"
        /** From the in-game panel: wait until the panel and its scrim are off the screen. */
        const val PANEL_DELAY_MS = 350L
        /** 重新取一帧: time for this screen to close and the game to draw again. */
        const val RETAKE_DELAY_MS = 1_000L

        private const val REQUEST_PICK = 51
        private const val PANEL_WIDTH_DP = 224f
        private const val HINT_BG = 0xCC071219.toInt()
        private const val MATCH = ViewGroup.LayoutParams.MATCH_PARENT
        private const val WRAP = ViewGroup.LayoutParams.WRAP_CONTENT
        private const val STATE_SOURCE = "source"
        private const val STATE_FRAME = "frame"
        private const val STATE_DETECTED = "detected"
        private const val STATE_CHOICE = "choice"
        private const val STATE_CHOICE_BY_USER = "choiceByUser"
        private const val STATE_SELECTED = "selected"
        private const val STATE_TOUCHED = "touched"
        private const val STATE_RECT = "rect"

        private val REGION_HINTS = arrayOf(
            "框住左边 4 颗豆（柱间的第 5、6 颗不框）。圆点是推算的豆心，1 号在最左。",
            "框住右边 4 颗豆（柱间的第 5、6 颗不框）。圆点是推算的豆心，1 号在最右。",
            "紧贴左边的头像，不含名字和血条。",
            "紧贴右边的头像，不含名字和血条。",
        )

        /**
         * Opens the editor on a live snapshot (takes ownership of [snapshot]). Called from the
         * overlay, so it starts its own task; when it closes, the game is in front again.
         */
        fun openLive(context: Context, snapshot: Bitmap, profile: String, draft: Bundle?): Boolean {
            val file = CalibrationFrames.holdLive(context, snapshot)
            val intent = Intent(context, CalibrationActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
                .putExtra(EXTRA_SOURCE, SOURCE_LIVE)
                .putExtra(EXTRA_FRAME, file.path)
                .putExtra(EXTRA_PROFILE, profile)
            if (draft != null) intent.putExtra(EXTRA_DRAFT, draft)
            return try {
                context.startActivity(intent)
                true
            } catch (e: RuntimeException) {
                Log.e(TAG, "open calibration failed", e)
                CalibrationFrames.discard(file.path)
                false
            }
        }

        /** Opens the editor on a screenshot the user picks (layout chosen by hand). */
        fun openFile(context: Context) {
            context.startActivity(Intent(context, CalibrationActivity::class.java).putExtra(EXTRA_SOURCE, SOURCE_FILE))
        }

        fun profileName(profile: String): String = if (profile == "camp") "训练场" else "决斗场"

        /** One line for the settings screens. */
        fun summary(settings: Settings): String {
            val set = (0 until COUNT).filter { settings.calibRect(Settings.KEY_CALIB_RECTS[it]) != null }
            if (set.isEmpty()) return "自动（未校准）"
            return "已校准 " + set.joinToString("、") { LABELS[it] } + "（${profileName(settings.calibProfile)}画面）"
        }

        private fun profileOrEmpty(p: String?): String = when (p) {
            "camp" -> "camp"
            "duel" -> "duel"
            else -> ""
        }

        private fun toRect(v: FloatArray) = RectF(v[0], v[1], v[0] + v[2], v[1] + v[3])

        /** x,y,w,h fractions (5 decimals) that pass Settings.calibRect's checks in Float arithmetic. */
        private fun toStored(r: RectF): FloatArray? {
            val x = round5(r.left.coerceIn(0f, 1f))
            val y = round5(r.top.coerceIn(0f, 1f))
            var w = round5(r.right.coerceIn(0f, 1f) - x).coerceAtMost(1f - x)
            var h = round5(r.bottom.coerceIn(0f, 1f) - y).coerceAtMost(1f - y)
            while (w > 0f && x + w > 1f) w = Math.nextDown(w)
            while (h > 0f && y + h > 1f) h = Math.nextDown(h)
            if (w <= 0f || h <= 0f) return null
            return floatArrayOf(x, y, w, h)
        }

        private fun round5(v: Float): Float = Math.round(v * 100_000f) / 100_000f
    }
}
