package com.narutotimer

import java.io.File
import java.io.FileOutputStream
import java.io.InputStream

/**
 * Small file-retention helpers (pure JVM, checked on the host by android/tools/appcheck).
 * They only touch regular files directly inside the given directory, never subdirectories.
 */
object Housekeeping {
    /**
     * Deletes the files in [dir] last modified more than [maxAgeMillis] before [nowMillis]
     * (stale calibration frames, update APKs and partial downloads). Returns how many were deleted.
     */
    fun deleteOlderThan(dir: File, maxAgeMillis: Long, nowMillis: Long = System.currentTimeMillis()): Int =
        deleteFiles(dir) { nowMillis - it.lastModified() > maxAgeMillis }

    /** Deletes the files in [dir] that match [filter]; returns how many were deleted. */
    fun deleteFiles(dir: File, filter: (File) -> Boolean = { true }): Int {
        var n = 0
        dir.listFiles()?.forEach { f -> if (f.isFile && filter(f) && f.delete()) n++ }
        return n
    }

    /**
     * Keeps only the newest [keep] files of [dir] that match [filter] (by modification time, then by
     * name, so files written within the same clock tick keep a stable order). Returns how many were deleted.
     */
    fun pruneNewest(dir: File, keep: Int, filter: (File) -> Boolean = { true }): Int {
        val files = dir.listFiles()?.filter { it.isFile && filter(it) } ?: return 0
        if (files.size <= keep) return 0
        val newestFirst = files
            .map { it to it.lastModified() }
            .sortedWith(compareByDescending<Pair<File, Long>> { it.second }.thenByDescending { it.first.name })
        var n = 0
        for ((f, _) in newestFirst.drop(keep.coerceAtLeast(0))) if (f.delete()) n++
        return n
    }

    /**
     * Copies [input] to [out], at most [limit] bytes. Returns false, and deletes [out], when the input
     * is longer than that (the caller reports it as too big); IO errors propagate, also deleting [out].
     */
    fun copyLimited(input: InputStream, out: File, limit: Long): Boolean {
        var ok = false
        try {
            FileOutputStream(out).use { output ->
                val buf = ByteArray(64 * 1024)
                var total = 0L
                while (true) {
                    val n = input.read(buf)
                    if (n < 0) break
                    total += n
                    if (total > limit) return false
                    output.write(buf, 0, n)
                }
            }
            ok = true
            return true
        } finally {
            if (!ok) out.delete()
        }
    }
}
