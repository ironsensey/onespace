package com.onespace.companion

import android.app.Activity
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri

/** Stores the latest received share; puts it on Android's clipboard only while Activity is visible.
 * OneSpace uses foreground focus for clipboard activation; background receipt remains in storage.
 */
object ClipboardInbox {
    private const val NAME = "onespace_clipboard_inbox"
    private const val CHANNEL = "onespace_incoming"
    const val ACTION_INBOX_READY = "com.onespace.companion.INBOX_READY"

    @Synchronized
    fun received(context: Context, uri: Uri, kind: String, name: String, created: Long = System.currentTimeMillis()) {
        if (!InboxPolicy.clipboardSupported(kind, name)) return
        val prefs = context.getSharedPreferences(NAME, Context.MODE_PRIVATE)
        if (!InboxPolicy.acceptsClipboard(prefs.getLong("latest_created", 0), created)) return
        prefs.edit().putString("uri", uri.toString()).putString("kind", kind)
            .putLong("latest_created", created).apply()
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "Received clipboard items", NotificationManager.IMPORTANCE_DEFAULT))
        // Notifications may be denied on Android 13+; the saved inbox item remains accessible.
        val intent = Intent(context, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP)
        val pi = PendingIntent.getActivity(context, 118, intent, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        try { manager.notify(47603, Notification.Builder(context, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_download_done)
            .setContentTitle("OneSpace: ready on your phone")
            .setContentText("Open OneSpace to make the latest shared item pasteable")
            .setOnlyAlertOnce(true).setAutoCancel(true).setContentIntent(pi).build())
        } catch (_: SecurityException) { /* Permission denied; pending item stays saved. */ }
        // Notify the visible Activity last; if it pastes immediately it can cancel
        // the already-posted notification instead of leaving a stale alert.
        context.sendBroadcast(Intent(ACTION_INBOX_READY).setPackage(context.packageName))
    }

    /** Only invoke with foreground focus and after the app has been unlocked. */
    @Synchronized
    fun pasteIntoClipboard(activity: Activity): Boolean {
        if (!activity.window.decorView.hasWindowFocus()) return false
        val pref = activity.getSharedPreferences(NAME, Context.MODE_PRIVATE)
        val uriText = pref.getString("uri", null) ?: return false
        val uri = Uri.parse(uriText)
        return try {
            val clipboard = activity.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
            if (pref.getString("kind", "") == "text") {
                val data = activity.contentResolver.openInputStream(uri)?.bufferedReader()?.use { it.readText() } ?: return false
                if (data.isBlank() || data.length > 250000) return false
                clipboard.setPrimaryClip(ClipData.newPlainText("OneSpace shared text", data))
            } else {
                clipboard.setPrimaryClip(ClipData.newUri(activity.contentResolver, "OneSpace shared photo", uri))
            }
            pref.edit().remove("uri").remove("kind").apply()
            (activity.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager).cancel(47603)
            true
        } catch (_: Exception) { false } // permissions/storage can change; preserve pending reference
    }
}
