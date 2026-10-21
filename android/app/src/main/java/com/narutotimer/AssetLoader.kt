package com.narutotimer

import android.content.Context
import android.content.res.AssetManager
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.io.InputStream
import java.nio.ByteBuffer
import java.security.MessageDigest
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * 启动时把识别资源交给 native AssetStore（architect 所有）。
 *
 * - APK assets 根下的 templates/、avatars/、ninja_templates/（由 Gradle syncRecognitionAssets 生成）
 *   中每个 PNG：读原始字节 →（仅 avatars/：计算 SHA-256，注册为文本 "<path>.sha256"，供 avatars/index.json 校验）→
 *   剥掉 iCCP/sRGB/gAMA/cHRM 色彩块（Go image/png 不做色彩管理，必须拿到原始像素）→
 *   BitmapFactory 解码为 *非预乘* ARGB_8888 → 以 RGBA 字节注册。
 *   templates/ 只加载 manifest.json 引用的 PNG（native scene 目录只按清单读模板）。
 * - 每个 .json 以 UTF-8 文本注册。game/ 下的 .json 仍在 APK 里，但 native 从不读取，不加载。
 * - 用户账号名图鉴：filesDir/identity/（含 seen/、fight/）下 png/jpg/jpeg，key 为 "identity/<相对路径>"。
 *   seen/ 先裁到最新 [IdentityStore.SEEN_CAP] 张；过大的图片跳过并记入 errors，单个文件（含 OOM）
 *   失败不影响其余资源和引擎构建（[IdentityStore]，AND-05/AND-06）。
 * - 最后设置我方账号名（读当时的设置，[applyPlayerNames]）与区域校准，并 nativeFinishAssets() 构建引擎。
 *
 * 读文件、去色彩块和像素拷贝都用复用的暂存缓冲，加载结束即释放（docs/性能优化-android-app.md）。
 */
object AssetLoader {
    private const val TAG = "AssetLoader"
    /** Roots the native core reads; game/ (vision_catalog / substitutes) is never read natively. */
    private val ROOTS = listOf("templates", "avatars", "ninja_templates")
    private const val TEMPLATE_DIR = "templates/"
    private const val TEMPLATE_MANIFEST = "templates/manifest.json"
    /** Only the avatar catalog checks "<path>.sha256" (native ninja/avatar.cpp loadAvatarEntry). */
    private const val SHA256_DIR = "avatars/"
    private const val IDENTITY_DIR = "identity"

    data class Report(
        val images: Int,
        val texts: Int,
        val pixelBytes: Long,
        val millis: Long,
        val errors: List<String>,
        val ready: Boolean,
        val nativeError: String,
    )

    // Scratch reused across files while loading (all under this object's monitor) and dropped by
    // releaseScratch() afterwards: file bytes, the PNG without colour chunks, decoded RGBA pixels.
    private var raw = ByteArray(0)
    private var stripped = ByteArray(0)
    private var pixels: ByteBuffer? = null
    private var sha256: MessageDigest? = null
    /** Makes "read the setting, hand it to native" atomic, so the newest names are always the last applied. */
    private val namesLock = Any()

    /**
     * Hands the current [Settings.playerNames] to native. Native keeps them across engine rebuilds
     * (jni.cpp g_playerNames), so this may run at any time, also while assets load; names saved
     * during a load are no longer lost (AND-08). Blocks on the native lock: never on the main thread.
     */
    fun applyPlayerNames(settings: Settings) = synchronized(namesLock) {
        NativeCore.setPlayerNames(settings.playerNames)
    }

