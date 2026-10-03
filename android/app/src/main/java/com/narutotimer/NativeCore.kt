package com.narutotimer

import android.hardware.HardwareBuffer
import java.nio.ByteBuffer
import java.nio.ByteOrder

/**
 * JNI 门面（architect 所有）。所有 external 都是 @JvmStatic，由 jni.cpp 的 RegisterNatives 绑定。
 *
 * 线程约定：
 *  - [nativeAddImage]/[nativeAddText] 可在任意线程调用（AssetStore 自带锁）。
 *  - [analyzeHardwareBuffer]/[analyzeBuffer] 只应在 FrameLoop 的单一分析线程调用；
 *    解码缓冲是线程私有的（ThreadLocal），返回的 [AnalysisResult] 不可变、可跨线程发布。
 *
 * 结果编码见 ARCHITECTURE.md §JNI 结果编码（v1，小端，'NTR1'）。
 */
object NativeCore {
    /** 采集方式（进入 native 帧指纹；与 nt::frame::MethodName 一致）。 */
    const val METHOD_PROJECTION = 0
    const val METHOD_SCREENSHOT = 1
    const val METHOD_BUFFER = 2

    private const val MAGIC = 0x4E545231
    private const val HEADER_BYTES = 72
    private const val BEAD_BYTES = 24
    private const val STRING_SLOTS = 13
    private const val INITIAL_OUT_BYTES = 16 * 1024

    private const val FLAG_FIGHTING = 1 shl 0
    private const val FLAG_UNCERTAIN = 1 shl 1
    private const val FLAG_ROUND_OPENING = 1 shl 2
    private const val FLAG_HOLD = 1 shl 3
    private const val FLAG_HAS_ERR = 1 shl 4
    private const val FLAG_DUPLICATE = 1 shl 5
    private const val FLAG_REUSED = 1 shl 6

    private const val BEAD_LIT = 1 shl 0
    private const val BEAD_GOLD = 1 shl 1
    private const val BEAD_UNKNOWN = 1 shl 2

    init {
        System.loadLibrary("narutocore")
    }

    // ---- 原始 JNI（见 jni.cpp kMethods） ----
    @JvmStatic external fun nativeInit(): Boolean
    @JvmStatic external fun nativeAddImage(path: String, width: Int, height: Int, rgba: ByteBuffer, rowStride: Int): Boolean
    @JvmStatic external fun nativeAddText(path: String, text: String): Boolean
    @JvmStatic external fun nativeFinishAssets(): Boolean
    @JvmStatic external fun nativeLastError(): String?
    @JvmStatic external fun nativeSetPlayerNames(names: Array<String>)
    @JvmStatic external fun nativeAnalyzeHardwareBuffer(hb: HardwareBuffer, capturedAtNanos: Long, method: Int, out: ByteBuffer): Int
    @JvmStatic external fun nativeAnalyzeBuffer(
        pixels: ByteBuffer, width: Int, height: Int, rowStride: Int, capturedAtNanos: Long, method: Int, out: ByteBuffer,
    ): Int
    @JvmStatic external fun nativeTakeOppCrop(): IntArray?
    @JvmStatic external fun nativeResetDelivery()
    @JvmStatic external fun nativeRelease()
    @JvmStatic external fun nativeResultHeaderBytes(): Int

    // ---- 高层 API ----

    /** 最近一次 native 错误（无则空串）。 */
    fun lastError(): String = nativeLastError() ?: ""

    fun setPlayerNames(names: List<String>) = nativeSetPlayerNames(names.toTypedArray())

    /**
     * 分析一帧 HardwareBuffer（MediaProjection ImageReader / takeScreenshot）。
     * 调用方在返回后才能关闭 Image / HardwareBuffer。失败返回 null（原因见 [lastError]）。
     */
    fun analyzeHardwareBuffer(hb: HardwareBuffer, capturedAtNanos: Long, method: Int): AnalysisResult? {
        val d = decoder.get()!!
        var n = nativeAnalyzeHardwareBuffer(hb, capturedAtNanos, method, d.out)
        if (n < 0) {
            d.grow(-n)
            n = nativeAnalyzeHardwareBuffer(hb, capturedAtNanos, method, d.out)
        }
        return if (n > 0) d.decode(n) else null
    }

    /** 分析一块 direct ByteBuffer（RGBA8，rowStride 字节）。 */
    fun analyzeBuffer(pixels: ByteBuffer, width: Int, height: Int, rowStride: Int, capturedAtNanos: Long, method: Int): AnalysisResult? {
        val d = decoder.get()!!
        var n = nativeAnalyzeBuffer(pixels, width, height, rowStride, capturedAtNanos, method, d.out)
        if (n < 0) {
            d.grow(-n)
            n = nativeAnalyzeBuffer(pixels, width, height, rowStride, capturedAtNanos, method, d.out)
        }
        return if (n > 0) d.decode(n) else null
    }

