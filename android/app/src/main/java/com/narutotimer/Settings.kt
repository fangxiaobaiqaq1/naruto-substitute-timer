package com.narutotimer

import android.content.Context
import android.content.SharedPreferences
import java.util.concurrent.CopyOnWriteArrayList

/**
 * 持久化设置（architect 所有）。对应 Go config.UIConfig 中 Android 端仍有意义的字段，
 * 以及 config.Default() 里状态机要用的固定参数（Tracking / Scene / Layout.BeadsPerSide）。
 *
 * 线程：getter/setter 线程安全（SharedPreferences + @Synchronized）。setter 立即 apply() 落盘，
 * 然后在调用线程同步通知 [addListener] 注册的监听者（参数为键名，见 KEY_*）。
 * 侧别（playerSide/rememberSide）的语义规则在 tracker/Session 里（Go settings.go），这里只是存储。
 */
class Settings(context: Context) {
    private val prefs: SharedPreferences = context.applicationContext.getSharedPreferences("settings", Context.MODE_PRIVATE)
    private val listeners = CopyOnWriteArrayList<(String) -> Unit>()

    // ---- 固定参数（Go config.Default()，Android 不提供修改入口） ----
    /** ui.pollIntervalMs：对局中采集/识别间隔。 */
    val pollIntervalMs: Int = 16
    /** ui.idlePollIntervalMs：非对局时的间隔。 */
    val idlePollIntervalMs: Int = 400
    /** tracking.minimumConfirmFrames */
    val minimumConfirmFrames: Int = 2
    /** tracking.enterFightFrames */
    val enterFightFrames: Int = 1
    /** tracking.leaveFightFrames */
    val leaveFightFrames: Int = 10
    /** layout.beadsPerSide */
    val beadsPerSide: Int = 4
    /** scene.endScenes */
    val endScenes: List<String> = listOf("lobby", "result")
    /** scene.holdScenes */
    val holdScenes: List<String> = listOf("vs", "queue", "pick", "ban")
    /** ui.substituteCooldownSeconds（历史字段；当前策略恒为 15 秒，见 NinjaRules）。 */
    val substituteCooldownSeconds: Double = 15.0

    // ---- 用户设置 ----
    /** ui.playerNames：我方账号名（已 trim，去空）。 */
    var playerNames: List<String>
        @Synchronized get() = prefs.getString(KEY_PLAYER_NAMES, "")!!.split('\n').map { it.trim() }.filter { it.isNotEmpty() }
        set(value) = put(KEY_PLAYER_NAMES) { putString(KEY_PLAYER_NAMES, value.map { it.trim() }.filter { it.isNotEmpty() }.joinToString("\n")) }

    /** ui.playerSide："auto" / "left" / "right"。 */
    var playerSide: String
        @Synchronized get() = when (val v = prefs.getString(KEY_PLAYER_SIDE, "auto")) { "left", "right" -> v; else -> "auto" }
        set(value) = put(KEY_PLAYER_SIDE) { putString(KEY_PLAYER_SIDE, if (value == "left" || value == "right") value else "auto") }

    /** ui.rememberSide */
    var rememberSide: Boolean
        @Synchronized get() = prefs.getBoolean(KEY_REMEMBER_SIDE, true)
        set(value) = put(KEY_REMEMBER_SIDE) { putBoolean(KEY_REMEMBER_SIDE, value) }

    /** ui.ninjaQuery：完整的对面忍者变体名覆盖；空 = 视觉识别。 */
    var ninjaQuery: String
        @Synchronized get() = prefs.getString(KEY_NINJA_QUERY, "")!!
        set(value) = put(KEY_NINJA_QUERY) { putString(KEY_NINJA_QUERY, value.trim()) }

    /** ui.showBothSides */
    var showBothSides: Boolean
        @Synchronized get() = prefs.getBoolean(KEY_SHOW_BOTH, false)
        set(value) = put(KEY_SHOW_BOTH) { putBoolean(KEY_SHOW_BOTH, value) }

    /** ui.overlayMode："full" / "mini"。 */
    var overlayMode: String
        @Synchronized get() = if (prefs.getString(KEY_OVERLAY_MODE, "full") == "mini") "mini" else "full"
        set(value) = put(KEY_OVERLAY_MODE) { putString(KEY_OVERLAY_MODE, if (value == "mini") "mini" else "full") }

