package com.onespace.companion

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.webkit.MimeTypeMap
import org.json.JSONArray
import org.json.JSONObject
import java.io.InputStream

/** User-visible shared files under Downloads/OneSpace/{Permanent,Temporary}. */
class ReceiveStore(private val context: Context) {
    companion object { private val metadataLock = Any() }
    private val prefs = context.getSharedPreferences("received_files", Context.MODE_PRIVATE)
    private fun write(name: String, bucket: String, input: InputStream): Uri {
        require(bucket == "Permanent" || bucket == "Temporary")
        val extension = name.substringAfterLast('.', "").lowercase()
        val mime = MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension) ?: "application/octet-stream"
        val values = ContentValues().apply {
            put(MediaStore.MediaColumns.DISPLAY_NAME, name.take(120))
            put(MediaStore.MediaColumns.MIME_TYPE, mime)
            put(MediaStore.MediaColumns.RELATIVE_PATH, "${Environment.DIRECTORY_DOWNLOADS}/OneSpace/$bucket/")
            put(MediaStore.MediaColumns.IS_PENDING, 1)
        }
        val uri = context.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
            ?: throw IllegalStateException("Cannot create Downloads file")
        try {
            context.contentResolver.openOutputStream(uri)?.use { input.copyTo(it, 65536) }
                ?: throw IllegalStateException("Cannot write Downloads file")
            context.contentResolver.update(uri, ContentValues().apply { put(MediaStore.MediaColumns.IS_PENDING, 0) }, null, null)
            if (bucket == "Temporary") {
                synchronized(metadataLock) {
                val files = JSONArray(prefs.getString("temporary", "[]"))
                files.put(JSONObject().put("uri", uri.toString()).put("created", System.currentTimeMillis()))
                prefs.edit().putString("temporary", files.toString()).apply()
                }
            }
            return uri
        } catch (error: Exception) {
            context.contentResolver.delete(uri, null, null)
            throw error
        }
    }
    fun receive(name: String, bucket: String, input: InputStream): Uri = write(name, bucket, input)
    fun ensureFolders() {
        if (Build.VERSION.SDK_INT < 29) return
        for (bucket in listOf("Permanent", "Temporary")) {
            if (prefs.getBoolean("made_$bucket", false)) continue
            val text = "OneSpace $bucket folder.\nTemporary transfers expire after 24 hours when the app can perform cleanup.\n"
            write("OneSpace-INFO.txt", bucket, text.byteInputStream())
            prefs.edit().putBoolean("made_$bucket", true).apply()
        }
    }
    fun cleanup(): Int = synchronized(metadataLock) {
        val existing = JSONArray(prefs.getString("temporary", "[]"))
        val keep = JSONArray(); var deleted = 0
        val now = System.currentTimeMillis()
        for (i in 0 until existing.length()) {
            val item = existing.optJSONObject(i) ?: continue
            if (now - item.optLong("created", 0) >= 24L * 60 * 60 * 1000) {
                try {
                    context.contentResolver.delete(Uri.parse(item.getString("uri")), null, null)
                    deleted++
                } catch (_: Exception) { keep.put(item) } // Retry after permission becomes available.
            } else keep.put(item)
        }
        prefs.edit().putString("temporary", keep.toString()).apply()
        deleted
    }
    fun countTemporary(): Int = JSONArray(prefs.getString("temporary", "[]")).length()
}