    /** 完整加载并构建引擎。耗时（数百毫秒），必须在后台线程调用。 */
    @Synchronized
    fun loadAll(context: Context, playerNames: List<String>, settings: Settings? = null): Report {
        val started = System.nanoTime()
        val errors = ArrayList<String>()
        var images = 0
        var texts = 0
        var skipped = 0
        var bytes = 0L
        if (!NativeCore.nativeInit()) errors += "nativeInit: ${NativeCore.lastError()}"
        val am = context.assets
        try {
            val templates = templateFiles(am)
            for (root in ROOTS) {
                for (path in listAssets(am, root)) {
                    try {
                        if (path.endsWith(".png", ignoreCase = true)) {
                            if (templates != null && path.startsWith(TEMPLATE_DIR) &&
                                path.substring(TEMPLATE_DIR.length) !in templates) {
                                skipped++
                            } else {
                                val len = readAsset(am, path) // May replace [raw]: read it first.
                                val n = registerImage(path, raw, len)
                                if (n >= 0) { images++; bytes += n } else errors += "decode $path"
                            }
                        } else if (path.endsWith(".json", ignoreCase = true)) {
                            val len = readAsset(am, path)
                            if (NativeCore.nativeAddText(path, String(raw, 0, len, Charsets.UTF_8))) texts++ else errors += "text $path"
                        }
                    } catch (e: Exception) {
                        errors += "$path: ${e.message}"
                    } catch (e: OutOfMemoryError) {
                        errors += "$path: out of memory"
                    }
                }
            }
            images += registerUserIdentityLocked(context, errors)
        } finally {
            releaseScratch()
        }
        val decoded = System.nanoTime()
        if (settings != null) applyPlayerNames(settings) else NativeCore.setPlayerNames(playerNames)
        if (settings != null && !NativeCore.setCalibration(settings)) errors += "calibration: ${NativeCore.lastError()}"
        val ready = NativeCore.nativeFinishAssets()
        val nativeError = if (ready) "" else NativeCore.lastError()
        val now = System.nanoTime()
        val millis = (now - started) / 1_000_000
        Log.i(
            TAG,
            "assets: images=$images texts=$texts skipped=$skipped bytes=$bytes ${millis}ms " +
                "(decode ${(decoded - started) / 1_000_000}ms, engine ${(now - decoded) / 1_000_000}ms) " +
                "ready=$ready errors=${errors.size} $nativeError",
        )
        return Report(images, texts, bytes, millis, errors, ready, nativeError)
    }

    /** 注册 filesDir/identity 下的用户图片。返回成功数。 */
    @Synchronized
    fun registerUserIdentity(context: Context, errors: MutableList<String> = ArrayList()): Int =
        try {
            registerUserIdentityLocked(context, errors)
        } finally {
            releaseScratch()
        }

    private fun registerUserIdentityLocked(context: Context, errors: MutableList<String>): Int {
        val dir = IdentityStore.root(context.filesDir)
        if (!dir.isDirectory) return 0
        val pruned = runCatching { IdentityStore.pruneSeen(context.filesDir) }.getOrDefault(0)
        if (pruned > 0) Log.i(TAG, "identity: deleted $pruned old collected crop(s)")
        var n = 0
        dir.walkTopDown().filter { it.isFile && isImageName(it.name) }.sortedBy { it.path }.forEach { f ->
            val key = IDENTITY_DIR + "/" + f.relativeTo(dir).invariantSeparatorsPath
            try {
                // A file imported before the size check existed must not take the engine down (AND-06).
                val size = f.length()
                val tooBig = IdentityStore.fileSizeReason(size)
                if (tooBig != null) {
                    errors += "$key: $tooBig"
                    return@forEach
                }
                val len = f.inputStream().use { readFully(it, size.toInt()) }
                val problem = boundsProblem(raw, len, size)
                if (problem != null) {
                    errors += "$key: $problem"
                } else if (registerImage(key, raw, len) >= 0) {
                    n++
                } else {
                    errors += "decode $key"
                }
            } catch (e: Exception) {
                errors += "$key: ${e.message}"
            } catch (e: OutOfMemoryError) {
                errors += "$key: out of memory"
            }
        }
        return n
    }

    /** [IdentityStore.rejectReason] for the image in the first [len] bytes of [data]; header only, no pixels decoded. */
    private fun boundsProblem(data: ByteArray, len: Int, size: Long): String? {
        val o = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(data, 0, len, o)
        return IdentityStore.rejectReason(o.outWidth, o.outHeight, size)
    }

    /**
     * Why the identity image [file] cannot be used (too big, not an image), as a short Chinese
     * reason; null = fine. Reads only the header. Used when importing (AND-06).
     */
    fun identityImageProblem(file: File): String? {
        val size = file.length()
        IdentityStore.fileSizeReason(size)?.let { return it }
        val o = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(file.path, o)
        return IdentityStore.rejectReason(o.outWidth, o.outHeight, size)
    }

    /**
     * Brings an imported account-name image to the 1920×1080 HUD scale the engine reads it at:
     * [scale] is [IdentityStore.importScale] of this phone's screen. Rewrites [file] as a PNG scaled
     * by [scale] (a JPG becomes `<name>.png`, written to a temporary file first). Returns the stored
     * file; [file] itself when [scale] is 1 or it cannot be decoded.
     */
    fun scaleIdentityImage(file: File, scale: Double): File {
        if (scale == 1.0) return file
        val src = BitmapFactory.decodeFile(file.path) ?: return file
        val w = Math.round(src.width * scale).toInt().coerceAtLeast(1)
        val h = Math.round(src.height * scale).toInt().coerceAtLeast(1)
        val scaled = Bitmap.createScaledBitmap(src, w, h, true)
        val out = File(file.parentFile, file.name.substringBeforeLast('.') + ".png")
        val tmp = File(file.parentFile, out.name + IdentityStore.TMP_SUFFIX)
        try {
            FileOutputStream(tmp).use {
                if (!scaled.compress(Bitmap.CompressFormat.PNG, 100, it)) throw java.io.IOException("encode ${out.name} failed")
            }
            if (!tmp.renameTo(out)) throw java.io.IOException("rename ${tmp.name} failed")
            if (out != file) file.delete()
        } finally {
            tmp.delete()
            if (scaled !== src) scaled.recycle()
            src.recycle()
        }
        return out
    }

