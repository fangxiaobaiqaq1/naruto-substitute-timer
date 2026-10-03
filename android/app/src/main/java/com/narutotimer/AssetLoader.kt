package com.narutotimer

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.Log
import java.io.File
import java.io.FileOutputStream
import java.nio.ByteBuffer
import java.security.MessageDigest
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * 启动时把识别资源交给 native AssetStore（architect 所有）。
 *
 * - APK assets 根下的 templates/、avatars/、game/、ninja_templates/（由 Gradle syncRecognitionAssets 生成）
 *   中每个 PNG：读原始字节 → 计算 SHA-256（注册为文本 "<path>.sha256"，供 avatars/index.json 校验）→
 *   剥掉 iCCP/sRGB/gAMA/cHRM 色彩块（Go image/png 不做色彩管理，必须拿到原始像素）→
 *   BitmapFactory 解码为 *非预乘* ARGB_8888 → 以 RGBA 字节注册。
 * - 每个 .json 以 UTF-8 文本注册。
 * - 用户账号名图鉴：filesDir/identity/（含 seen/、fight/）下 png/jpg/jpeg，key 为 "identity/<相对路径>"。
 * - 最后设置我方账号名并 nativeFinishAssets() 构建引擎。
 */
object AssetLoader {
    private const val TAG = "AssetLoader"
    private val ROOTS = listOf("templates", "avatars", "game", "ninja_templates")
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

    private var buffer: ByteBuffer = ByteBuffer.allocateDirect(256 * 1024)

    /** 完整加载并构建引擎。耗时（数百毫秒），必须在后台线程调用。 */
    @Synchronized
    fun loadAll(context: Context, playerNames: List<String>): Report {
        val started = System.nanoTime()
        val errors = ArrayList<String>()
        var images = 0
        var texts = 0
        var bytes = 0L
        if (!NativeCore.nativeInit()) errors += "nativeInit: ${NativeCore.lastError()}"
        val am = context.assets
        for (root in ROOTS) {
            for (path in listRecursive(context, root)) {
                try {
                    when {
                        path.endsWith(".png", ignoreCase = true) -> {
                            val raw = am.open(path).use { it.readBytes() }
                            val n = registerImage(path, raw)
                            if (n >= 0) { images++; bytes += n } else errors += "decode $path"
                        }
                        path.endsWith(".json", ignoreCase = true) -> {
                            val text = am.open(path).use { it.readBytes().toString(Charsets.UTF_8) }
                            if (NativeCore.nativeAddText(path, text)) texts++ else errors += "text $path"
                        }
                    }
                } catch (e: Exception) {
                    errors += "$path: ${e.message}"
                }
            }
        }
        images += registerUserIdentity(context, errors)
        NativeCore.setPlayerNames(playerNames)
        val ready = NativeCore.nativeFinishAssets()
        val nativeError = if (ready) "" else NativeCore.lastError()
        val millis = (System.nanoTime() - started) / 1_000_000
        Log.i(TAG, "assets: images=$images texts=$texts bytes=$bytes ${millis}ms ready=$ready errors=${errors.size} $nativeError")
        return Report(images, texts, bytes, millis, errors, ready, nativeError)
    }

    /** 注册 filesDir/identity 下的用户图片。返回成功数。 */
    @Synchronized
    fun registerUserIdentity(context: Context, errors: MutableList<String> = ArrayList()): Int {
        val dir = File(context.filesDir, IDENTITY_DIR)
        if (!dir.isDirectory) return 0
        var n = 0
        dir.walkTopDown().filter { it.isFile && isImageName(it.name) }.sortedBy { it.path }.forEach { f ->
            val key = IDENTITY_DIR + "/" + f.relativeTo(dir).invariantSeparatorsPath
            try {
                if (registerImage(key, f.readBytes()) >= 0) n++ else errors += "decode $key"
            } catch (e: Exception) {
                errors += "$key: ${e.message}"
            }
        }
        return n
    }

