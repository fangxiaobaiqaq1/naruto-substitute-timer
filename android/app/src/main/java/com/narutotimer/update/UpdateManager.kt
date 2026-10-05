package com.narutotimer.update

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.widget.Toast
import com.narutotimer.BuildConfig
import com.narutotimer.R
import com.narutotimer.Settings as AppSettings
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import java.util.concurrent.Executors

/** Android APK update client. Network and verification never run on the UI thread. */
object UpdateManager {
    enum class Source(val key: String, val label: String, val api: String, val repository: String) {
        GITEE("gitee", "Gitee（国内）", "https://gitee.com/api/v5/repos/xiaobaiqaq/naruto-substitute-timer/releases/latest", "xiaobaiqaq/naruto-substitute-timer"),
        GITHUB("github", "GitHub（海外）", "https://api.github.com/repos/fangxiaobaiqaq1/naruto-substitute-timer/releases/latest", "fangxiaobaiqaq1/naruto-substitute-timer"),
    }

    private data class Asset(val name: String, val url: String, val size: Long, val digest: String?)
    private data class Release(val tag: String, val title: String, val body: String, val page: String, val assets: List<Asset>, val source: Source)
    private const val MAX_RESPONSE = 2L * 1024 * 1024
    private const val MAX_APK = 256L * 1024 * 1024
    private val executor = Executors.newSingleThreadExecutor { r -> Thread(r, "nt-updater") }
    private val main = Handler(Looper.getMainLooper())

    fun source(settings: AppSettings): Source = Source.entries.firstOrNull { it.key == settings.updateSource } ?: Source.GITEE

    fun check(activity: Activity, settings: AppSettings, quiet: Boolean = false) {
        val source = source(settings)
        if (!quiet) Toast.makeText(activity, "正在检查 ${source.label} 更新…", Toast.LENGTH_SHORT).show()
        executor.execute {
            runCatching { checkNow(source) }
                .onSuccess { release -> main.post { showResult(activity, settings, release, quiet) } }
                .onFailure { error -> if (!quiet) main.post { Toast.makeText(activity, "更新检查失败：${error.message}", Toast.LENGTH_LONG).show() } }
        }
    }

    private fun showResult(activity: Activity, settings: AppSettings, release: Release, quiet: Boolean) {
        val cmp = compare(release.tag, BuildConfig.VERSION_NAME)
        if (cmp <= 0) {
            if (!quiet) Toast.makeText(activity, "当前已是最新版本（${BuildConfig.VERSION_NAME}）", Toast.LENGTH_LONG).show()
            return
        }
        AlertDialog.Builder(activity)
            .setTitle("发现新版本 ${release.tag}")
            .setMessage((release.title.ifBlank { release.tag } + "\n\n" + release.body).trim())
            .setNegativeButton("稍后", null)
            .setPositiveButton("下载并安装") { _, _ -> downloadAndInstall(activity, release) }
            .show()
    }

    private fun downloadAndInstall(activity: Activity, release: Release) {
        Toast.makeText(activity, "正在下载 ${release.tag}…", Toast.LENGTH_SHORT).show()
        executor.execute {
            runCatching { download(activity, release) }
                .onSuccess { file -> main.post { install(activity, file) } }
                .onFailure { error -> main.post { Toast.makeText(activity, "更新下载失败：${error.message}", Toast.LENGTH_LONG).show() } }
        }
    }

