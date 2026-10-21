// Host checks for the pure-Kotlin parts of the Android app's capture / identity / update / loader
// code (audit track T3). Run: android/tools/appcheck/run.sh
package appcheck

import com.narutotimer.Housekeeping
import com.narutotimer.IdentityStore
import com.narutotimer.NewestWins
import com.narutotimer.capture.CaptureSize
import com.narutotimer.update.ApkCheck
import java.io.ByteArrayInputStream
import java.io.File
import java.nio.file.Files
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.system.exitProcess

private var failures = 0
private var checks = 0

private fun check(name: String, ok: Boolean, detail: () -> String = { "" }) {
    checks++
    if (!ok) {
        failures++
        println("FAIL: $name ${detail()}")
    }
}

private fun <T> eq(name: String, got: T, want: T) = check(name, got == want) { "got=$got want=$want" }

/** v0.25 ProjectionCaptureService: width 1280, height from the current window bounds. */
private fun legacySize(w: Int, h: Int): Pair<Int, Int> =
    1280 to (1280L * h / maxOf(1, w)).toInt().coerceAtLeast(1)

private fun captureSize() {
    // AND-01: a portrait start used to give 1280×2844 (game letterboxed into a 1280×576 band).
    eq("legacy portrait size (the bug)", legacySize(1080, 2400), 1280 to 2844)
    eq("portrait 1080x2400", CaptureSize.landscape(1080, 2400), 1280 to 576)
    eq("landscape 2400x1080", CaptureSize.landscape(2400, 1080), 1280 to 576)
    eq("portrait tablet 1600x2560", CaptureSize.landscape(1600, 2560), 1280 to 800)
    eq("square 1840x1840", CaptureSize.landscape(1840, 1840), 1280 to 1280)
    eq("degenerate 0x0", CaptureSize.landscape(0, 0), 1280 to 1)
    eq("degenerate 2400x0", CaptureSize.landscape(2400, 0), 1280 to 1)
    // Parity: a landscape start keeps exactly the v0.25 size, and portrait == its landscape twin.
    var landscapeSame = true
    var orientationFree = true
    for (w in 320..4000 step 7) for (h in 200..w step 13) {
        if (CaptureSize.landscape(w, h) != legacySize(w, h)) landscapeSame = false
        if (CaptureSize.landscape(h, w) != CaptureSize.landscape(w, h)) orientationFree = false
    }
    check("landscape sizes identical to v0.25", landscapeSame)
    check("portrait start == landscape start", orientationFree)
}

private fun tempDir(name: String): File = Files.createTempDirectory("appcheck-$name").toFile()

private fun identityStore() {
    val files = tempDir("identity")
    try {
        val seen = IdentityStore.seenDir(files).apply { mkdirs() }
        val base = 1_700_000_000_000L
        // 250 crops, one per second; plus files the pruning must not touch.
        for (i in 0 until 250) File(seen, "opp-%03d.png".format(i)).apply { writeText("x"); setLastModified(base + i * 1000L) }
        File(seen, "notes.txt").writeText("keep")
        File(seen, "opp-999.png.tmp").writeText("partial")
        val mine = File(IdentityStore.root(files), "我_1.png").apply { writeText("mine") }
        eq("pruneSeen deletes the oldest 50", IdentityStore.pruneSeen(files), 50)
        val left = seen.listFiles()!!.filter { IdentityStore.isImageName(it.name) }.map { it.name }.sorted()
        eq("pruneSeen keeps 200", left.size, IdentityStore.SEEN_CAP)
        eq("pruneSeen keeps the newest", left.first(), "opp-050.png")
        check("pruneSeen leaves other files", File(seen, "notes.txt").isFile && File(seen, "opp-999.png.tmp").isFile && mine.isFile)
        eq("pruneSeen at cap is a no-op", IdentityStore.pruneSeen(files), 0)
        // Same timestamp: name order breaks the tie (newest name kept).
        val tie = tempDir("tie")
        for (n in listOf("a.png", "b.png", "c.png")) File(tie, n).apply { writeText("x"); setLastModified(base) }
        eq("tie prune count", Housekeeping.pruneNewest(tie, 2), 1)
        check("tie keeps b,c", !File(tie, "a.png").exists() && File(tie, "b.png").exists() && File(tie, "c.png").exists())
        tie.deleteRecursively()
        eq("clearSeen deletes every crop", IdentityStore.clearSeen(files), 200)
        check("clearSeen removes partial writes, keeps other files and imported images",
            !File(seen, "opp-999.png.tmp").exists() && File(seen, "notes.txt").isFile && mine.isFile)
        eq("clearSeen on a missing dir", IdentityStore.clearSeen(tempDir("none")), 0)
    } finally {
        files.deleteRecursively()
    }
    // AND-06 limits.
    eq("name strip accepted", IdentityStore.rejectReason(300, 40, 20_000), null)
    eq("2400x1080 screenshot accepted", IdentityStore.rejectReason(2400, 1080, 3_000_000), null)
    check("48 MP photo refused", IdentityStore.rejectReason(8000, 6000, 12_000_000)?.contains("8000×6000") == true)
    check("long strip refused", IdentityStore.rejectReason(5000, 10, 10_000) != null)
    check("huge file refused", IdentityStore.rejectReason(300, 40, 17L * 1024 * 1024)?.contains("MB") == true)
    check("undecodable refused", IdentityStore.rejectReason(0, 0, 10) != null)
}