    /** Whether the user explicitly started the game overlay (default: off). */
    var overlayEnabled: Boolean
        @Synchronized get() = prefs.getBoolean(KEY_OVERLAY_ENABLED, false)
        set(value) = put(KEY_OVERLAY_ENABLED) { putBoolean(KEY_OVERLAY_ENABLED, value) }

    /** ui.windowOpacity ∈ [0.40, 1.00]（完整悬浮窗）。 */
    var windowOpacity: Float
        @Synchronized get() = prefs.getFloat(KEY_WINDOW_OPACITY, 1f).coerceIn(0.40f, 1f)
        set(value) = put(KEY_WINDOW_OPACITY) { putFloat(KEY_WINDOW_OPACITY, value.coerceIn(0.40f, 1f)) }

    /** ui.miniOpacity ∈ [0.20, 1.00]（迷你悬浮窗）。 */
    var miniOpacity: Float
        @Synchronized get() = prefs.getFloat(KEY_MINI_OPACITY, 0.85f).coerceIn(0.20f, 1f)
        set(value) = put(KEY_MINI_OPACITY) { putFloat(KEY_MINI_OPACITY, value.coerceIn(0.20f, 1f)) }

    /** Android overlay scale ∈ [0.25, 3.00], with 1% slider precision. */
    var fontScale: Float
        @Synchronized get() = prefs.getFloat(KEY_FONT_SCALE, DEFAULT_FONT_SCALE).coerceIn(FONT_SCALE_MIN, FONT_SCALE_MAX)
        set(value) = put(KEY_FONT_SCALE) { putFloat(KEY_FONT_SCALE, value.coerceIn(FONT_SCALE_MIN, FONT_SCALE_MAX)) }

    /** Card width / height as a multiple of its content ∈ [1, 3]; text keeps [fontScale]. */
    var cardWidth: Float
        @Synchronized get() = prefs.getFloat(KEY_CARD_WIDTH, 1f).coerceIn(1f, CARD_STRETCH_MAX)
        set(value) = put(KEY_CARD_WIDTH) { putFloat(KEY_CARD_WIDTH, value.coerceIn(1f, CARD_STRETCH_MAX)) }
    var cardHeight: Float
        @Synchronized get() = prefs.getFloat(KEY_CARD_HEIGHT, 1f).coerceIn(1f, CARD_STRETCH_MAX)
        set(value) = put(KEY_CARD_HEIGHT) { putFloat(KEY_CARD_HEIGHT, value.coerceIn(1f, CARD_STRETCH_MAX)) }

    /** 悬浮窗位置（px，相对屏幕左上；Int.MIN_VALUE = 未设置，用默认位置）。 */
    var overlayX: Int
        @Synchronized get() = prefs.getInt(KEY_OVERLAY_X, Int.MIN_VALUE)
        set(value) = put(KEY_OVERLAY_X) { putInt(KEY_OVERLAY_X, value) }
    var overlayY: Int
        @Synchronized get() = prefs.getInt(KEY_OVERLAY_Y, Int.MIN_VALUE)
        set(value) = put(KEY_OVERLAY_Y) { putInt(KEY_OVERLAY_Y, value) }

    /** 区域校准（docs/区域校准.md）：画框时画面的布局，"camp" / "duel"。 */
    var calibProfile: String
        @Synchronized get() = if (prefs.getString(KEY_CALIB_PROFILE, "duel") == "camp") "camp" else "duel"
        set(value) = put(KEY_CALIB_PROFILE) { putString(KEY_CALIB_PROFILE, if (value == "camp") "camp" else "duel") }

    /**
     * One calibration box ([KEY_CALIB_RECTS]) as x,y,w,h fractions of the frame; null = automatic.
     * Only boxes the native core uses: inside the frame and within [CalibrationLimits] (a stored
     * box outside them stays automatic, see [calibRectIssue]).
     */
    @Synchronized
    fun calibRect(key: String): FloatArray? {
        val b = storedCalibRect(key) ?: return null
        return b.takeIf { CalibrationLimits.issue(isAvatarKey(key), it) == null }
    }

    /** Why the stored box of [key] is not used (size limits), or null: none stored, or used. */
    @Synchronized
    fun calibRectIssue(key: String): String? {
        val b = storedCalibRect(key) ?: return null
        return CalibrationLimits.issue(isAvatarKey(key), b)
    }