    private fun checkNow(source: Source): Release {
        val root = JSONObject(getText(source.api, source, 2 * 1024 * 1024))
        val tag = root.optString("tag_name").trim()
        require(compare(tag, "0.0.0") >= 0 && tag.matches(Regex("v?\\d+\\.\\d+(\\.\\d+)?"))) { "版本号无效" }
        require(!root.optBoolean("draft") && !root.optBoolean("prerelease")) { "最新版本不是正式版" }
        val assetsJson = if (source == Source.GITEE) {
            val releaseId = root.optLong("id", 0L)
            require(releaseId > 0L) { "Gitee 发行版 ID 无效" }
            JSONArray(getText(giteeAttachmentsApi(releaseId), source, MAX_RESPONSE))
        } else {
            root.optJSONArray("assets") ?: error("发行版没有附件")
        }
        val assets = ArrayList<Asset>()
        for (i in 0 until assetsJson.length()) {
            val item = assetsJson.optJSONObject(i) ?: continue
            val name = item.optString("name")
            val url = if (source == Source.GITHUB) {
                item.optString("browser_download_url")
            } else {
                val releaseId = root.optLong("id", 0L)
                val assetId = item.optLong("id", 0L)
                giteeAttachmentsApi(releaseId, assetId, download = true)
            }
            val size = item.optLong("size", 0)
            if (name.isNotBlank() && url.isNotBlank()) assets += Asset(name, url, size, item.optString("digest").takeIf { it.isNotBlank() })
        }
        // Gitee's public release API omits attachment sizes. Keep accepting a
        // missing size here; downloadTo still enforces the actual response
        // length and the SHA-256 check before installation.
        val apk = assets.filter { it.name.endsWith(".apk", true) && (it.size == 0L || it.size in 1..MAX_APK) }
            .maxByOrNull { scoreApk(it.name) } ?: error("没有可用 APK 附件")
        val page = root.optString("html_url").ifBlank { "https://${if (source == Source.GITEE) "gitee.com" else "github.com"}/${source.repository}/releases" }
        return Release(tag, root.optString("name"), root.optString("body").take(4000), page, assets, source).let {
            if (apk.digest != null) it else it
        }
    }

    private fun giteeAttachmentsApi(releaseId: Long, assetId: Long = 0L, download: Boolean = false): String {
        require(releaseId > 0L) { "Gitee 发行版 ID 无效" }
        val suffix = if (download) "/$assetId/download" else "?per_page=100"
        if (download) require(assetId > 0L) { "Gitee 附件 ID 无效" }
        return "https://gitee.com/api/v5/repos/${Source.GITEE.repository}/releases/$releaseId/attach_files$suffix"
    }

    private fun download(context: Context, release: Release): File {
        val apk = release.assets.filter { it.name.endsWith(".apk", true) }.maxByOrNull { scoreApk(it.name) } ?: error("APK 附件不存在")
        val expected = apk.digest?.removePrefix("sha256:")?.lowercase()?.takeIf { it.matches(Regex("[0-9a-f]{64}")) }
            ?: checksumFromAssets(release, apk)
            ?: error("发布附件缺少 SHA-256，已拒绝安装")
        val dir = File(context.cacheDir, "updates").apply { mkdirs() }
        val target = File(dir, "update-${release.tag}.apk")
        val part = File(dir, ".${target.name}.part")
        downloadTo(apk.url, release.source, part, apk.size)
        require(sha256(part) == expected) { "APK SHA-256 校验失败" }
        if (target.exists()) target.delete()
        require(part.renameTo(target)) { "无法保存更新文件" }
        return target
    }

    private fun checksumFromAssets(release: Release, apk: Asset): String? {
        val sums = release.assets.firstOrNull { it.name.equals("SHA256SUMS.txt", true) || it.name.equals("sha256sums.txt", true) } ?: return null
        val text = getText(sums.url, release.source, 64 * 1024, asset = true)
        val line = text.lineSequence().firstOrNull { it.trim().endsWith(" ${apk.name}") || it.trim().endsWith(" *${apk.name}") } ?: return null
        return line.trim().split(Regex("\\s+"), limit = 2).firstOrNull()?.lowercase()?.takeIf { it.matches(Regex("[0-9a-f]{64}")) }
    }

