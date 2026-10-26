package com.onespace.companion

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.database.MatrixCursor
import android.provider.OpenableColumns
import android.net.Uri
import android.os.ParcelFileDescriptor
import java.io.FileNotFoundException

/** Exposes only the verified staged APK to the Android installation UI. */
class OneSpaceUpdateProvider: ContentProvider() {
    override fun onCreate() = true
    override fun getType(uri: Uri): String? = if (uri.path == "/apk") "application/vnd.android.package-archive" else null
    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? {
        if (uri.path != "/apk") return null
        val ctx = context ?: return null
        val file = java.io.File(java.io.File(ctx.cacheDir, "updates"), "OneSpace-update.apk")
        if (!file.isFile) return null
        val available = listOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE)
        val columns = projection?.filter { it in available } ?: available
        val cursor = MatrixCursor(columns.toTypedArray())
        cursor.addRow(columns.map { if (it == OpenableColumns.SIZE) file.length() else "OneSpace-Android.apk" })
        return cursor
    }
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0
    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (uri.path != "/apk" || mode != "r") throw FileNotFoundException("Read-only APK endpoint")
        val ctx = context ?: throw FileNotFoundException("No application context")
        val f = java.io.File(java.io.File(ctx.cacheDir, "updates"), "OneSpace-update.apk")
        if (!f.isFile) throw FileNotFoundException("Update not downloaded")
        return ParcelFileDescriptor.open(f, ParcelFileDescriptor.MODE_READ_ONLY)
    }
}