    /**
     * 取走 native 待保存的对面账号裁剪（Go identity.saveOppCrop），写成
     * filesDir/identity/seen/opp-<yyyyMMdd-HHmmss>.png（先写 .tmp 再改名，不留半个文件）并注册回
     * AssetStore；seen/ 只保留最新 [IdentityStore.SEEN_CAP] 张（AND-05）。
     * 由 FrameLoop 在分析线程空闲时调用。返回写入的文件或 null。
     */
    fun drainOppCrop(context: Context): File? {
        val data = NativeCore.nativeTakeOppCrop() ?: return null
        if (data.size < 2) return null
        val w = data[0]
        val h = data[1]
        if (w <= 0 || h <= 0 || data.size < 2 + w * h) return null
        val bmp = Bitmap.createBitmap(data, 2, w, w, h, Bitmap.Config.ARGB_8888)
        val dir = IdentityStore.seenDir(context.filesDir).apply { mkdirs() }
        val name = "opp-" + SimpleDateFormat("yyyyMMdd-HHmmss", Locale.US).format(Date()) + ".png"
        val file = File(dir, name)
        val tmp = File(dir, name + IdentityStore.TMP_SUFFIX)
        return try {
            FileOutputStream(tmp).use { bmp.compress(Bitmap.CompressFormat.PNG, 100, it) }
            if (!tmp.renameTo(file)) throw java.io.IOException("rename ${tmp.name} failed")
            IdentityStore.pruneSeen(context.filesDir)
            val png = file.readBytes()
            synchronized(this) {
                try {
                    registerImage("$IDENTITY_DIR/seen/$name", png, png.size)
                } finally {
                    releaseScratch()
                }
            }
            file
        } catch (e: Exception) {
            Log.w(TAG, "save opp crop failed", e)
            tmp.delete()
            null
        } finally {
            bmp.recycle()
        }
    }

    private fun isImageName(name: String): Boolean = IdentityStore.isImageName(name)

    /**
     * Files under [root], sorted per directory. A child with a file extension we load is a file;
     * only other names are listed again (AssetManager.list scans the APK's entries on every call).
     */
    private fun listAssets(am: AssetManager, root: String): List<String> {
        val out = ArrayList<String>()
        fun walk(path: String) {
            val children = am.list(path) ?: return
            if (children.isEmpty()) {
                out += path
                return
            }
            children.sort()
            for (c in children) {
                val low = c.lowercase(Locale.ROOT)
                if (low.endsWith(".png") || low.endsWith(".json")) out += "$path/$c" else walk("$path/$c")
            }
        }
        walk(root)
        return out
    }

    /**
     * PNG names the template manifest references (any string value ending in .png: today "file"
     * and "mask"); the native scene catalog reads templates only through it (scene.cpp readGray).
     * null when the manifest cannot be read, in which case every template is loaded.
     */
    private fun templateFiles(am: AssetManager): Set<String>? = runCatching {
        val len = readAsset(am, TEMPLATE_MANIFEST)
        val out = HashSet<String>()
        fun collect(v: Any?) {
            when (v) {
                is JSONObject -> v.keys().forEach { collect(v.opt(it)) }
                is JSONArray -> for (i in 0 until v.length()) collect(v.opt(i))
                is String -> if (v.endsWith(".png", ignoreCase = true)) out += v
            }
        }
        collect(JSONObject(String(raw, 0, len, Charsets.UTF_8)))
        out.takeIf { it.isNotEmpty() }
    }.onFailure { Log.w(TAG, "template manifest unreadable; loading every template", it) }.getOrNull()

    /** Reads the APK asset [path] into [raw]; returns its length. */
    private fun readAsset(am: AssetManager, path: String): Int = am.open(path).use { readFully(it, it.available()) }

    /** Reads [input] to its end into [raw] (grown as needed); returns the length. */
    private fun readFully(input: InputStream, sizeHint: Int): Int {
        val hint = sizeHint.coerceIn(0, MAX_SIZE_HINT)
        if (raw.size <= hint) raw = ByteArray(maxOf(hint + 1, MIN_SCRATCH_BYTES))
        var n = 0
        while (true) {
            if (n == raw.size) raw = raw.copyOf(raw.size * 2)
            val r = input.read(raw, n, raw.size - n)
            if (r < 0) return n
            n += r
        }
    }

