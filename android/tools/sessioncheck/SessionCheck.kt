// Replays scenarios.seq through the production Android Session/SideClock/NinjaRules and prints one
// canonical line per `report`. gosession.go prints the same lines from Go captureOnce; run.sh diffs them.
// Internal Session fields are read by reflection only to compare state that has no public view.
package sessioncheck

import android.content.Context
import com.narutotimer.AnalysisResult
import com.narutotimer.BeadInfo
import com.narutotimer.NativeCore
import com.narutotimer.Settings
import com.narutotimer.tracker.Session
import com.narutotimer.tracker.SideClock
import com.narutotimer.tracker.TimerColors
import java.io.File
import java.util.Locale

// One hour ahead of now: a zero-time (failure) frame falls back to System.nanoTime(), which must lie
// before every synthetic frame, exactly as time.Now() does on the Go side.
private val BASE_MS = System.nanoTime() / 1_000_000L + 3_600_000L
private fun nanos(ms: Long) = (BASE_MS + ms) * 1_000_000L

private class Spec {
    var count = 1; var step = 50L
    var left = 4; var right = 4; var beads = ""; var leftSlots = 4; var rightSlots = 4
    var meta: Pair<Int, Int>? = null
    var fight = true; var hold = false; var scene = "fight"; var profile = "duel"
    var width = 1280; var height = 576; var method = "android-media-projection"
    var fail: String? = null; var ro = false
    var ln = ""; var rn = ""; var lc = ""; var rc = ""; var ps = ""; var pn = ""; var on = ""
    var dup = false; var ts = 0L; var shot: Pair<Long, Long>? = null
}

private fun pair(v: String, sep: Char): Pair<String, String> = v.substringBefore(sep) to v.substringAfter(sep)

private fun parseFrame(tokens: List<String>): Spec {
    val s = Spec()
    for (tok in tokens) {
        when {
            tok.startsWith("x") && tok.drop(1).all(Char::isDigit) -> s.count = tok.drop(1).toInt()
            tok.startsWith("+") -> s.step = tok.drop(1).toLong()
            else -> {
                val (k, v) = pair(tok, '=')
                when (k) {
                    "beads" -> if (v == "none" || v == "unknown") s.beads = v else pair(v, '/').let {
                        s.left = it.first.toInt(); s.right = it.second.toInt()
                    }
                    "slots" -> pair(v, '/').let { s.leftSlots = it.first.toInt(); s.rightSlots = it.second.toInt() }
                    "meta" -> pair(v, '/').let { s.meta = it.first.toInt() to it.second.toInt() }
                    "fight" -> s.fight = v == "1"
                    "hold" -> s.hold = v == "1"
                    "scene" -> s.scene = v
                    "profile" -> s.profile = v
                    "size" -> pair(v, 'x').let { s.width = it.first.toInt(); s.height = it.second.toInt() }
                    "method" -> s.method = v
                    "fail" -> s.fail = v
                    "ro" -> s.ro = v == "1"
                    "ln" -> s.ln = v
                    "rn" -> s.rn = v
                    "lc" -> s.lc = v
                    "rc" -> s.rc = v
                    "ps" -> s.ps = v
                    "pn" -> s.pn = v
                    "on" -> s.on = v
                    "dup" -> s.dup = v == "1"
                    "ts" -> s.ts = v.toLong()
                    "shot" -> pair(v, '/').let { s.shot = it.first.toLong() to it.second.toLong() }
                    else -> error("unknown frame key $k")
                }
            }
        }
    }
    return s
}

private fun beadList(s: Spec): List<BeadInfo> {
    if (s.beads == "none") return emptyList()
    val unknown = s.beads == "unknown"
    val out = ArrayList<BeadInfo>()
    for (i in 1..s.leftSlots) out += BeadInfo(100 + i * 10, 50, "L$i", !unknown && i <= s.left, false, unknown, 1.0)
    for (i in 1..s.rightSlots) out += BeadInfo(1100 - i * 10, 50, "R$i", !unknown && i <= s.right, false, unknown, 1.0)
    return out
}

/** A capture/CaptureMeta.kt helper (pure functions), or null before it existed. */
private fun captureMeta(name: String, vararg types: Class<*>): ((Array<Any>) -> Any)? = try {
    val cls = Class.forName("com.narutotimer.capture.CaptureMeta")
    val method = cls.getMethod(name, *types)
    val obj = cls.getField("INSTANCE").get(null);
    { args -> method.invoke(obj, *args) }
} catch (_: ClassNotFoundException) {
    null
} catch (_: NoSuchMethodException) {
    null
}

private val screenshotMeta = captureMeta("screenshotNanos", Long::class.java, Long::class.java, Long::class.java)

/** Pre-fix production stamped screenshot frames in the callback (System.nanoTime() in onSuccess). */
private fun screenshotNanos(requestedAt: Long, takenMs: Long, callbackAt: Long): Long =
    screenshotMeta?.invoke(arrayOf(requestedAt, takenMs, callbackAt)) as Long? ?: callbackAt

