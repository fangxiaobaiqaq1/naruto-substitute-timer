package com.narutotimer.update

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import java.io.File

/** Minimal private FileProvider for the verified APK in cacheDir/updates. */
class UpdateFileProvider : ContentProvider() {
    override fun onCreate(): Boolean = true
    override fun getType(uri: Uri): String = "application/vnd.android.package-archive"
    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor {
        val file = fileFor(uri)
        return MatrixCursor(arrayOf("_display_name", "_size")).apply { addRow(arrayOf(file.name, file.length())) }
    }
    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor = ParcelFileDescriptor.open(fileFor(uri), ParcelFileDescriptor.MODE_READ_ONLY)
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0

    private fun fileFor(uri: Uri): File {
        val ctx = context ?: error("provider context unavailable")
        require(uri.pathSegments.firstOrNull() == "apk") { "invalid update URI" }
        val name = uri.pathSegments.getOrNull(1) ?: error("missing APK name")
        require(name.matches(Regex("[A-Za-z0-9._-]+\\.apk"))) { "invalid APK name" }
        val root = File(ctx.cacheDir, "updates").canonicalFile
        val file = File(root, name).canonicalFile
        require(file.parentFile == root && file.isFile) { "APK is not in update cache" }
        return file
    }
}