    private val decoder = object : ThreadLocal<Decoder>() {
        override fun initialValue() = Decoder()
    }

    private val LABELS: Array<Array<String>> = Array(2) { side ->
        Array(16) { n -> (if (side == 0) "L" else "R") + n }
    }

    private fun labelOf(code: Int): String {
        if (code < 0) return ""
        val side = code ushr 8
        val n = code and 0xff
        if (side > 1) return ""
        return if (n < 16) LABELS[side][n] else (if (side == 0) "L" else "R") + n
    }

    /** 线程私有解码器：复用输出缓冲与字符串（字节相同则复用上一帧的 String，避免每帧分配）。 */
    private class Decoder {
        var out: ByteBuffer = ByteBuffer.allocateDirect(INITIAL_OUT_BYTES).order(ByteOrder.LITTLE_ENDIAN)
        private val lastBytes = arrayOfNulls<ByteArray>(STRING_SLOTS)
        private val lastStrings = Array(STRING_SLOTS) { "" }
        private var scratch = ByteArray(256)

        fun grow(need: Int) {
            var cap = out.capacity()
            while (cap < need) cap *= 2
            out = ByteBuffer.allocateDirect(cap).order(ByteOrder.LITTLE_ENDIAN)
        }

        fun decode(total: Int): AnalysisResult? {
            val b = out
            if (total < HEADER_BYTES || b.getInt(0) != MAGIC || b.getInt(4) != total) return null
            val flags = b.getInt(8)
            val leftSlots = b.getInt(12)
            val rightSlots = b.getInt(16)
            val width = b.getInt(20)
            val height = b.getInt(24)
            val beadCount = b.getInt(28)
            val sequence = b.getLong(32)
            val capturedAt = b.getLong(40)
            val analysisStarted = b.getLong(48)
            val analyzedAt = b.getLong(56)
            val gateScore = b.getDouble(64)
            var pos = HEADER_BYTES
            val beads = ArrayList<BeadInfo>(beadCount)
            for (i in 0 until beadCount) {
                val bf = b.getInt(pos + 12)
                beads.add(
                    BeadInfo(
                        x = b.getInt(pos),
                        y = b.getInt(pos + 4),
                        label = labelOf(b.getInt(pos + 8)),
                        lit = bf and BEAD_LIT != 0,
                        gold = bf and BEAD_GOLD != 0,
                        unknown = bf and BEAD_UNKNOWN != 0,
                        conf = b.getDouble(pos + 16),
                    ),
                )
                pos += BEAD_BYTES
            }
            val s = arrayOfNulls<String>(STRING_SLOTS)
            for (slot in 0 until STRING_SLOTS) {
                val len = b.getInt(pos)
                pos += 4
                s[slot] = readString(slot, pos, len)
                pos += len
            }
            val err = s[11]!!
            return AnalysisResult(
                fighting = flags and FLAG_FIGHTING != 0,
                uncertain = flags and FLAG_UNCERTAIN != 0,
                beads = beads,
                name = s[0]!!,
                layoutProfile = s[1]!!,
                scene = s[2]!!,
                gateScore = gateScore,
                roundOpening = flags and FLAG_ROUND_OPENING != 0,
                leftNinja = s[3]!!,
                rightNinja = s[4]!!,
                leftNinjaCandidate = s[5]!!,
                rightNinjaCandidate = s[6]!!,
                leftSlots = leftSlots,
                rightSlots = rightSlots,
                playerSide = s[7]!!,
                playerName = s[8]!!,
                oppName = s[9]!!,
                hold = flags and FLAG_HOLD != 0,
                err = if (flags and FLAG_HAS_ERR != 0) err else null,
                status = s[10]!!,
                width = width,
                height = height,
                captureStartedNanos = capturedAt,
                capturedAtNanos = capturedAt,
                analysisStartedNanos = analysisStarted,
                analyzedAtNanos = analyzedAt,
                captureMethod = s[12]!!,
                sequence = sequence,
                duplicate = flags and FLAG_DUPLICATE != 0,
                reused = flags and FLAG_REUSED != 0,
            )
        }

        private fun readString(slot: Int, pos: Int, len: Int): String {
            if (len == 0) return ""
            if (scratch.size < len) scratch = ByteArray(maxOf(len, scratch.size * 2))
            val b = out
            for (i in 0 until len) scratch[i] = b.get(pos + i)
            val prev = lastBytes[slot]
            if (prev != null && prev.size == len) {
                var same = true
                for (i in 0 until len) if (prev[i] != scratch[i]) { same = false; break }
                if (same) return lastStrings[slot]
            }
            val bytes = scratch.copyOf(len)
            val str = String(bytes, Charsets.UTF_8)
            lastBytes[slot] = bytes
            lastStrings[slot] = str
            return str
        }
    }
}