    private fun downloadTo(raw: String, source: Source, out: File, expected: Long) {
        val conn = open(raw, source, true)
        try {
            require(conn.responseCode == HttpURLConnection.HTTP_OK) { "下载返回 HTTP ${conn.responseCode}" }
            val length = conn.contentLengthLong
            require((length <= 0L || length <= MAX_APK) && (expected <= 0L || length <= 0L || length == expected)) { "APK 大小不符" }
            conn.inputStream.use { input -> FileOutputStream(out).use { output ->
                val buf = ByteArray(128 * 1024); var total = 0L
                while (true) { val n = input.read(buf); if (n < 0) break; total += n; require(total <= MAX_APK); output.write(buf, 0, n) }
                output.fd.sync()
                require(total > 0L && (length <= 0L || total == length) && (expected <= 0L || total == expected)) { "下载不完整" }
            } }
        } finally { conn.disconnect() }
    }

    private fun getText(raw: String, source: Source, limit: Long, asset: Boolean = false): String {
        val conn = open(raw, source, asset)
        try { require(conn.responseCode == HttpURLConnection.HTTP_OK) { "服务器返回 HTTP ${conn.responseCode}" }; val bytes = conn.inputStream.use { it.readBytesLimited(limit) }; return bytes.toString(Charsets.UTF_8) }
        finally { conn.disconnect() }
    }

    private fun open(raw: String, source: Source, asset: Boolean): HttpURLConnection {
        var current = raw
        repeat(5) {
            val u = URL(current)
            require(u.protocol == "https" && u.userInfo == null && u.port == -1) { "更新地址不安全" }
            require(allowed(u.host, source, asset)) { "更新跳转地址不在所选源内" }
            val c = (u.openConnection() as HttpURLConnection).apply { connectTimeout = 15_000; readTimeout = 30_000; instanceFollowRedirects = false; setRequestProperty("User-Agent", "Naruto-Substitute-Timer-Android"); if (!asset) setRequestProperty("Accept", "application/json") }
            if (c.responseCode in 300..399) { val next = c.getHeaderField("Location") ?: error("更新跳转地址缺失"); current = URL(u, next).toString(); c.disconnect(); return@repeat }
            return c
        }
        error("更新跳转次数过多")
    }

    private fun allowed(host: String, source: Source, asset: Boolean): Boolean = when (source) {
        Source.GITHUB -> if (asset) host == "github.com" || host == "release-assets.githubusercontent.com" || host == "objects.githubusercontent.com" else host == "api.github.com"
        Source.GITEE -> host == "gitee.com" || host == "foruda.gitee.com"
    }

    private fun install(context: Activity, file: File) {
        if (Build.VERSION.SDK_INT >= 26 && !context.packageManager.canRequestPackageInstalls()) {
            context.startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}")))
            Toast.makeText(context, "请允许本应用安装未知来源，然后重新点击检查更新", Toast.LENGTH_LONG).show()
            return
        }
        val uri = Uri.parse("content://${context.packageName}.update-files/apk/${Uri.encode(file.name)}")
        val intent = Intent(Intent.ACTION_VIEW).setDataAndType(uri, "application/vnd.android.package-archive").addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        context.startActivity(intent)
    }

    private fun scoreApk(name: String): Int { val n = name.lowercase(); return (if (n.contains("universal")) 100 else 0) + (if (n.contains("x86_64")) 80 else 0) + (if (n.contains("arm64")) 20 else 0) }
    private fun compare(a: String, b: String): Int {
        fun parts(s: String): IntArray {
            val p = s.trim().removePrefix("v").split('.')
            return intArrayOf(p.getOrNull(0)?.toIntOrNull() ?: 0, p.getOrNull(1)?.toIntOrNull() ?: 0, p.getOrNull(2)?.toIntOrNull() ?: 0)
        }
        val x = parts(a)
        val y = parts(b)
        for (i in 0..2) if (x[i] != y[i]) return x[i].compareTo(y[i])
        return 0
    }

    private fun sha256(file: File): String = MessageDigest.getInstance("SHA-256").digest(file.readBytes()).joinToString("") { "%02x".format(it) }

    private fun java.io.InputStream.readBytesLimited(limit: Long): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        val buffer = ByteArray(64 * 1024)
        var total = 0L
        while (true) {
            val read = read(buffer)
            if (read < 0) break
            total += read
            require(total <= limit) { "更新响应过大" }
            out.write(buffer, 0, read)
        }
        return out.toByteArray()
    }
}
