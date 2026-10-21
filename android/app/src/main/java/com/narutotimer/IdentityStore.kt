package com.narutotimer

import com.narutotimer.tracker.GoText
import java.io.File
import java.util.Locale
import kotlin.math.abs
import kotlin.math.min

/**
 * Account-name image storage under filesDir/identity (pure JVM, checked on the host by
 * android/tools/appcheck). AssetLoader registers every image there with the native engine.
 *
 * - `seen/` holds opponents' name strips that AssetLoader.drainOppCrop collects automatically.
 *   It keeps only the newest [SEEN_CAP] (the number the Go audit GE-05 proposes): older ones are
 *   deleted after each new crop and before every asset load, so startup time, native memory and
 *   opponent matching stay bounded (audit AND-05). [clearSeen] deletes them all (main screen,
 *   「清除自动收集」).
 * - Images must pass [rejectReason] (audit AND-06): a name strip is about 300×40 px, while a
 *   48 MP photo needs about 200 MB to decode and used to leave the engine FAILED at every start.
 * - Images are optional: a name typed in 「我的名字」 is read in the battle HUD title by the native
 *   text reader (nt/hudtext.h). An image labelled with a typed name ([labelFromFile], Go
 *   identity.LoadBook) also matches on the VS loading screen; [importedLabels] tells the UI which
 *   names have one. The engine reads an image at the 1920×1080 HUD scale, so a crop of this phone's
 *   own screenshot is brought to it on import ([importScale]).
 */
object IdentityStore {
    const val DIR = "identity"
    const val SEEN = "seen"
    const val FIGHT = "fight"
    const val SEEN_CAP = 200
    /** Largest accepted image file. */
    const val MAX_FILE_BYTES = 16L * 1024 * 1024
    /** Largest accepted side and pixel count (16 MB of RGBA; a full 2400×1080 screenshot still fits). */
    const val MAX_SIDE = 4096
    const val MAX_PIXELS = 4L * 1024 * 1024

    fun isImageName(name: String): Boolean {
        val low = name.lowercase(Locale.ROOT)
        return low.endsWith(".png") || low.endsWith(".jpg") || low.endsWith(".jpeg")
    }

    fun root(filesDir: File): File = File(filesDir, DIR)
    fun seenDir(filesDir: File): File = File(root(filesDir), SEEN)

    /**
     * Why an image of [width]×[height] px stored in [bytes] bytes is refused, as a short Chinese
     * reason for the user (fits a two-line toast); null = accepted. A size ≤ 0 means "not decodable as an image".
     */
    fun rejectReason(width: Int, height: Int, bytes: Long): String? = when {
        bytes > MAX_FILE_BYTES -> fileSizeReason(bytes)
        width <= 0 || height <= 0 -> "不是 PNG/JPG 图片"
        width > MAX_SIDE || height > MAX_SIDE || width.toLong() * height > MAX_PIXELS ->
            "图片过大 ${width}×${height}，请导入账号名裁剪图"
        else -> null
    }

    /** The file-size part of [rejectReason] alone (checked before reading a file); null = small enough. */
    fun fileSizeReason(bytes: Long): String? = if (bytes <= MAX_FILE_BYTES) null else FILE_TOO_BIG

    /** Reason for a file over [MAX_FILE_BYTES] (also when the copy stopped at the limit). */
    val FILE_TOO_BIG = "文件超过 ${MAX_FILE_BYTES / (1024 * 1024)} MB"

    /** Deletes all but the newest [SEEN_CAP] collected crops; returns how many were deleted. */
    fun pruneSeen(filesDir: File): Int = Housekeeping.pruneNewest(seenDir(filesDir), SEEN_CAP) { isImageName(it.name) }

    /** Deletes every collected crop (and unfinished writes); returns how many images were deleted. */
    fun clearSeen(filesDir: File): Int {
        val dir = seenDir(filesDir)
        Housekeeping.deleteFiles(dir) { it.name.endsWith(TMP_SUFFIX) }
        return Housekeeping.deleteFiles(dir) { isImageName(it.name) }
    }

    /** Suffix of a crop being written (renamed to its final name once complete; never loaded). */
    const val TMP_SUFFIX = ".tmp"

    /**
     * Go identity.labelFromFile: the file name without its extension, cut at the first '_' unless
     * the name starts with it, then NormalizeName. `白方小_20261021-120000-0.png` → `白方小`.
     */
    fun labelFromFile(name: String): String {
        var base = name.substringAfterLast('/')
        val dot = base.lastIndexOf('.')
        if (dot >= 0) base = base.substring(0, dot)
        val cut = base.indexOf('_')
        if (cut > 0) base = base.substring(0, cut)
        return GoText.normalizeName(base)
    }

    /**
     * Labels of the account-name images that can mark 我方: the imported ones (`identity/`, read on
     * the VS loading screen) and `identity/fight/` (top battle HUD). Collected opponents (`seen/`)
     * never carry a configured name.
     */
    fun importedLabels(filesDir: File): Set<String> {
        val out = HashSet<String>()
        for (dir in listOf(root(filesDir), File(root(filesDir), FIGHT))) {
            dir.listFiles()?.forEach { f -> if (f.isFile && isImageName(f.name)) labelFromFile(f.name).takeIf { it.isNotEmpty() }?.let(out::add) }
        }
        return out
    }

    /** The configured names (NormalizeName) that have no image in [labels]: auto side detection cannot use them. */
    fun namesWithoutImage(names: List<String>, labels: Set<String>): List<String> =
        names.map(GoText::normalizeName).filter { it.isNotEmpty() && it !in labels }.distinct()

    /**
     * Factor that brings a crop of a native screenshot of a [screenWidth]×[screenHeight] px screen
     * (either orientation) to the 1920×1080 HUD scale of account-name images: the engine scales an
     * image by the capture's HUD scale min(W/1920, H/1080), so an image cut from a 1440 px high
     * screen was a third too large and never matched. 1.0 for 1080 px high screens and within 2 %.
     */
    fun importScale(screenWidth: Int, screenHeight: Int): Double {
        val long = maxOf(screenWidth, screenHeight)
        val short = minOf(screenWidth, screenHeight)
        if (long <= 0 || short <= 0) return 1.0
        val hud = min(long / 1920.0, short / 1080.0)
        return if (abs(hud - 1.0) <= 0.02) 1.0 else 1.0 / hud
    }
}