    /** The stored box when it lies inside the frame (whatever its size). */
    private fun storedCalibRect(key: String): FloatArray? {
        val v = prefs.getString(key, "")!!.split(',').mapNotNull { it.trim().toFloatOrNull() }
        if (v.size != 4) return null
        return v.toFloatArray().takeIf(CalibrationLimits::inFrame)
    }

    private fun isAvatarKey(key: String) = key == KEY_CALIB_LEFT_AVATAR || key == KEY_CALIB_RIGHT_AVATAR

    fun setCalibRect(key: String, rect: FloatArray?) =
        put(key) { putString(key, rect?.joinToString(",") ?: "") }

    /** 是否优先使用 MediaProjection（false = 只用无障碍截屏）。 */
    var preferProjection: Boolean
        @Synchronized get() = prefs.getBoolean(KEY_PREFER_PROJECTION, true)
        set(value) = put(KEY_PREFER_PROJECTION) { putBoolean(KEY_PREFER_PROJECTION, value) }

    /** 在线更新源：gitee / github。 */
    var updateSource: String
        @Synchronized get() = if (prefs.getString(KEY_UPDATE_SOURCE, "gitee") == "github") "github" else "gitee"
        set(value) = put(KEY_UPDATE_SOURCE) { putString(KEY_UPDATE_SOURCE, if (value == "github") "github" else "gitee") }

    var autoCheckUpdates: Boolean
        @Synchronized get() = prefs.getBoolean(KEY_AUTO_UPDATES, true)
        set(value) = put(KEY_AUTO_UPDATES) { putBoolean(KEY_AUTO_UPDATES, value) }

    /** debug.enabled：输出 [timer] 调试日志。 */
    var debug: Boolean
        @Synchronized get() = prefs.getBoolean(KEY_DEBUG, false)
        set(value) = put(KEY_DEBUG) { putBoolean(KEY_DEBUG, value) }

    fun addListener(l: (String) -> Unit) { listeners += l }
    fun removeListener(l: (String) -> Unit) { listeners -= l }

    private inline fun put(key: String, edit: SharedPreferences.Editor.() -> Unit) {
        synchronized(this) { prefs.edit().apply(edit).apply() }
        for (l in listeners) l(key)
    }

    companion object {
        const val KEY_PLAYER_NAMES = "playerNames"
        const val KEY_PLAYER_SIDE = "playerSide"
        const val KEY_REMEMBER_SIDE = "rememberSide"
        const val KEY_NINJA_QUERY = "ninjaQuery"
        const val KEY_SHOW_BOTH = "showBothSides"
        const val KEY_OVERLAY_MODE = "overlayMode"
        const val KEY_OVERLAY_ENABLED = "overlayEnabled"
        const val KEY_WINDOW_OPACITY = "windowOpacity"
        const val KEY_MINI_OPACITY = "miniOpacity"
        const val KEY_FONT_SCALE = "fontScale"
        const val KEY_CARD_WIDTH = "cardWidth"
        const val KEY_CARD_HEIGHT = "cardHeight"
        const val KEY_OVERLAY_X = "overlayX"
        const val KEY_OVERLAY_Y = "overlayY"
        const val KEY_PREFER_PROJECTION = "preferProjection"
        const val KEY_UPDATE_SOURCE = "updateSource"
        const val KEY_AUTO_UPDATES = "autoCheckUpdates"
        const val KEY_DEBUG = "debug"
        const val KEY_CALIB_PROFILE = "calibProfile"
        const val KEY_CALIB_LEFT_BEADS = "calibLeftBeads"
        const val KEY_CALIB_RIGHT_BEADS = "calibRightBeads"
        const val KEY_CALIB_LEFT_AVATAR = "calibLeftAvatar"
        const val KEY_CALIB_RIGHT_AVATAR = "calibRightAvatar"
        /** Native order: left beads, right beads, left avatar, right avatar. */
        val KEY_CALIB_RECTS = listOf(KEY_CALIB_LEFT_BEADS, KEY_CALIB_RIGHT_BEADS, KEY_CALIB_LEFT_AVATAR, KEY_CALIB_RIGHT_AVATAR)
        /** The overlay card is designed at 100 %; the old one-line pill used 75 %. */
        const val DEFAULT_FONT_SCALE = 1.00f
        const val FONT_SCALE_MIN = 0.25f
        const val FONT_SCALE_MAX = 3.00f
        const val FONT_SCALE_STEP = 0.01f
        const val CARD_STRETCH_MAX = 3.00f
        const val CARD_STRETCH_STEP = 0.05f
    }
}
