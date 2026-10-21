package com.narutotimer.ui

import android.content.Context
import android.graphics.Bitmap
import android.graphics.ImageDecoder
import android.net.Uri
import android.util.Log
import com.narutotimer.FrameBox
import com.narutotimer.FrameGeometry
import com.narutotimer.Housekeeping
import com.narutotimer.PixelPoint
import com.narutotimer.PixelRect
import com.narutotimer.SideGeometry
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import kotlin.math.max
import kotlin.math.roundToInt

/**
 * Frame files for [CalibrationActivity] in cacheDir/calibration. The Intent carries the
 * path, so a recreated or restarted activity reloads the same frame. A live snapshot is
 * also held in memory while its editor is open: the editor shows it at once and the PNG
 * (only needed after process death) is written in the background, together with what the
 * engine used on that frame ([Auto], a `.json` file next to the PNG).
 */
object CalibrationFrames {
    private const val TAG = "CalibrationFrames"
    private const val DIR = "calibration"
    /** Longest side kept/shown: bounded memory and always uploadable as a texture. */
    private const val MAX_SIDE = 4096
    private const val AUTO_SUFFIX = ".json"
    /** Largest picked file copied into the cache (AND-15); a game screenshot is a few MB. */
    private const val MAX_IMPORT_BYTES = 64L * 1024 * 1024

    /**
     * A live frame's automatic analysis: the geometry its analysis used (docs/区域校准.md 每帧几何),
     * which boxes (Settings.KEY_CALIB_RECTS order) were calibrated then (for those the geometry's
     * box is the calibration, not the automatic default) and the ninja recognised on each side
     * (index 0 left, 1 right; "" = none).
     */
    class Auto(val geometry: FrameGeometry, val calibrated: BooleanArray, val ninjas: List<String> = listOf("", "")) {
        fun ninja(side: Int): String = ninjas.getOrNull(side).orEmpty()
    }

    /** A frame this process holds for its editor. */
    class Held(val bitmap: Bitmap, val auto: Auto?)

    private val lock = Any()
    private var heldPath: String? = null
    private var heldBitmap: Bitmap? = null
    private var heldAuto: Auto? = null

    /** Takes ownership of [snapshot]; returns the file the editor should be opened with. */
    fun holdLive(context: Context, snapshot: Bitmap, auto: Auto?): File {
        val dir = dir(context)
        val file = File(dir, "live-${System.currentTimeMillis()}.png")
        val frame = capped(snapshot)
        synchronized(lock) {
            heldPath = file.path
            heldBitmap = frame
            heldAuto = auto
        }
        keepOnly(context, file.path)
        Thread({
            // Geometry first: once the PNG exists, so does its geometry (if the frame had one).
            if (auto != null) writeAuto(auto, file)
            write(frame, file)
        }, "nt-calib-save").start()
        return file
    }

    /** The in-memory frame for [path], if this process still holds it. */
    fun held(path: String): Held? = synchronized(lock) {
        val bitmap = heldBitmap
        if (heldPath == path && bitmap != null) Held(bitmap, heldAuto) else null
    }

    /** The geometry saved next to the frame [path]; null if it had none or it cannot be read. */
    fun readAuto(path: String): Auto? {
        val file = autoFile(path)
        if (!file.isFile) return null
        return try {
            decodeAuto(JSONObject(file.readText()))
        } catch (e: Exception) {
            Log.w(TAG, "read ${file.name} failed", e)
            null
        }
    }

    /** Copies a picked image into the cache; null if it cannot be read or is over [MAX_IMPORT_BYTES]. */
    fun import(context: Context, uri: Uri): File? {
        val file = File(dir(context), "file-${System.currentTimeMillis()}.img")
        return try {
            val input = context.contentResolver.openInputStream(uri) ?: return null
            val fits = input.use { src -> Housekeeping.copyLimited(src, file, MAX_IMPORT_BYTES) }
            if (!fits) Log.w(TAG, "import $uri: over ${MAX_IMPORT_BYTES / (1024 * 1024)} MB")
            file.takeIf { fits }
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
        val auto = autoFile(path).path
        val keep = setOf(path, "$path.tmp", auto, "$auto.tmp")
        dir(context).listFiles()?.forEach { f ->
            if (f.path !in keep) f.delete()
        }
    }

    /** The editor finished with [path]: drop the memory copy and the files. */
    fun discard(path: String) {
        synchronized(lock) {
            if (heldPath == path) {
                heldPath = null
                heldBitmap = null
                heldAuto = null
            }
        }
        File(path).delete()
        autoFile(path).delete()
    }

    private fun dir(context: Context): File = File(context.cacheDir, DIR).apply { mkdirs() }

    private fun autoFile(path: String): File = File(path + AUTO_SUFFIX)

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
        commit(tmp, file, ok, file.path)
    }

