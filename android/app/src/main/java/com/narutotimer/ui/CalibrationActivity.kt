package com.narutotimer.ui

// 区域校准（docs/区域校准.md）：在一帧游戏画面上框出左右两侧的豆和头像。
//
// 画面来源：
//  - live：游戏内设置 → 位置 →「区域校准」。OverlayController 先关掉面板、藏起计时卡片，
//    再用 FrameLoop.requestSnapshot 取下一帧（录屏或无障碍截屏），连同该帧识别出的布局和识别在
//    这一帧实际用的几何（NativeCore.lastGeometry，同一帧）打开本页。
//  - file：主界面高级设置 →「区域校准」→ 载入截图；布局由用户选（训练场 / 决斗场），没有自动识别结果。
// 画面存成 cacheDir/calibration 下的文件（几何在旁边的 .json），路径在 Intent 里，旋转/进程重建后照常
// 恢复；未保存的修改在 onSaveInstanceState 里。「重新取一帧」把未保存的修改带给下一次截取。
//
// 默认方案 = 自动：没保存过、也没改过的框从这一帧的 beadBox/avatarBox 开始，标「（自动）」。只有动过
// （画、移动、缩放、导入、清除）的框才写入设置；什么都没改，保存不写任何设置，识别一切照旧。
// 方案文件（导出/导入）的格式见 CalibrationScheme。
//
// 校准值是标准忍者值：保存的豆框永远是普通忍者的豆行。这一帧某侧采样的豆比标准行低（识别出水门/佐助/鼬
// 的整行下移）时，这一侧的豆框（自动、已保存、未保存、导入的）在画板上下移到看得见的豆上，在这一帧画/改的
// 豆框按同一下移换回标准值（CalibrationRow）。stored/pending/imported/autoBoxes 一律是标准值，只有画板上
// 是下移后的位置（shownRect / standardRect）；头像框不动。

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.RectF
import android.net.Uri
import android.os.Bundle
import android.util.Log
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.view.WindowManager
import android.widget.Button
import android.widget.CheckBox
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import com.narutotimer.FrameBox
import com.narutotimer.FrameGeometry
import com.narutotimer.PixelRect
import com.narutotimer.Settings
import com.narutotimer.TimerApp
import com.narutotimer.capture.FrameLoop
import com.narutotimer.service.TimerAccessibilityService
import com.narutotimer.tracker.NinjaRules
import com.narutotimer.ui.CalibrationView.Companion.COUNT
import com.narutotimer.ui.CalibrationView.Companion.LABELS
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import java.util.Locale

class CalibrationActivity : Activity() {
    private lateinit var settings: Settings
    private lateinit var board: CalibrationView
    private lateinit var message: TextView
    private lateinit var fitButton: Button
    private lateinit var profileText: TextView
    private lateinit var profileGroup: RadioGroup
    private lateinit var campRadio: RadioButton
    private lateinit var duelRadio: RadioButton
    private lateinit var autoCheck: CheckBox
    private lateinit var autoText: TextView
    private lateinit var sideText: TextView
    private lateinit var regionHint: TextView
    private lateinit var clearButton: Button
    private val chips = arrayOfNulls<LinearLayout>(COUNT)
    private val chipStates = arrayOfNulls<TextView>(COUNT)

    private var source = SOURCE_LIVE
    private var framePath: String? = null
    /** Profile analysed on the live frame ("" = not recognised as a match). */
    private var detected = ""
    /** Profile changed boxes are saved with: detected, picked by the user or from a scheme file. */
    private var choice = ""
    private var choiceByUser = false
    /** [choice] came from an imported scheme file (until the user picks again). */
    private var choiceImported = false
    /** Saved boxes (x,y,w,h); null = automatic. */
    private val stored = arrayOfNulls<FloatArray>(COUNT)
    /** Per box: [EDIT_NONE] (saved box or automatic), [EDIT_SET] ([pending] box), [EDIT_CLEAR] (back to automatic). */
    private val edits = IntArray(COUNT)
    private val pending = arrayOfNulls<RectF>(COUNT)
    /** A pending box exactly as an imported scheme file has it (saved as is) until it is moved. */
    private val imported = arrayOfNulls<FloatArray>(COUNT)
    /** What the engine used on this frame (live frames with geometry only). */
    private var auto: CalibrationFrames.Auto? = null
    /** This frame's automatic boxes (default scheme); null = unknown (no geometry, or calibrated then). */
    private val autoBoxes = arrayOfNulls<RectF>(COUNT)
    /** Per side (0 left, 1 right): this frame's bead row below the standard row, in pitches (CalibrationRow.drop). */
    private val drops = DoubleArray(2)
    /** Per side: the ninja recognised on this frame ("" = none or no geometry). */
    private val ninjas = arrayOf("", "")
    /** Size of the analysed frame (the shown bitmap may be scaled down). */
    private var frameWidth = 0
    private var frameHeight = 0
    private var showAuto = true
    /** Scheme text waiting for its export document (kept across recreation while the picker is open). */
    private var exportText: String? = null
    private var exportUnsaved = false
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

