package com.onespace.companion

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build

/** Restarts an opt-in foreground sync service after reboot when Android allows it.
 * Does not circumvent battery settings or start when the user disabled background sync. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) {
        if (intent?.action != Intent.ACTION_BOOT_COMPLETED) return
        if (!FeatureSettings(context).get("background") || (!SecureStore(context).paired() && !HubProfile(context).configured())) return
        try {
            val start = Intent(context, BackgroundSyncService::class.java)
            if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(start)
            else context.startService(start)
        } catch (e: Exception) {
            android.util.Log.i("OneSpace", "Background service could not restart after boot", e)
        }
    }
}