/** engine.BeadInfo（native 原样）。 */
data class BeadInfo(
    val x: Int,
    val y: Int,
    /** "L1".."R6" */
    val label: String,
    /** true = 亮蓝（可用） */
    val lit: Boolean,
    /** true = 金色可用豆；lit 同时为 true */
    val gold: Boolean,
    /** true = 这颗没看清，不能当暗豆 */
    val unknown: Boolean,
    /** 视觉证据得分 0~1，不是已校准的正确概率。 */
    val conf: Double,
)

/** frame.Bead：UI/状态机只关心的豆子显示信息（Go fillFromEngine 换算）。 */
data class FrameBead(
    val x: Int,
    val y: Int,
    val label: String,
    /** true = 亮蓝可用 */
    val lit: Boolean,
    /** true = 暗色（冷却中） */
    val dark: Boolean,
    /** 金色外观；是否可用由 lit/dark 表达。 */
    val gold: Boolean,
    /** true = 没看清，不能当暗豆、不能拿来掉 1 */
    val unknown: Boolean,
    val conf: Double,
)

/**
 * 一帧分析结果 = Go engine.Result 全部字段 + frame.Frame 的元数据（Hold/Err/Duplicate/Sequence/时间戳）。
 * 时间均为 System.nanoTime() 时基（CLOCK_MONOTONIC），0 = 零时刻（Go time.Time{}）。
 * 状态机（tracker/Session）把它当 Go 的 frame.Frame 使用。
 */
data class AnalysisResult(
    // ---- engine.Result ----
    val fighting: Boolean,
    val uncertain: Boolean,
    val beads: List<BeadInfo>,
    val name: String,
    val layoutProfile: String,
    val scene: String,
    val gateScore: Double,
    val roundOpening: Boolean,
    val leftNinja: String,
    val rightNinja: String,
    val leftNinjaCandidate: String,
    val rightNinjaCandidate: String,
    val leftSlots: Int,
    val rightSlots: Int,
    val playerSide: String,
    val playerName: String,
    val oppName: String,
    // ---- frame.Frame ----
    /** Go f.Hold（= res.Uncertain，或采集失败/画面过小）。 */
    val hold: Boolean,
    /** Go f.Err；null = 无错误。 */
    val err: String?,
    val status: String,
    val width: Int,
    val height: Int,
    val captureStartedNanos: Long,
    /** Successful image acquisition only; 0 on failed capture. */
    val capturedAtNanos: Long,
    /** 0 if analysis was skipped (deduped). */
    val analysisStartedNanos: Long,
    val analyzedAtNanos: Long,
    val captureMethod: String,
    val sequence: Long,
    /** Identical pixels; never count as an independent confirmation. */
    val duplicate: Boolean,
    /** 与上一帧像素相同，native 复用了上次识别结果（诊断）。 */
    val reused: Boolean,
) {
    /** Go engine.Result.Name 之外的 Frame.Engine 字段。 */
    val engine: String get() = name

    /** Go fillFromEngine：BeadInfo → frame.Bead。 */
    val frameBeads: List<FrameBead> by lazy(LazyThreadSafetyMode.PUBLICATION) {
        beads.map { b ->
            val unknown = b.unknown
            FrameBead(
                x = b.x,
                y = b.y,
                label = b.label,
                lit = (b.lit || b.gold) && !unknown,
                dark = !b.lit && !b.gold && !unknown,
                gold = b.gold,
                unknown = unknown,
                conf = b.conf,
            )
        }
    }

    /**
     * Go Frame.Slots：per-side topology from the detector. Legacy frames without
     * metadata retain explicitly configured counts.
     */
    fun slots(left: Boolean, fallback: Int): Int {
        val n = if (left) leftSlots else rightSlots
        return if (n == 4 || n == 6) n else fallback
    }

    companion object {
        /** 采集失败帧（Go: Frame{Hold: true, Err: ..., CaptureStarted: requestedAt}）。 */
        fun failure(message: String, startedNanos: Long, method: String = ""): AnalysisResult = AnalysisResult(
            fighting = false, uncertain = false, beads = emptyList(), name = "", layoutProfile = "", scene = "",
            gateScore = 0.0, roundOpening = false, leftNinja = "", rightNinja = "", leftNinjaCandidate = "",
            rightNinjaCandidate = "", leftSlots = 0, rightSlots = 0, playerSide = "", playerName = "", oppName = "",
            hold = true, err = message, status = "", width = 0, height = 0, captureStartedNanos = startedNanos,
            capturedAtNanos = 0, analysisStartedNanos = 0, analyzedAtNanos = 0, captureMethod = method,
            sequence = 0, duplicate = false, reused = false,
        )
    }
}