        choice = if (source == SOURCE_LIVE) detected else ""
        if (savedInstanceState != null) {
            readEdits(savedInstanceState)
            choice = profileOrEmpty(savedInstanceState.getString(STATE_CHOICE))
            choiceByUser = savedInstanceState.getBoolean(STATE_CHOICE_BY_USER)
            choiceImported = savedInstanceState.getBoolean(STATE_CHOICE_IMPORTED)
            exportText = savedInstanceState.getString(STATE_EXPORT)
            exportUnsaved = savedInstanceState.getBoolean(STATE_EXPORT_UNSAVED)
        } else {
            intent.getBundleExtra(EXTRA_DRAFT)?.let { draft ->
                readEdits(draft)
                // A new frame's own profile wins; otherwise keep what the user picked before.
                if (choice.isEmpty() && draft.getBoolean(STATE_CHOICE_BY_USER)) {
                    choice = profileOrEmpty(draft.getString(STATE_CHOICE))
                    choiceByUser = choice.isNotEmpty()
                    choiceImported = choiceByUser && draft.getBoolean(STATE_CHOICE_IMPORTED)
                }
            }
        }
        normalizeEdits()
        syncing = true
        autoCheck.isChecked = showAuto
        syncing = false
        board.showAuto = showAuto
        board.selected = selected
        syncBoard()
        updateRegions()
        updateAuto()

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
        outState.putString(STATE_EXPORT, exportText)
        outState.putBoolean(STATE_EXPORT_UNSAVED, exportUnsaved)
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
                choiceImported = false
                updateProfile()
            }
        }
        Ui.add(body, profileGroup, 0f)
        autoCheck = CheckBox(this).apply {
            text = "显示自动识别区域"
            textSize = 13f
            minHeight = dp(40f)
            setTextColor(Ui.TEXT)
            setOnCheckedChangeListener { _, on ->
                if (syncing) return@setOnCheckedChangeListener
                showAuto = on
                board.showAuto = on
            }
        }
        Ui.add(body, autoCheck, 2f)
        autoText = Ui.text(this, "", 11.5f, Ui.TEXT_2)
        Ui.add(body, autoText, 0f)
        sideText = Ui.text(this, "", 11.5f, Ui.TEXT_2).apply { visibility = View.GONE }
        Ui.add(body, sideText, 4f)
        Ui.add(body, Ui.text(this, "要框的区域", 13f, Ui.TEXT_2), 10f)
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
        Ui.add(body, Ui.row(
            this,
            Ui.button(this, "导出方案") { exportScheme() },
            Ui.button(this, "导入方案") { importScheme() },
            gapDp = 6f,
        ), 6f)
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
        out.putIntArray(STATE_EDITS, edits.copyOf())
        for (i in 0 until COUNT) {
            val r = pending[i]
            if (edits[i] != EDIT_SET || r == null) continue
            out.putFloatArray(STATE_RECT + i, floatArrayOf(r.left, r.top, r.right, r.bottom))
            imported[i]?.let { out.putFloatArray(STATE_EXACT + i, it) }
        }
        out.putString(STATE_CHOICE, choice)
        out.putBoolean(STATE_CHOICE_BY_USER, choiceByUser)
        out.putBoolean(STATE_CHOICE_IMPORTED, choiceImported)
        out.putBoolean(STATE_SHOW_AUTO, showAuto)
    }

    private fun readEdits(state: Bundle) {
        selected = state.getInt(STATE_SELECTED, 0).coerceIn(0, COUNT - 1)
        showAuto = state.getBoolean(STATE_SHOW_AUTO, true)
        val e = state.getIntArray(STATE_EDITS) ?: return
        for (i in 0 until COUNT) {
            when (e.getOrNull(i)) {
                EDIT_SET -> {
                    val v = state.getFloatArray(STATE_RECT + i)
                    if (v != null && v.size == 4) {
                        edits[i] = EDIT_SET
                        pending[i] = RectF(v[0], v[1], v[2], v[3])
                        imported[i] = state.getFloatArray(STATE_EXACT + i)?.takeIf(CalibrationScheme::valid)
                    }
                }
                EDIT_CLEAR -> edits[i] = EDIT_CLEAR
            }
        }
    }

    /** Clearing a box that is no longer saved is no edit. */
    private fun normalizeEdits() {
        for (i in 0 until COUNT) {
            if (edits[i] == EDIT_SET && pending[i] == null) edits[i] = EDIT_NONE
            if (edits[i] == EDIT_CLEAR && stored[i] == null) edits[i] = EDIT_NONE
            if (edits[i] != EDIT_SET) {
                pending[i] = null
                imported[i] = null
            }
        }
    }

    private fun dirty() = edits.any { it != EDIT_NONE }

    /** The box stands for the automatic position: shown as such and never saved. */
    private fun automatic(i: Int) = edits[i] == EDIT_CLEAR || (edits[i] == EDIT_NONE && stored[i] == null)

    /** Shows each box: its pending edit, the saved box, or this frame's automatic box (as shown on this frame). */
    private fun syncBoard() {
        for (i in 0 until COUNT) {
            val isAuto = automatic(i)
            val r = when {
                edits[i] == EDIT_SET -> pending[i]
                isAuto -> autoBoxes[i]
                else -> stored[i]?.let(::toRect)
            }
            board.setBox(i, r?.let { shownRect(i, it) }, isAuto)
        }
    }

    /** A standard box as the board shows it: a bead box on a dropped row sits on the visible beads. */
    private fun shownRect(i: Int, r: RectF): RectF {
        val dy = rowShift(i, r)
        return RectF(r.left, r.top + dy, r.right, r.bottom + dy)
    }

    /** A box on the board as its standard value (what is saved and exported). */
    private fun standardRect(i: Int, r: RectF): RectF {
        val dy = rowShift(i, r)
        return RectF(r.left, r.top - dy, r.right, r.bottom - dy)
    }

    /** CalibrationRow.shift of a bead box on this frame; avatar boxes never shift. */
    private fun rowShift(i: Int, r: RectF): Float = when (i) {
        CalibrationView.LEFT_BEADS -> CalibrationRow.shift(drops[0], r.width(), frameWidth, frameHeight)
        CalibrationView.RIGHT_BEADS -> CalibrationRow.shift(drops[1], r.width(), frameWidth, frameHeight)
        else -> 0f
    }

    /** Board box [shown] is the standard box [box] as shown here, or the same once stored. */
    private fun sameShown(i: Int, shown: RectF, box: RectF): Boolean =
        sameRect(shown, shownRect(i, box)) || sameRect(standardRect(i, shown), box)

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
            val (text, tint) = when {
                edits[i] == EDIT_SET -> "已修改" to Ui.WARN
                edits[i] == EDIT_CLEAR -> "将恢复自动" to Ui.WARN
                stored[i] != null -> "已校准" to Ui.OK
                else -> "自动" to Ui.TEXT_2
            }
            chipStates[i]?.text = text
            chipStates[i]?.setTextColor(tint)
        }
        regionHint.text = REGION_HINTS[selected]
        clearButton.isEnabled = edits[selected] == EDIT_SET || (edits[selected] == EDIT_NONE && stored[selected] != null)
        updateProfile()
    }

    /** Saved boxes the user has not changed here; they keep their own layout. */
    private fun untouchedStored(): List<Int> = (0 until COUNT).filter { edits[it] == EDIT_NONE && stored[it] != null }

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
            choiceImported && choice.isNotEmpty() && detected.isNotEmpty() && detected != choice ->
                "画面：${profileName(choice)}（方案文件，这一帧识别为${profileName(detected)}）" to Ui.WARN
            choiceImported && choice.isNotEmpty() -> "画面：${profileName(choice)}（方案文件）" to Ui.TEXT_2
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

    /** The automatic-result line under 「显示自动识别区域」. */
    private fun updateAuto() {
        val g = auto?.geometry
        autoCheck.isEnabled = g != null
        autoCheck.alpha = if (g != null) 1f else 0.45f
        autoText.text = when {
            g != null -> {
                val layout = if (g.layoutProfile.isEmpty()) "" else "（${profileName(g.layoutProfile)}布局）"
                "虚线是识别在这一帧实际用的位置$layout：十字是豆心，另有「名字」「头像窗口」。" +
                    "标「（自动）」的框就是自动位置，不改就不保存。"
            }
            !frameReady -> ""
            source == SOURCE_FILE -> "载入的截图没有自动识别结果（只有在游戏里截取的画面才有），框要自己画。"
            else -> "这一帧没有自动识别结果（大厅、结算等画面不识别豆和头像），框要自己画。"
        }
        // Per side: the ninja recognised on this frame and whether its bead row is converted.
        val lines = if (g == null) emptyList() else (0 until 2).mapNotNull { s ->
            val side = if (s == 0) "左侧" else "右侧"
            val name = ninjas[s].takeIf { it.isNotBlank() }?.let(NinjaRules::shortLabel)
            when {
                drops[s] > 0.0 -> "$side：${if (name != null) "$name，" else ""}豆行比普通忍者低，已按标准换算"
                name != null -> "$side：$name"
                g.side(s == 0).beads.isNotEmpty() -> "$side：没认出忍者"
                else -> null
            }
        }
        sideText.text = lines.joinToString("\n")
        sideText.setTextColor(if (drops.any { it > 0.0 }) Ui.WARN else Ui.TEXT_2)
        sideText.visibility = if (lines.isEmpty()) View.GONE else View.VISIBLE
    }

    private val boardListener = object : CalibrationView.Listener {
        override fun onBoxEdited(index: Int) {
            // [r] is where the board shows it; the boxes kept here are standard values.
            val r = board.box(index) ?: return
            val saved = stored[index]?.let(::toRect)
            val autoBox = autoBoxes[index]
            val start = when (edits[index]) {
                EDIT_SET -> pending[index]
                EDIT_CLEAR -> autoBox
                else -> saved ?: autoBox
            }
            // Moved back to exactly where it was (as it would be saved): no change at all.
            if (start != null && sameShown(index, r, start)) {
                syncBoard()
                return
            }
            edits[index] = when {
                saved != null && sameShown(index, r, saved) -> EDIT_NONE
                saved == null && autoBox != null && sameShown(index, r, autoBox) -> EDIT_NONE
                else -> EDIT_SET
            }
            // Drawn on this frame's (possibly dropped) row: kept, saved and exported as the standard row.
            pending[index] = if (edits[index] == EDIT_SET) standardRect(index, r) else null
            imported[index] = null
            syncBoard()
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
            onFrame(path, it.bitmap, it.auto)
            return
        }
        val live = source == SOURCE_LIVE
        Thread({
            val bitmap = CalibrationFrames.decode(File(path))
            val auto = if (live && bitmap != null) CalibrationFrames.readAuto(path) else null
            runOnUiThread { if (!isDestroyed && run == loadRun) onFrame(path, bitmap, auto) }
        }, "nt-calib-load").start()
    }

    private fun onFrame(path: String, bitmap: Bitmap?, auto: CalibrationFrames.Auto?) {
        if (bitmap == null) {
            showMessage(if (source == SOURCE_FILE) "无法读取这张截图，请换一张" else "画面已失效，请点「重新取一帧」")
            return
        }
        framePath = path
        frameReady = true
        message.visibility = View.GONE
        board.setImage(bitmap)
        setAuto(auto, bitmap)
    }

    /** This frame's automatic analysis: the dashed regions and the default boxes. */
    private fun setAuto(frameAuto: CalibrationFrames.Auto?, bitmap: Bitmap) {
        val g = frameAuto?.geometry?.takeIf { it.width > 0 && it.height > 0 }
        val a = if (g != null) frameAuto else null
        auto = a
        frameWidth = g?.width ?: bitmap.width
        frameHeight = g?.height ?: bitmap.height
        // 校准值是标准忍者值: how far each side's sampled row lies below its standard row here.
        for (s in 0 until 2) {
            val side = g?.side(s == 0)
            drops[s] = if (g == null || side == null) 0.0 else CalibrationRow.drop(
                IntArray(side.beads.size) { side.beads[it].y }, side.beadBox?.toFloatArray(), g.width, g.height,
            )
            ninjas[s] = a?.ninja(s).orEmpty()
        }
        if (drops.any { it > 0.0 }) {
            Log.i(TAG, "row drop left=${drops[0]} (${ninjas[0]}) right=${drops[1]} (${ninjas[1]})")
        }
        val calibrated = frameAuto?.calibrated
        for (i in 0 until COUNT) {
            // A box calibrated when the frame was analysed reports that calibration, not the automatic box.
            val box = if (g == null || calibrated == null || calibrated.getOrElse(i) { true }) null else defaultBox(g, i)
            autoBoxes[i] = box?.let { RectF(it.x, it.y, it.x + it.width, it.y + it.height) }
        }
        board.autoRegions = g?.let(::regionsOf)
        syncBoard()
        updateRegions()
        updateAuto()
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
        val uri = if (resultCode == RESULT_OK) data?.data else null
        when (requestCode) {
            REQUEST_PICK -> onPicked(uri)
            REQUEST_EXPORT -> onExportTarget(uri)
            REQUEST_IMPORT -> if (uri != null) readScheme(uri)
        }
    }

    private fun onPicked(uri: Uri?) {
        if (uri == null) {
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
                onFrame(file.path, bitmap, null)
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
        val i = selected
        if (edits[i] == EDIT_NONE && stored[i] == null) return
        edits[i] = if (stored[i] != null) EDIT_CLEAR else EDIT_NONE
        pending[i] = null
        imported[i] = null
        syncBoard()
        updateRegions()
    }

    private fun confirmResetAll() {
        AlertDialog.Builder(this)
            .setTitle("全部恢复自动")
            .setMessage("清除已保存的框和这里还没保存的修改，豆和头像都改回自动定位？")
            .setPositiveButton("恢复自动") { _, _ -> resetAll() }
            .setNegativeButton("取消", null)
            .show()
    }

    private fun resetAll() {
        // Only saved boxes are written: each write rebuilds the recognition pipeline.
        for (i in 0 until COUNT) if (stored[i] != null) settings.setCalibRect(Settings.KEY_CALIB_RECTS[i], null)
        readStored()
        edits.fill(EDIT_NONE)
        pending.fill(null)
        imported.fill(null)
        syncBoard()
        updateRegions()
        // Boxes calibrated when this frame was analysed have no automatic position on it.
        val unknown = auto?.calibrated?.any { it } == true
        toast(if (unknown) "已全部恢复自动；重新取一帧可以看到自动识别的位置" else "已全部恢复自动")
    }

    private fun save() {
        if (!frameReady) {
            toast("还没有画面")
            return
        }
        val sets = (0 until COUNT).any { edits[it] == EDIT_SET }
        if (!sets) {
            // Untouched boxes stay automatic: no change writes nothing at all.
            if (edits.none { it == EDIT_CLEAR }) {
                toast("没有修改，识别照旧")
                finish()
            } else {
                commit(null, emptyList())
            }
            return
        }
        val profile = choice
        if (profile != "camp" && profile != "duel") {
            askProfile()
            return
        }
        // One profile covers all boxes: untouched ones drawn on the other layout cannot stay.
        val stale = staleFor(profile)
        if (stale.isNotEmpty()) {
            AlertDialog.Builder(this)
                .setTitle("布局不同")
                .setMessage(
                    "${names(stale)}是在${profileName(settings.calibProfile)}画面上框的，" +
                        "与这一帧（${profileName(profile)}）不同。保存后会恢复自动，可在这一帧上重新框。",
                )
                .setPositiveButton("保存") { _, _ -> commit(profile, stale) }
                .setNegativeButton("取消", null)
                .show()
            return
        }
        commit(profile, emptyList())
    }

    private fun askProfile() {
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
    }

    /** Untouched saved boxes that a scheme of [profile] cannot keep (drawn on the other layout). */
    private fun staleFor(profile: String): List<Int> =
        if (settings.calibProfile != profile) untouchedStored() else emptyList()

    /** The boxes 保存 leaves (x,y,w,h; null = automatic); [clear] = stale saved boxes dropped. */
    private fun finalBoxes(clear: List<Int>): Array<FloatArray?> = Array(COUNT) { i ->
        when {
            // An imported box is stored exactly as the file has it.
            edits[i] == EDIT_SET -> imported[i]?.copyOf() ?: pending[i]?.let(::toStored)
            edits[i] == EDIT_CLEAR || i in clear -> null
            else -> stored[i]
        }
    }

    /** [profile] = layout of the changed boxes (null: no box set here). */
    private fun commit(profile: String?, clear: List<Int>) {
        val next = finalBoxes(clear)
        // Only real changes are written: each write rebuilds the native pipeline.
        val changed = (0 until COUNT).filter { !sameBox(next[it], stored[it]) }
        val newProfile = profile?.takeIf { it != settings.calibProfile && next.any { b -> b != null } }
        if (changed.isEmpty() && newProfile == null) {
            toast("没有修改，识别照旧")
            finish()
            return
        }
        // Profile first: listeners reacting to a box already see the matching layout.
        if (newProfile != null) settings.calibProfile = newProfile
        for (i in changed) settings.setCalibRect(Settings.KEY_CALIB_RECTS[i], next[i])
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

    // ---- Scheme files (docs/区域校准.md 方案文件) -----------------------------

    /** Exports what 保存 would leave (saved boxes plus the changes here); automatic boxes are not in it. */
    private fun exportScheme() {
        if (!frameReady) {
            toast("还没有画面")
            return
        }
        val sets = (0 until COUNT).any { edits[it] == EDIT_SET }
        val profile = if (sets) choice else settings.calibProfile
        if (profile != "camp" && profile != "duel") {
            askProfile()
            return
        }
        val stale = if (sets) staleFor(profile) else emptyList()
        val boxes = finalBoxes(stale)
        if (boxes.all { it == null }) {
            AlertDialog.Builder(this)
                .setTitle("没有要导出的框")
                .setMessage("现在全部是自动识别（默认方案），不用导出。改过或保存过的框才会写进方案文件。")
                .setPositiveButton("知道了", null)
                .show()
            return
        }
        val scheme = CalibrationScheme.Scheme(profile, frameWidth, frameHeight, boxes.toList())
        if (stale.isEmpty()) {
            startExport(scheme)
            return
        }
        AlertDialog.Builder(this)
            .setTitle("布局不同")
            .setMessage(
                "${names(stale)}是在${profileName(settings.calibProfile)}画面上框的，与这一帧" +
                    "（${profileName(profile)}）不同，不会导出（保存时也会恢复自动）。",
            )
            .setPositiveButton("导出") { _, _ -> startExport(scheme) }
            .setNegativeButton("取消", null)
            .show()
    }

    private fun startExport(scheme: CalibrationScheme.Scheme) {
        exportText = CalibrationScheme.encode(scheme)
        exportUnsaved = dirty()
        val intent = Intent(Intent.ACTION_CREATE_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .setType("application/json")
            .putExtra(Intent.EXTRA_TITLE, CalibrationScheme.fileName(frameWidth, frameHeight))
        try {
            @Suppress("DEPRECATION")
            startActivityForResult(intent, REQUEST_EXPORT)
        } catch (e: RuntimeException) {
            Log.e(TAG, "create document failed", e)
            exportText = null
            toast("没有可用的文件管理器")
        }
    }

    private fun onExportTarget(uri: Uri?) {
        val text = exportText
        val unsaved = exportUnsaved
        exportText = null
        if (uri == null || text == null) return
        val app = applicationContext
        Thread({
            val ok = writeDocument(app, uri, text)
            runOnUiThread {
                toast(
                    when {
                        !ok -> "导出失败，请换个位置再试"
                        unsaved -> "方案已导出（含还没保存的修改）"
                        else -> "方案已导出"
                    },
                )
            }
        }, "nt-calib-export").start()
    }

    private fun importScheme() {
        if (!frameReady) {
            toast("先载入画面，再导入方案")
            return
        }
        // Any file: chat apps and file managers label .json files inconsistently; the content is checked.
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .setType("*/*")
        try {
            @Suppress("DEPRECATION")
            startActivityForResult(intent, REQUEST_IMPORT)
        } catch (e: RuntimeException) {
            Log.e(TAG, "open document failed", e)
            toast("没有可用的文件选择器")
        }
    }

    private fun readScheme(uri: Uri) {
        val app = applicationContext
        Thread({
            val result = runCatching { CalibrationScheme.decode(readDocument(app, uri)) }
            runOnUiThread { if (!isDestroyed) onSchemeRead(result) }
        }, "nt-calib-scheme").start()
    }

    private fun onSchemeRead(result: Result<CalibrationScheme.Scheme>) {
        val scheme = result.getOrElse { e ->
            if (e !is CalibrationScheme.FormatException) Log.w(TAG, "read scheme failed", e)
            AlertDialog.Builder(this)
                .setTitle("无法导入方案")
                .setMessage(if (e is CalibrationScheme.FormatException) e.message else "读不了这个文件。")
                .setPositiveButton("知道了", null)
                .show()
            return
        }
        if (!frameReady) return
        val notes = ArrayList<String>()
        val unsaved = (0 until COUNT).filter { edits[it] != EDIT_NONE }
        if (unsaved.isNotEmpty()) {
            notes += "方案会替换这里还没保存的修改（${names(unsaved)}）。"
        }
        val diff = CalibrationScheme.aspectDifference(scheme, frameWidth, frameHeight)
        if (diff != null && diff > CalibrationScheme.ASPECT_TOLERANCE) {
            notes += "方案是在 ${scheme.frameWidth}×${scheme.frameHeight} 的画面上做的，和这一帧（${frameWidth}×${frameHeight}）" +
                "的比例相差 ${String.format(Locale.ROOT, "%.1f", diff * 100)}%，框可能对不上。"
        }
        if (detected.isNotEmpty() && detected != scheme.profile) {
            notes += "方案的框画在${profileName(scheme.profile)}画面上，这一帧是${profileName(detected)}：" +
                "框不会贴着这一帧的豆和头像，保存后识别会按布局换算。"
        }
        if (notes.isEmpty()) {
            applyScheme(scheme)
            return
        }
        AlertDialog.Builder(this)
            .setTitle("导入方案")
            .setMessage(notes.joinToString("\n\n") + "\n\n载入这个方案？")
            .setPositiveButton("载入") { _, _ -> applyScheme(scheme) }
            .setNegativeButton("取消", null)
            .show()
    }

    /**
     * Loads a scheme as pending edits for review. It replaces the whole scheme: its boxes are
     * changed (kept exactly as in the file), saved boxes it lacks go back to automatic, so 保存
     * stores exactly the file's scheme.
     */
    private fun applyScheme(scheme: CalibrationScheme.Scheme) {
        for (i in 0 until COUNT) {
            val b = scheme.boxes[i]
            if (b != null) {
                edits[i] = EDIT_SET
                pending[i] = toRect(b)
                imported[i] = b.copyOf()
            } else {
                edits[i] = if (stored[i] != null) EDIT_CLEAR else EDIT_NONE
                pending[i] = null
                imported[i] = null
            }
        }
        choice = scheme.profile
        choiceByUser = true
        choiceImported = true
        syncBoard()
        val loaded = (0 until COUNT).filter { scheme.boxes[it] != null }
        val cleared = (0 until COUNT).filter { edits[it] == EDIT_CLEAR }
        loaded.firstOrNull()?.let { selected = it }
        board.selected = selected
        updateRegions()
        // Short: a text toast shows two lines at most. The chips keep the per-box state.
        val parts = ArrayList<String>()
        parts += if (loaded.isEmpty()) "方案是全部自动" else "已载入${names(loaded)}"
        if (cleared.isNotEmpty()) parts += "${names(cleared)}将恢复自动"
        Toast.makeText(applicationContext, parts.joinToString("；") + "，点「保存」生效", Toast.LENGTH_LONG).show()
    }

    private fun toast(s: String) = Toast.makeText(applicationContext, s, Toast.LENGTH_SHORT).show()

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
        private const val REQUEST_EXPORT = 52
        private const val REQUEST_IMPORT = 53
        private const val EDIT_NONE = 0
        private const val EDIT_SET = 1
        private const val EDIT_CLEAR = 2
        private const val PANEL_WIDTH_DP = 224f
        private const val HINT_BG = 0xCC071219.toInt()
        private const val MATCH = ViewGroup.LayoutParams.MATCH_PARENT
        private const val WRAP = ViewGroup.LayoutParams.WRAP_CONTENT
        private const val STATE_SOURCE = "source"
        private const val STATE_FRAME = "frame"
        private const val STATE_DETECTED = "detected"
        private const val STATE_CHOICE = "choice"
        private const val STATE_CHOICE_BY_USER = "choiceByUser"
        private const val STATE_CHOICE_IMPORTED = "choiceImported"
        private const val STATE_SELECTED = "selected"
        private const val STATE_EDITS = "edits"
        private const val STATE_RECT = "rect"
        private const val STATE_EXACT = "exact"
        private const val STATE_SHOW_AUTO = "showAuto"
        private const val STATE_EXPORT = "exportText"
        private const val STATE_EXPORT_UNSAVED = "exportUnsaved"

        private const val BEAD_ROW_HINT =
            "豆框按普通忍者的豆行保存；认出九喇嘛水门、侠隐佐助、百战鼬时会自动换算，认不出时请换普通忍者的画面。"

        private val REGION_HINTS = arrayOf(
            "框住左边 4 颗豆（柱间的第 5、6 颗不框）。圆点是推算的豆心，1 号在最左。$BEAD_ROW_HINT",
            "框住右边 4 颗豆（柱间的第 5、6 颗不框）。圆点是推算的豆心，1 号在最右。$BEAD_ROW_HINT",
            "紧贴左边的头像，不含名字和血条。",
            "紧贴右边的头像，不含名字和血条。",
        )

        /**
         * Opens the editor on a live snapshot (takes ownership of its bitmap). Called from the
         * overlay, so it starts its own task; when it closes, the game is in front again.
         */
        fun openLive(context: Context, snapshot: FrameLoop.Snapshot, draft: Bundle?): Boolean {
            val settings = (context.applicationContext as TimerApp).settings
            val auto = snapshot.geometry?.takeIf(::hasRegions)?.let { g ->
                // Boxes calibrated now were calibrated when this frame was analysed (the engine
                // applies a saved calibration within ~0.1 s).
                CalibrationFrames.Auto(g, BooleanArray(COUNT) { settings.calibRect(Settings.KEY_CALIB_RECTS[it]) != null }, snapshot.ninjas)
            }
            val file = CalibrationFrames.holdLive(context, snapshot.bitmap, auto)
            val intent = Intent(context, CalibrationActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
                .putExtra(EXTRA_SOURCE, SOURCE_LIVE)
                .putExtra(EXTRA_FRAME, file.path)
                .putExtra(EXTRA_PROFILE, snapshot.layoutProfile)
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

        private fun names(boxes: List<Int>): String = boxes.joinToString("、") { LABELS[it] }

        /** A geometry with anything to show (lobby/result frames report empty sides). */
        private fun hasRegions(g: FrameGeometry): Boolean = g.width > 0 && g.height > 0 &&
            listOf(g.left, g.right).any {
                it.beads.isNotEmpty() || it.nameRoi != null || it.avatarRoi != null || it.beadBox != null || it.avatarBox != null
            }

        /** The automatic box of [index] (Settings.KEY_CALIB_RECTS order) on this frame. */
        private fun defaultBox(g: FrameGeometry, index: Int): FrameBox? = when (index) {
            CalibrationView.LEFT_BEADS -> g.left.beadBox
            CalibrationView.RIGHT_BEADS -> g.right.beadBox
            CalibrationView.LEFT_AVATAR -> g.left.avatarBox
            else -> g.right.avatarBox
        }

        /** The engine's regions as frame fractions; a bead is drawn on its sampled pixel's middle. */
        private fun regionsOf(g: FrameGeometry): CalibrationView.AutoRegions {
            val w = g.width.toFloat()
            val h = g.height.toFloat()
            fun rect(r: PixelRect?) = r?.let { RectF(it.x / w, it.y / h, (it.x + it.width) / w, (it.y + it.height) / h) }
            val sides = arrayOf(g.left, g.right)
            return CalibrationView.AutoRegions(
                beads = Array(2) { s ->
                    val beads = sides[s].beads
                    FloatArray(beads.size * 2) { k -> if (k % 2 == 0) (beads[k / 2].x + 0.5f) / w else (beads[k / 2].y + 0.5f) / h }
                },
                names = Array(2) { rect(sides[it].nameRoi) },
                avatars = Array(2) { rect(sides[it].avatarRoi) },
            )
        }

        private fun sameBox(a: FloatArray?, b: FloatArray?): Boolean =
            if (a == null || b == null) a == b else a.contentEquals(b)

        /** The same box as far as saving goes: identical, or identical once stored (5 decimals ≈ 0.02 px). */
        private fun sameRect(a: RectF, b: RectF): Boolean {
            if (a == b) return true
            val x = toStored(a) ?: return false
            val y = toStored(b) ?: return false
            return x.contentEquals(y)
        }

        /** The document as UTF-8 text; anything over [CalibrationScheme.MAX_BYTES] is not a scheme. */
        private fun readDocument(context: Context, uri: Uri): String {
            val input = context.contentResolver.openInputStream(uri) ?: throw IOException("no stream for $uri")
            val bytes = input.use { s ->
                val out = ByteArrayOutputStream()
                val chunk = ByteArray(8192)
                while (true) {
                    val n = s.read(chunk)
                    if (n < 0) break
                    out.write(chunk, 0, n)
                    if (out.size() > CalibrationScheme.MAX_BYTES) {
                        throw CalibrationScheme.FormatException("文件太大，不是校准方案文件。")
                    }
                }
                out.toByteArray()
            }
            return String(bytes, Charsets.UTF_8)
        }

        private fun writeDocument(context: Context, uri: Uri, text: String): Boolean = try {
            val resolver = context.contentResolver
            // "wt" truncates an existing document; some providers only know "w".
            val out = runCatching { resolver.openOutputStream(uri, "wt") }.getOrNull() ?: resolver.openOutputStream(uri, "w")
            out?.use { it.write(text.toByteArray(Charsets.UTF_8)) } != null
        } catch (e: Exception) {
            Log.w(TAG, "export $uri failed", e)
            false
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