    private fun releaseScratch() {
        raw = ByteArray(0)
        stripped = ByteArray(0)
        pixels = null
        sha256 = null
    }

    /** 解码并注册 [data] 前 [len] 字节的一张图；返回像素字节数，失败返回 -1。调用方持有本对象的锁。 */
    private fun registerImage(path: String, data: ByteArray, len: Int): Int {
        if (path.startsWith(SHA256_DIR) && path.endsWith(".png", ignoreCase = true)) {
            NativeCore.nativeAddText("$path.sha256", sha256Hex(data, len))
        }
        var src = data
        var srcLen = len
        if (isPng(data, len)) {
            val n = stripColorChunks(data, len)
            if (n >= 0) {
                src = stripped
                srcLen = n
            }
        }
        val opts = BitmapFactory.Options().apply {
            inPremultiplied = false
            inPreferredConfig = Bitmap.Config.ARGB_8888
            inScaled = false
            inMutable = false
        }
        var bmp = BitmapFactory.decodeByteArray(src, 0, srcLen, opts) ?: return -1
        if (bmp.config != Bitmap.Config.ARGB_8888) {
            val copy = bmp.copy(Bitmap.Config.ARGB_8888, false)
            bmp.recycle()
            bmp = copy ?: return -1
        }
        try {
            val rowBytes = bmp.rowBytes
            val need = rowBytes * bmp.height
            var buffer = pixels
            if (buffer == null || buffer.capacity() < need) {
                buffer = ByteBuffer.allocateDirect(maxOf(need, MIN_PIXEL_BYTES, (buffer?.capacity() ?: 0) * 2))
                pixels = buffer
            }
            buffer!!.clear()
            // ARGB_8888 在内存中按 R,G,B,A 字节排列；非预乘位图按原值拷出。
            bmp.copyPixelsToBuffer(buffer)
            buffer.rewind()
            return if (NativeCore.nativeAddImage(path, bmp.width, bmp.height, buffer, rowBytes)) bmp.width * bmp.height * 4 else -1
        } finally {
            bmp.recycle()
        }
    }

    private fun sha256Hex(data: ByteArray, len: Int): String {
        val md = sha256 ?: MessageDigest.getInstance("SHA-256").also { sha256 = it }
        md.update(data, 0, len)
        val d = md.digest()
        val out = CharArray(d.size * 2)
        for (i in d.indices) {
            val v = d[i].toInt() and 0xff
            out[2 * i] = HEX[v ushr 4]
            out[2 * i + 1] = HEX[v and 0xf]
        }
        return String(out)
    }

    private const val HEX = "0123456789abcdef"
    private const val MIN_SCRATCH_BYTES = 128 * 1024
    private const val MIN_PIXEL_BYTES = 256 * 1024
    private const val MAX_SIZE_HINT = 64 * 1024 * 1024
    private val PNG_SIG = byteArrayOf(0x89.toByte(), 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A)

    private fun isPng(b: ByteArray, len: Int): Boolean {
        if (len < 8) return false
        for (i in 0 until 8) if (b[i] != PNG_SIG[i]) return false
        return true
    }

    /** iCCP / sRGB / gAMA / cHRM：Go image/png 不做色彩管理。 */
    private fun isColorChunk(b: ByteArray, at: Int): Boolean =
        chunkIs(b, at, "iCCP") || chunkIs(b, at, "sRGB") || chunkIs(b, at, "gAMA") || chunkIs(b, at, "cHRM")

    private fun chunkIs(b: ByteArray, at: Int, type: String): Boolean {
        for (k in 0 until 4) if (b[at + k] != type[k].code.toByte()) return false
        return true
    }

    /**
     * 逐块复制 PNG 到 [stripped]，去掉色彩管理块（CRC 原样保留，块内容不变），返回新长度；
     * 没有色彩块或结构异常时返回 -1（调用方用原字节）。
     */
    private fun stripColorChunks(b: ByteArray, size: Int): Int {
        if (stripped.size < size) stripped = ByteArray(maxOf(size, MIN_SCRATCH_BYTES))
        val out = stripped
        System.arraycopy(b, 0, out, 0, 8)
        var n = 8
        var i = 8
        var dropped = false
        while (i + 12 <= size) {
            val len = ((b[i].toInt() and 0xff) shl 24) or ((b[i + 1].toInt() and 0xff) shl 16) or
                ((b[i + 2].toInt() and 0xff) shl 8) or (b[i + 3].toInt() and 0xff)
            if (len < 0 || i + 12 + len > size) return -1
            val total = 12 + len
            if (isColorChunk(b, i + 4)) {
                dropped = true
            } else {
                System.arraycopy(b, i, out, n, total)
                n += total
            }
            val end = chunkIs(b, i + 4, "IEND")
            i += total
            if (end) break
        }
        return if (dropped) n else -1
    }
}