private fun buildFrame(s: Spec, ms: Long, seq: Long): AnalysisResult {
    // Failure frames name the frame's capture source (production: FrameLoop via CaptureMeta.methodName).
    s.fail?.let { return AnalysisResult.failure(it, nanos(ms), s.method) }
    val at = s.shot?.let { (taken, callback) -> screenshotNanos(nanos(ms), BASE_MS + ms + taken, nanos(ms + callback)) }
        ?: nanos(ms + s.ts)
    val beads = beadList(s)
    val meta = s.meta ?: if (s.beads == "none") 0 to 0 else s.leftSlots to s.rightSlots
    return AnalysisResult(
        fighting = s.fight, uncertain = s.hold, beads = beads, name = "rgb-${s.profile}+gate", layoutProfile = s.profile,
        scene = s.scene, gateScore = 1.0, roundOpening = s.ro, leftNinja = s.ln, rightNinja = s.rn,
        leftNinjaCandidate = s.lc, rightNinjaCandidate = s.rc, leftSlots = meta.first, rightSlots = meta.second,
        playerSide = s.ps, playerName = s.pn, oppName = s.on, hold = s.hold, err = null, status = "",
        width = s.width, height = s.height, captureStartedNanos = at, capturedAtNanos = at,
        analysisStartedNanos = at, analyzedAtNanos = at, captureMethod = s.method,
        sequence = seq, duplicate = s.dup, reused = false,
    )
}

private inline fun <reified T> Session.field(name: String): T =
    Session::class.java.getDeclaredField(name).let { it.isAccessible = true; it.get(this) as T }

private fun b(v: Boolean) = if (v) 1 else 0
private fun dash(v: String) = v.ifEmpty { "-" }
private fun colorName(c: Int) = when (c) {
    TimerColors.CLOCK_WARN -> "warn"
    TimerColors.CLOCK_LIVE -> "live"
    else -> "idle"
}

private fun clock(c: SideClock, now: Long): String =
    "${c.eventCount()}[${c.latestRemaining(now).joinToString(",") { String.format(Locale.ROOT, "%.3f", it) }}]"

private fun report(s: Session, name: String, index: Int, ms: Long): String {
    val now = nanos(ms)
    val st = s.overlayState(now)
    val beads = Regex("豆 左(.+?)/右(.+)$").find(st.infoText)?.destructured?.let { (l, r) -> "$l/$r" } ?: "?/?"
    val tag = st.tagText.substringAfter(" · ", "-")
    val alt = if (st.dual) "${dash(st.altText)}(${colorName(st.altColor)})" else "-(idle)"
    return "$name #$index @${ms}ms: fight=${b(s.fighting)} side=${dash(s.side)} " +
        "L=${clock(s.field("leftClock"), now)} R=${clock(s.field("rightClock"), now)} " +
        "dual=${b(st.dual)} alt=$alt tag=$tag beads=$beads | " +
        "freeze=${b(s.field("holdFreeze"))} inherit=${b(s.field("inheritOnReturn"))} " +
        "sync=${b(s.field("syncLeft"))}${b(s.field("syncRight"))} leave=${s.field<Int>("leaveHits")} " +
        "fightHits=${s.field<Int>("fightHits")} profile=${dash(s.field("layoutProfile"))} " +
        "slots=${s.field<Int>("leftSlots")}/${s.field<Int>("rightSlots")}"
}

fun main(args: Array<String>) {
    val out = StringBuilder()
    var session: Session? = null
    var settings: Settings? = null
    var name = ""
    var reports = 0
    var t = 0L
    var seq = 0L
    fun current(): Session = session ?: Session(settings!!).also { session = it }
    for (raw in File(args[0]).readLines()) {
        val line = raw.trim()
        if (line.isEmpty() || line.startsWith("#")) continue
        val tokens = line.split(Regex("\\s+"))
        when (tokens[0]) {
            "==" -> {
                name = line.removePrefix("==").trim(); reports = 0; t = 0; session = null
                settings = Settings(Context()).also { it.rememberSide = true; it.playerSide = "left" }
            }
            "set" -> for (tok in tokens.drop(1)) {
                val (k, v) = pair(tok, '=')
                when (k) {
                    "side" -> settings!!.playerSide = v
                    "names" -> settings!!.playerNames = v.split('|')
                    "ninja" -> settings!!.ninjaQuery = v
                    else -> error("unknown setting $k")
                }
            }
            "f" -> {
                val spec = parseFrame(tokens.drop(1))
                repeat(spec.count) { current().onFrame(buildFrame(spec, t, ++seq)); t += spec.step }
            }
            "wait" -> t += tokens[1].toLong()
            "report" -> out.append(report(current(), name, ++reports, t)).append('\n')
            else -> error("unknown line: $line")
        }
    }
    // Failure frames must name the capture source like native frames do (nt::frame::MethodName).
    if (args.size > 1) {
        val names = captureMeta("methodName", Int::class.java)?.let { m ->
            listOf(NativeCore.METHOD_PROJECTION, NativeCore.METHOD_SCREENSHOT, NativeCore.METHOD_BUFFER)
                .joinToString(",") { m(arrayOf(it)) as String }
        } ?: "missing (failure frames say \"native\" / the method number)"
        out.append("failure-frame methods: ${if (names == args[1]) "same as native" else "$names != native ${args[1]}"}\n")
    }
    print(out)
}
