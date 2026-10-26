package com.onespace.companion

import android.content.Context
import android.net.Uri
import org.json.JSONObject
import java.io.IOException
import java.time.Instant

/** Receipts survive a failed ACK; transport retries never duplicate a received file. */
object HubSync {
    private val lock = Any()
    fun receive(context: Context, client: HubApi, active: () -> Boolean) {
        val events = client.list("/hub/v1/events?wait=50")
        val state = context.getSharedPreferences("onespace_hub_delivery", Context.MODE_PRIVATE)
        val settings = FeatureSettings(context)
        val now = System.currentTimeMillis()
        for (i in 0 until events.length()) {
            if (!active()) return
            val event = events.getJSONObject(i)
            val id = event.getString("id")
            require(id.matches(Regex("[A-Za-z0-9_-]{8,80}"))) { "Invalid hub event ID" }
            val expires = try { Instant.parse(event.getString("expiresAt")).toEpochMilli() } catch (_: Exception) { 0L }
            synchronized(lock) {
                if (!active()) return@synchronized
                val kind = event.optString("kind")
                if (expires > System.currentTimeMillis() && kind == "clipboard.text" && settings.get("hub_receive")) {
                    val text = event.optString("text")
                    if (HubPolicy.validText(text)) {
                        var saved = state.getString("uri_$id", null)?.let(Uri::parse)
                        if (saved == null) {
                            saved = ReceiveStore(context).receive("Hub clipboard.txt", "Temporary", text.byteInputStream())
                            if (!state.edit().putString("uri_$id", saved.toString()).commit()) {
                                context.contentResolver.delete(saved, null, null)
                                throw IOException("Cannot persist hub receipt")
                            }
                        }
                        // Durable receipt precedes clipboard activation and ACK. If an
                        // ACK response is lost, do not lose the only notification too.
                        if (active() && expires > System.currentTimeMillis() && !state.getBoolean("shown_$id", false)) {
                            val created = try { Instant.parse(event.getString("createdAt")).toEpochMilli() } catch (_: Exception) { now }
                            ClipboardInbox.received(context, saved, "text", "Hub clipboard.txt", created)
                            state.edit().putBoolean("shown_$id", true).apply()
                        }
                    }
                }
                // Unsupported media actions are never executed on this phone.
                client.request("/hub/v1/events/$id/ack", "POST", JSONObject())
                state.edit().putLong("ack_$id", System.currentTimeMillis()).apply()
            }
        }
        for ((key, value) in state.all) {
            if (key.startsWith("ack_") && value is Long && value < now - 3600000L) {
                val id = key.removePrefix("ack_")
                state.edit().remove(key).remove("uri_$id").remove("shown_$id").apply()
            }
        }
    }
}