    private fun writeAuto(auto: Auto, frame: File) {
        val file = autoFile(frame.path)
        val tmp = File(file.path + ".tmp")
        val ok = try {
            tmp.writeText(encodeAuto(auto).toString())
            true
        } catch (e: Exception) {
            Log.w(TAG, "write ${file.name} failed", e)
            false
        }
        commit(tmp, file, ok, frame.path)
    }

    /** Renames [tmp] only while an editor still uses [framePath] (atomic: never a partial file). */
    private fun commit(tmp: File, file: File, ok: Boolean, framePath: String) {
        synchronized(lock) {
            if (ok && heldPath == framePath && tmp.renameTo(file)) return
        }
        tmp.delete()
    }

    // ---- Geometry file (JSON; floats widen exactly to double and read back bit for bit) ----

    private fun encodeAuto(auto: Auto): JSONObject {
        val g = auto.geometry
        return JSONObject()
            .put("width", g.width)
            .put("height", g.height)
            .put("profile", g.layoutProfile)
            .put("sequence", g.sequence)
            .put("calibrated", JSONArray().also { a -> auto.calibrated.forEach { a.put(it) } })
            .put("ninjas", JSONArray().put(auto.ninja(0)).put(auto.ninja(1)))
            .put("left", encodeSide(g.left))
            .put("right", encodeSide(g.right))
    }

    private fun encodeSide(s: SideGeometry): JSONObject = JSONObject().apply {
        put("beads", JSONArray().also { a -> s.beads.forEach { a.put(it.x).put(it.y) } })
        s.nameRoi?.let { put("nameRoi", JSONArray().put(it.x).put(it.y).put(it.width).put(it.height)) }
        s.avatarRoi?.let { put("avatarRoi", JSONArray().put(it.x).put(it.y).put(it.width).put(it.height)) }
        s.beadBox?.let { put("beadBox", box(it)) }
        s.avatarBox?.let { put("avatarBox", box(it)) }
    }

    private fun box(b: FrameBox): JSONArray =
        JSONArray().put(b.x.toDouble()).put(b.y.toDouble()).put(b.width.toDouble()).put(b.height.toDouble())

    private fun decodeAuto(o: JSONObject): Auto {
        val flags = o.getJSONArray("calibrated")
        val geometry = FrameGeometry(
            width = o.getInt("width"),
            height = o.getInt("height"),
            layoutProfile = o.optString("profile"),
            sequence = o.optLong("sequence"),
            left = decodeSide(o.getJSONObject("left")),
            right = decodeSide(o.getJSONObject("right")),
        )
        val ninjas = o.optJSONArray("ninjas")
        return Auto(geometry, BooleanArray(flags.length()) { flags.getBoolean(it) }, List(2) { ninjas?.optString(it).orEmpty() })
    }

    private fun decodeSide(o: JSONObject): SideGeometry {
        val b = o.getJSONArray("beads")
        fun pixels(key: String) = o.optJSONArray(key)?.let { PixelRect(it.getInt(0), it.getInt(1), it.getInt(2), it.getInt(3)) }
        fun box(key: String) = o.optJSONArray(key)?.let {
            FrameBox(it.getDouble(0).toFloat(), it.getDouble(1).toFloat(), it.getDouble(2).toFloat(), it.getDouble(3).toFloat())
        }
        return SideGeometry(
            beads = List(b.length() / 2) { PixelPoint(b.getInt(2 * it), b.getInt(2 * it + 1)) },
            nameRoi = pixels("nameRoi"),
            avatarRoi = pixels("avatarRoi"),
            beadBox = box("beadBox"),
            avatarBox = box("avatarBox"),
        )
    }
}
