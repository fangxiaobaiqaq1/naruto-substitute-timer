package com.narutotimer

import java.io.File
import java.util.Locale

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
 */
object IdentityStore {
    const val DIR = "identity"
    const val SEEN = "seen"
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
}