    /**
     * 取走 native 待保存的对面账号裁剪（Go identity.saveOppCrop），写成
     * filesDir/identity/seen/opp-<yyyyMMdd-HHmmss>.png 并注册回 AssetStore。
     * 由 FrameLoop 在分析线程空闲时调用。返回写入的文件或 null。
     */
    fun drainOppCrop(context: Context): File? {
        val data = NativeCore.nativeTakeOppCrop() ?: return null
        if (data.size < 2) return null
        val w = data[0]
        val h = data[1]
        if (w <= 0 || h <= 0 || data.size < 2 + w * h) return null
        val bmp = Bitmap.createBitmap(data, 2, w, w, h, Bitmap.Config.ARGB_8888)
        val dir = File(context.filesDir, "$IDENTITY_DIR/seen").apply { mkdirs() }
        val name = "opp-" + SimpleDateFormat("yyyyMMdd-HHmmss", Locale.US).format(Date()) + ".png"
        val file = File(dir, name)
        return try {
            FileOutputStream(file).use { bmp.compress(Bitmap.CompressFormat.PNG, 100, it) }
            synchronized(this) { registerImage("$IDENTITY_DIR/seen/$name", file.readBytes()) }
            file
        } catch (e: Exception) {
            Log.w(TAG, "save opp crop failed", e)
            null
        } finally {
            bmp.recycle()
        }
    }

    private fun isImageName(name: String): Boolean {
        val low = name.lowercase(Locale.ROOT)
        return low.endsWith(".png") || low.endsWith(".jpg") || low.endsWith(".jpeg")
    }

    private fun listRecursive(context: Context, root: String): List<String> {
        val out = ArrayList<String>()
        val am = context.assets
        fun walk(path: String) {
            val children = am.list(path) ?: return
            if (children.isEmpty()) {
                out += path
                return
            }
            for (c in children.sorted()) walk("$path/$c")
        }
        walk(root)
        return out
    }

    /** 解码并注册一张图；返回像素字节数，失败返回 -1。 */
    private fun registerImage(path: String, raw: ByteArray): Int {
        if (path.endsWith(".png", ignoreCase = true)) {
            NativeCore.nativeAddText("$path.sha256", sha256Hex(raw))
        }
        val bytes = if (isPng(raw)) stripColorChunks(raw) else raw
        val opts = BitmapFactory.Options().apply {
            inPremultiplied = false
            inPreferredConfig = Bitmap.Config.ARGB_8888
            inScaled = false
            inMutable = false
        }
        var bmp = BitmapFactory.decodeByteArray(bytes, 0, bytes.size, opts) ?: return -1
        if (bmp.config != Bitmap.Config.ARGB_8888) {
            val copy = bmp.copy(Bitmap.Config.ARGB_8888, false)
            bmp.recycle()
            bmp = copy ?: return -1
        }
        try {
            val rowBytes = bmp.rowBytes
            val need = rowBytes * bmp.height
            if (buffer.capacity() < need) buffer = ByteBuffer.allocateDirect(maxOf(need, buffer.capacity() * 2))
            buffer.clear()
            // ARGB_8888 在内存中按 R,G,B,A 字节排列；非预乘位图按原值拷出。
            bmp.copyPixelsToBuffer(buffer)
            buffer.rewind()
            return if (NativeCore.nativeAddImage(path, bmp.width, bmp.height, buffer, rowBytes)) bmp.width * bmp.height * 4 else -1
        } finally {
            bmp.recycle()
        }
    }

    private fun sha256Hex(data: ByteArray): String {
        val d = MessageDigest.getInstance("SHA-256").digest(data)
        val sb = StringBuilder(64)
        for (b in d) {
            val v = b.toInt() and 0xff
            sb.append("0123456789abcdef"[v ushr 4]).append("0123456789abcdef"[v and 0xf])
        }
        return sb.toString()
    }

    private val PNG_SIG = byteArrayOf(0x89.toByte(), 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A)
    private val COLOR_CHUNKS = setOf("iCCP", "sRGB", "gAMA", "cHRM")

    private fun isPng(b: ByteArray): Boolean {
        if (b.size < 8) return false
        for (i in 0 until 8) if (b[i] != PNG_SIG[i]) return false
        return true
    }

    /** 逐块复制 PNG，去掉色彩管理块（CRC 原样保留，块内容不变）。结构异常则原样返回。 */
    private fun stripColorChunks(b: ByteArray): ByteArray {
        val out = java.io.ByteArrayOutputStream(b.size)
        out.write(b, 0, 8)
        var i = 8
        var dropped = false
        while (i + 12 <= b.size) {
            val len = ((b[i].toInt() and 0xff) shl 24) or ((b[i + 1].toInt() and 0xff) shl 16) or
                ((b[i + 2].toInt() and 0xff) shl 8) or (b[i + 3].toInt() and 0xff)
            if (len < 0 || i + 12 + len > b.size) return b
            val type = String(b, i + 4, 4, Charsets.US_ASCII)
            val total = 12 + len
            if (type in COLOR_CHUNKS) dropped = true else out.write(b, i, total)
            i += total
            if (type == "IEND") break
        }
        return if (dropped) out.toByteArray() else b
    }
}
