package clockcheck

import com.narutotimer.tracker.SideClock
import java.io.File
import kotlin.system.exitProcess

// Kotlin side of the SideClock parity check: replays scenarios.in through the
// production tracker/SideClock.kt and prints exactly what main.go prints for
// internal/app.SideClock. run.sh compares both outputs byte for byte.

private const val MS = 1_000_000L

// Every scenario time is non-zero (0L means "now" to SideClock).
private const val BASE = 1_700_000_000_000L * MS

private fun at(ms: Long): Long = BASE + ms * MS

private fun rel(t: Long): String = if (t == 0L) "-" else (t - BASE).toString()

private fun micros(secs: List<Double>): String =
    secs.joinToString(" ", prefix = "[", postfix = "]") { Math.round(it * 1e6).toString() }

private fun fail(message: String): Nothing {
    System.err.println(message)
    exitProcess(1)
}

fun main(args: Array<String>) {
    if (args.size != 1) {
        System.err.println("usage: ClockCheck scenarios.in")
        exitProcess(2)
    }
    val out = System.out.bufferedWriter()
    var c: SideClock? = null
    var name = ""
    var now = 0L
    var step = 0
    fun state(op: String) {
        val clock = c!!
        step++
        val t = at(now)
        val (eventAt, serial) = clock.lastEvent()
        out.write(
            "$name #$step $op ready=${clock.lastReady()} events=${clock.eventCount()} serial=$serial last=${rel(eventAt)} " +
                "remaining=${micros(clock.remaining(t))} latest=${micros(clock.latestRemaining(t))} key=${clock.displayKey(t)} " +
                "active=${clock.active()} cd=${SideClock.formatCD(clock.latestRemaining(t))}\n"
        )
    }
    File(args[0]).readLines().forEachIndexed { index, text ->
        val line = index + 1
        val fields = text.trim().split(Regex("\\s+")).filter { it.isNotEmpty() }
        if (fields.isEmpty() || fields[0].startsWith("#")) return@forEachIndexed
        fun n(i: Int, fallback: Long): Long =
            if (i >= fields.size) fallback else fields[i].toLongOrNull() ?: fail("line $line: bad number ${fields[i]}")
        if (fields[0] == "scenario") {
            c = SideClock()
            name = fields[1]
            now = 0L
            step = 0
            return@forEachIndexed
        }
        val clock = c ?: fail("line $line: no scenario")
        when (fields[0]) {
            "gap" -> {
                clock.setObservationGap(n(1, 0) * MS)
                state("gap")
            }
            "obs" -> {
                now = n(2, 0)
                clock.observe(n(1, 0).toInt(), n(5, 1) != 0L, at(now), n(3, 15000) * MS, n(4, 2).toInt())
                state("obs ${fields[1]}")
            }
            "frames" -> {
                val ready = n(1, 0)
                val from = n(2, 0)
                val every = n(3, 0)
                val count = n(4, 0)
                val need = n(5, 2)
                for (i in 0 until count) {
                    now = from + i * every
                    clock.observe(ready.toInt(), true, at(now), 15_000 * MS, need.toInt())
                    state("obs ${fields[1]}")
                }
            }
            "inv" -> {
                now = n(1, 0)
                clock.invalidateObservation(at(now))
                state("inv")
            }
            "hold" -> {
                now = n(2, 0)
                val ok = clock.holdObservation(n(1, 0).toInt(), at(now))
                state("hold ${fields[1]} $ok")
            }
            "resume" -> {
                now = n(1, 0)
                val ok = clock.resumeInheritedObservation(at(now))
                state("resume $ok")
            }
            "sync" -> {
                clock.syncReady(n(1, 0).toInt())
                state("sync ${fields[1]}")
            }
            "resync" -> {
                clock.resyncObservation()
                state("resync")
            }
            "round" -> {
                clock.resetRound()
                state("round")
            }
            "reset" -> {
                clock.reset()
                state("reset")
            }
            else -> fail("line $line: unknown op ${fields[0]}")
        }
    }
    out.flush()
}
