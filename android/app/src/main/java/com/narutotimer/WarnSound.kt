package com.narutotimer

import android.content.Context
import android.media.AudioAttributes
import android.media.MediaPlayer
import android.net.Uri
import android.util.Log
import java.io.File

/**
 * Optional last-3-seconds reminder (Go internal/sound). Plays the imported copy in
 * filesDir/sounds, or the built-in chime when none is set or the copy is unusable.
 * A newer call replaces a sound that is still playing. Main thread only.
 */
object WarnSound {
    private const val TAG = "WarnSound"
    const val DIR = "sounds"
    const val MAX_BYTES = 5L shl 20
    /** Formats MediaPlayer decodes on every supported Android version. */
    val MIME_TYPES = arrayOf("audio/wav", "audio/x-wav", "audio/mpeg", "audio/mp3", "audio/ogg", "audio/mp4", "audio/aac")

    private var player: MediaPlayer? = null

    fun dir(c: Context): File = File(c.filesDir, DIR)

    fun label(settings: Settings): String =
        if (settings.warnSoundFile.isEmpty()) "内置提示音（叮咚）" else "自定义：" + settings.warnSoundFile

    fun play(c: Context, settings: Settings) {
        val name = settings.warnSoundFile
        val file = if (name.isEmpty()) null else File(dir(c), name).takeIf { it.isFile }
        if (!start(c, file) && file != null) start(c, null) // Broken import: fall back to the chime.
    }

    private fun start(c: Context, file: File?): Boolean {
        stop()
        return try {
            val p = MediaPlayer()
            p.setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_ASSISTANCE_SONIFICATION)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                    .build(),
            )
            if (file != null) {
                p.setDataSource(file.path)
            } else {
                c.resources.openRawResourceFd(R.raw.warn_default).use { fd ->
                    p.setDataSource(fd.fileDescriptor, fd.startOffset, fd.length)
                }
            }
            p.setOnCompletionListener { done -> done.release(); if (player === done) player = null }
            p.prepare()
            p.start()
            player = p
            true
        } catch (e: Exception) {
            Log.w(TAG, "play failed: ${file?.name ?: "default"}", e)
            stop()
            false
        }
    }

    private fun stop() {
        player?.let { runCatching { it.release() } }
        player = null
    }

    /** Extension for a picked MIME type; null = unsupported. */
    fun extension(mime: String?): String? = when (mime) {
        "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave" -> "wav"
        "audio/mpeg", "audio/mp3" -> "mp3"
        "audio/ogg", "application/ogg" -> "ogg"
        "audio/mp4", "audio/aac", "audio/x-m4a", "audio/m4a" -> "m4a"
        else -> null
    }

    /**
     * Copies [uri] into filesDir/sounds/warn-sound.<ext>, replacing any earlier import.
     * Returns the stored file name, or throws with a user-facing message. Worker thread.
     */
    fun import(c: Context, uri: Uri): String {
        val ext = extension(c.contentResolver.getType(uri)) ?: throw IllegalArgumentException("只支持 WAV / MP3 / OGG / M4A")
        val d = dir(c).apply { mkdirs() }
        val tmp = File(d, "import.tmp")
        val fits = c.contentResolver.openInputStream(uri)?.use { Housekeeping.copyLimited(it, tmp, MAX_BYTES) }
            ?: throw IllegalArgumentException("无法读取文件")
        if (!fits) {
            tmp.delete()
            throw IllegalArgumentException("音效文件超过 5 MB")
        }
        if (tmp.length() == 0L) {
            tmp.delete()
            throw IllegalArgumentException("音效文件是空的")
        }
        // Must be decodable before it replaces the current sound.
        try {
            MediaPlayer().apply { setDataSource(tmp.path); prepare(); release() }
        } catch (e: Exception) {
            tmp.delete()
            throw IllegalArgumentException("无法播放这个音效文件")
        }
        d.listFiles()?.filter { it.name.startsWith("warn-sound.") }?.forEach { it.delete() }
        val name = "warn-sound.$ext"
        if (!tmp.renameTo(File(d, name))) {
            tmp.delete()
            throw IllegalArgumentException("保存音效失败")
        }
        return name
    }
}