private fun housekeeping() {
    val dir = tempDir("cache")
    try {
        val now = 1_800_000_000_000L
        File(dir, "live-1.png").apply { writeText("old"); setLastModified(now - 2 * 3_600_000L) }
        File(dir, "live-1.png.json").apply { writeText("old"); setLastModified(now - 3_600_001L) }
        File(dir, "file-2.img").apply { writeText("new"); setLastModified(now - 60_000L) }
        File(dir, "sub").mkdirs()
        eq("deleteOlderThan 1h", Housekeeping.deleteOlderThan(dir, 3_600_000L, now), 2)
        check("recent file and subdir kept", File(dir, "file-2.img").isFile && File(dir, "sub").isDirectory)
        // copyLimited: within, at and over the limit.
        val out = File(dir, "copy.bin")
        check("copy under limit", Housekeeping.copyLimited(ByteArrayInputStream(ByteArray(1000)), out, 1001) && out.length() == 1000L)
        check("copy at limit", Housekeeping.copyLimited(ByteArrayInputStream(ByteArray(1001)), out, 1001) && out.length() == 1001L)
        check("copy over limit refused and deleted",
            !Housekeeping.copyLimited(ByteArrayInputStream(ByteArray(200_000)), out, 100_000) && !out.exists())
    } finally {
        dir.deleteRecursively()
    }
}

private fun apkCheck() {
    val ours = setOf("aa".repeat(32))
    val installed = ApkCheck.Installed("com.narutotimer", 30, ours)
    fun archive(pkg: String = "com.narutotimer", code: Long = 31, signers: Set<String> = ours, history: List<String> = emptyList()) =
        ApkCheck.Archive(pkg, code, signers, history)
    eq("valid update", ApkCheck.problem(archive(), installed), null)
    check("unparseable", ApkCheck.problem(null, installed)?.contains("无法解析") == true)
    check("other package", ApkCheck.problem(archive(pkg = "com.evil.app"), installed)?.contains("com.evil.app") == true)
    check("other signer", ApkCheck.problem(archive(signers = setOf("bb".repeat(32))), installed)?.contains("签名") == true)
    check("unsigned", ApkCheck.problem(archive(signers = emptySet()), installed)?.contains("签名") == true)
    check("extra signer", ApkCheck.problem(archive(signers = ours + "cc".repeat(32)), installed)?.contains("签名") == true)
    check("same versionCode", ApkCheck.problem(archive(code = 30), installed)?.contains("versionCode 30") == true)
    check("downgrade", ApkCheck.problem(archive(code = 29), installed)?.contains("不高于") == true)
    check("own signers unreadable", ApkCheck.problem(archive(), ApkCheck.Installed("com.narutotimer", 30, emptySet())) != null)
    val rotated = "dd".repeat(32)
    eq("rotated key with lineage", ApkCheck.problem(archive(signers = setOf(rotated), history = listOf(ours.first(), rotated)), installed), null)
    check("rotated key without our cert in lineage",
        ApkCheck.problem(archive(signers = setOf(rotated), history = listOf("ee".repeat(32), rotated)), installed) != null)
    // Package is checked before signer, signer before version (the most telling reason first).
    check("order: package first", ApkCheck.problem(archive(pkg = "x.y", code = 1, signers = setOf("bb")), installed)?.contains("包名") == true)
    check("order: signer before version", ApkCheck.problem(archive(code = 1, signers = setOf("bb")), installed)?.contains("签名") == true)
}

private fun newestWins() {
    val exec = Executors.newSingleThreadExecutor()
    try {
        val gate = NewestWins(exec)
        val firstRunning = CountDownLatch(1)
        val release = CountDownLatch(1)
        val ran = Collections.synchronizedList(ArrayList<Int>())
        val published = Collections.synchronizedList(ArrayList<Int>())
        val begun = Collections.synchronizedList(ArrayList<Int>())
        fun job(id: Int, block: Boolean = false) = gate.submit(
            begin = { begun += id },
            work = {
                ran += id
                if (block) { firstRunning.countDown(); release.await(5, TimeUnit.SECONDS) }
                id
            },
            publish = { published += it },
        )
        job(1, block = true)
        check("first job started", firstRunning.await(5, TimeUnit.SECONDS))
        job(2) // Superseded by 3 before its turn: skipped.
        job(3)
        release.countDown()
        exec.submit {}.get(5, TimeUnit.SECONDS) // Drain the queue.
        eq("begin runs for every request", begun.toList(), listOf(1, 2, 3))
        eq("superseded queued job skipped", ran.toList(), listOf(1, 3))
        eq("only the newest publishes", published.toList(), listOf(3))
        // A single request publishes.
        val g = job(4)
        exec.submit {}.get(5, TimeUnit.SECONDS)
        check("lone request publishes", published.last() == 4 && gate.isCurrent(g))
    } finally {
        exec.shutdownNow()
    }
}

fun main() {
    captureSize()
    identityStore()
    housekeeping()
    apkCheck()
    newestWins()
    if (failures == 0) {
        println("PASS: $checks checks")
    } else {
        println("$failures of $checks checks failed")
        exitProcess(1)
    }
}
