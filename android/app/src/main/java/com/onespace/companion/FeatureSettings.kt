package com.onespace.companion

import android.content.Context

/** Preferences are local to this phone; server-enforced permissions belong on Windows. */
class FeatureSettings(private val context: Context) {
    private val p = context.getSharedPreferences("onespace_features", Context.MODE_PRIVATE)
    fun get(key: String, fallback: Boolean = false) = p.getBoolean(key, fallback)
    fun set(key: String, enabled: Boolean) {
        if(key == "camera" && enabled && !get(key)) {
            // Do not send photos taken while Camera Sync was off.
            context.getSharedPreferences("onespace_background", Context.MODE_PRIVATE)
                .edit().remove("last_photo_id").remove("camera_baseline")
                .remove("camera_high_water").remove("camera_sent_ids").apply()
        }
        p.edit().putBoolean(key, enabled).apply()
    }
    var theme: String
        get() = p.getString("theme", "Midnight") ?: "Midnight"
        set(value) { p.edit().putString("theme", value).apply() }
    fun reset() { p.edit().clear().apply() }
}
