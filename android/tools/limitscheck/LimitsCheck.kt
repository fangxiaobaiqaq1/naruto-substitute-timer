package limitscheck

import com.narutotimer.CalibrationLimits
import kotlin.system.exitProcess

// CalibrationLimits (docs/区域校准.md 框的大小限制) decides exactly like Go
// (internal/config calibration_limits_test.go calibrationDecisionDigest) and the C++ core
// (android/app/src/main/cpp/tests/native_fixes_test.cpp): the same 2×200000 generated boxes
// (splitmix64, + - * / only), the same FNV-1a digest of the decisions.

private class SplitMix(private var s: ULong) {
    fun next(): ULong {
        s += 0x9E3779B97F4A7C15uL
        var z = s
        z = (z xor (z shr 30)) * 0xBF58476D1CE4E5B9uL
        z = (z xor (z shr 27)) * 0x94D049BB133111EBuL
        return z xor (z shr 31)
    }

    fun unit(): Double = (next() shr 11).toDouble() * (1.0 / (1L shl 53).toDouble())
}

/** 0 usable, 1 outside the frame, 2 too large, 3 too small, 4 bad shape. */
private fun decision(avatar: Boolean, x: Double, y: Double, w: Double, h: Double): Int {
    if (!CalibrationLimits.inFrame(x, y, w, h)) return 1
    val issue = CalibrationLimits.issue(avatar, w, h) ?: return 0
    return when {
        "太大" in issue -> 2
        "太小" in issue -> 3
        else -> 4
    }
}

private fun digest(n: Int, float32: Boolean, counts: IntArray): ULong {
    val g = SplitMix(20261021uL)
    var d = 14695981039346656037uL
    for (i in 0 until n) {
        val avatar = i % 2 == 1
        val wn = if (avatar) 68.5 / 960 else 0.0625
        val hn = if (avatar) 68.5 / 540 else 1.0 / 36
        val k = 0.2 + 2.2 * g.unit()
        var w = wn * k * (0.5 + g.unit())
        var h = hn * k * (0.5 + g.unit())
        when ((g.next() % 8uL).toInt()) {
            0 -> w = wn * 2
            1 -> h = hn * 0.5
            2 -> { w = wn * 2; h = hn * 0.5 }
        }
        var x = (1 - w) * g.unit()
        var y = (1 - h) * g.unit()
        when ((g.next() % 16uL).toInt()) {
            0 -> x = 1 - w
            1 -> y = 1 - h + 1e-9
        }
        val c = if (float32) {
            decision(avatar, x.toFloat().toDouble(), y.toFloat().toDouble(), w.toFloat().toDouble(), h.toFloat().toDouble())
        } else {
            decision(avatar, x, y, w, h)
        }
        counts[c]++
        d = (d xor c.toULong()) * 1099511628211uL
    }
    return d
}

fun main() {
    var ok = true
    for ((float32, want) in listOf(false to 0xe9e1f0fa7bdcdb2duL, true to 0x5c07b936a6c3ad61uL)) {
        val counts = IntArray(5)
        val got = digest(200000, float32, counts)
        println("float32=$float32 digest=0x${got.toString(16)} counts=${counts.joinToString()}")
        if (got != want) {
            println("FAIL: decisions differ from Go/C++ (want 0x${want.toString(16)})")
            ok = false
        }
    }
    // The FloatArray entry points used by Settings / CalibrationScheme agree with the double ones.
    val box = floatArrayOf(0.081f, 0.104f, 0.052f, 0.022f)
    if (!CalibrationLimits.usable(0, box) || CalibrationLimits.usable(2, floatArrayOf(0f, 0f, 1f, 1f))) {
        println("FAIL: FloatArray checks")
        ok = false
    }
    if (!ok) exitProcess(1)
    println("PASS: CalibrationLimits matches Go and C++")
}
