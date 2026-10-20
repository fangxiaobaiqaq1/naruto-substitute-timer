package com.narutotimer.ui

import android.content.Context
import android.graphics.Bitmap
import android.graphics.ImageDecoder
import android.net.Uri
import android.util.Log
import java.io.File
import java.io.FileOutputStream
import kotlin.math.max
import kotlin.math.roundToInt

/**
 * Frame files for [CalibrationActivity] in cacheDir/calibration. The Intent carries the
 * path, so a recreated or restarted activity reloads the same frame. A live snapshot is
 * also held in memory while its editor is open: the editor shows it at once and the PNG
 * (only needed after process death) is written in the background.
 */
object CalibrationFrames {
    private const val TAG = "CalibrationFrames"
    private const val DIR = "calibration"
    /** Longest side kept/shown: bounded memory and always uploadable as a texture. */
    private const val MAX_SIDE = 4096

    private val lock = Any()
    private var heldPath: String? = null
    private var heldBitmap: Bitmap? = null

    /** Takes ownership of [snapshot]; returns the file the editor should be opened with. */
    fun holdLive(context: Context, snapshot: Bitmap): File {
        val dir = dir(context)
        val file = File(dir, "live-${System.currentTimeMillis()}.png")
        val frame = capped(snapshot)
        synchronized(lock) {
            heldPath = file.path
            heldBitmap = frame
        }
        keepOnly(context, file.path)
        Thread({ write(frame, file) }, "nt-calib-save").start()
        return file
    }

    /** The in-memory frame for [path], if this process still holds it. */
    fun held(path: String): Bitmap? = synchronized(lock) { if (heldPath == path) heldBitmap else null }

    /** Copies a picked image into the cache; null if it cannot be read. */
    fun import(context: Context, uri: Uri): File? {
        val file = File(dir(context), "file-${System.currentTimeMillis()}.img")
        return try {
            val input = context.contentResolver.openInputStream(uri) ?: return null
            input.use { src -> FileOutputStream(file).use { src.copyTo(it) } }
            file
        } catch (e: Exception) {
            Log.w(TAG, "import $uri failed", e)
            file.delete()
            null
        }
    }

    /** Decodes a frame file as a software bitmap (downscaled above [MAX_SIDE]); null if unreadable. */
    fun decode(file: File): Bitmap? {
        if (!file.isFile) return null
        return try {
            ImageDecoder.decodeBitmap(ImageDecoder.createSource(file)) { decoder, info, _ ->
                decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
                val w = info.size.width
                val h = info.size.height
                val side = max(w, h)
                if (side > MAX_SIDE) {
                    val k = MAX_SIDE.toDouble() / side
                    decoder.setTargetSize(max(1, (w * k).roundToInt()), max(1, (h * k).roundToInt()))
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "decode ${file.name} failed", e)
            null
        } catch (e: OutOfMemoryError) {
            Log.w(TAG, "decode ${file.name} out of memory", e)
            null
        }
    }

    /** Deletes every other frame file (stale editors, interrupted writes). */
    fun keepOnly(context: Context, path: String) {
        dir(context).listFiles()?.forEach { f ->
            if (f.path != path && f.path != "$path.tmp") f.delete()
        }
    }

    /** The editor finished with [path]: drop the memory copy and the file. */
    fun discard(path: String) {
        synchronized(lock) {
            if (heldPath == path) {
                heldPath = null
                heldBitmap = null
            }
        }
        File(path).delete()
    }

    private fun dir(context: Context): File = File(context.cacheDir, DIR).apply { mkdirs() }

    private fun capped(bitmap: Bitmap): Bitmap {
        val side = max(bitmap.width, bitmap.height)
        if (side <= MAX_SIDE) return bitmap
        val k = MAX_SIDE.toDouble() / side
        val scaled = Bitmap.createScaledBitmap(
            bitmap,
            max(1, (bitmap.width * k).roundToInt()),
            max(1, (bitmap.height * k).roundToInt()),
            true,
        )
        if (scaled !== bitmap) bitmap.recycle()
        return scaled
    }

    private fun write(bitmap: Bitmap, file: File) {
        val tmp = File(file.path + ".tmp")
        val ok = try {
            FileOutputStream(tmp).use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        } catch (e: Exception) {
            Log.w(TAG, "write ${file.name} failed", e)
            false
        }
        synchronized(lock) {
            // Rename only while an editor still uses this frame (atomic: never a partial PNG).
            if (ok && heldPath == file.path && tmp.renameTo(file)) return
        }
        tmp.delete()
    }
}
