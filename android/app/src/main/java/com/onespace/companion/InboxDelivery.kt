package com.onespace.companion

import android.content.Context
import android.net.Uri
import android.os.SystemClock
import org.json.JSONObject
import java.io.IOException
import java.time.Instant

/** One receive/ack implementation shared by manual and background reception.
 * Independent text and file locks keep a photo download from blocking text.
 * Keep confirmed receipts across process restart and uncertain acknowledgements.
 */
object InboxDelivery {
    private val textLock = Any()
    private val fileLock = Any()

    fun fetch(context: Context, client: OneSpaceApi, wait: Int = 0, lane: String = "",
              active: () -> Boolean = { true }): Int {
        val jobs = client.list("/v1/outbox?wait=$wait&lane=$lane")
        var delivered = 0
        var matched = 0
        // Text first for manual requests; background workers use separate server lanes.
        val ordered = (0 until jobs.length()).map { jobs.getJSONObject(it) }
            .sortedBy { if (it.getJSONObject("item").optString("kind") == "text") 0 else 1 }
        for (job in ordered) {
            if (!active()) break
            val item = job.getJSONObject("item")
            if (!InboxPolicy.matchesLane(item.optString("kind"), lane)) continue
            val settings = FeatureSettings(context)
            if (lane.isNotEmpty() && !settings.get("inbox") && (!settings.get("clip_receive") ||
                    !InboxPolicy.clipboardSupported(item.optString("kind"), item.optString("name")))) continue
            matched++
            val lock = if (item.optString("kind") == "text") textLock else fileLock
            synchronized(lock) {
                if (!active()) return@synchronized
                val id = job.getString("id")
                require(id.matches(Regex("[a-f0-9]{24}"))) { "Invalid inbox job ID" }
                val prefs = context.getSharedPreferences("onespace_delivery", Context.MODE_PRIVATE)
                val legacy = context.getSharedPreferences("onespace_background", Context.MODE_PRIVATE)
                    .getString("delivered_jobs", "")?.split(',')?.contains(id) == true
                var uri = prefs.getString("job_$id", null)?.let(Uri::parse)
                if (uri == null && !legacy) {
                    val start = SystemClock.elapsedRealtime()
                    client.download(item.getString("id")) { input ->
                        uri = ReceiveStore(context).receive(item.getString("name"),
                            item.optString("bucket", "Temporary"), input)
                    }
                    val saved = uri ?: throw IOException("No received file")
                    if (!prefs.edit().putString("job_$id", saved.toString()).commit()) {
                        // This newly created file has no durable receipt; never ACK it.
                        context.contentResolver.delete(saved, null, null)
                        throw IOException("Cannot persist delivery receipt")
                    }
                    android.util.Log.i("OneSpace", "Inbox $id saved in ${SystemClock.elapsedRealtime() - start}ms")
                    delivered++
                }
                if (uri != null && settings.get("clip_receive") && !prefs.getBoolean("notified_$id", false)) {
                    val created = try { Instant.parse(item.getString("created")).toEpochMilli() }
                        catch (_: Exception) { System.currentTimeMillis() }
                    ClipboardInbox.received(context, uri!!, item.optString("kind"), item.optString("name"), created)
                    prefs.edit().putBoolean("notified_$id", true).apply()
                }
                client.request("/v1/outbox/$id/ack", "POST", JSONObject())
                prefs.edit().putLong("acked_$id", System.currentTimeMillis()).apply()
            }
        }
        // Older PCs ignore the additive lane parameter. Avoid hammering an old
        // server if it returns work intended for the other worker.
        if (wait > 0 && jobs.length() > 0 && matched == 0 && active()) Thread.sleep(1500L)
        return delivered
    }

    fun cleanup(context: Context) {
        val prefs = context.getSharedPreferences("onespace_delivery", Context.MODE_PRIVATE)
        val cutoff = System.currentTimeMillis() - 24L * 60 * 60 * 1000
        for ((key, value) in prefs.all) {
            if (!key.startsWith("acked_") || value !is Long || value >= cutoff) continue
            val id = key.removePrefix("acked_")
            val lock = if (prefs.getString("job_$id", "")?.endsWith(".txt") == true) textLock else fileLock
            // 24 hours exceeds an in-flight poll's lifetime; unacknowledged
            // receipts are retained so a reconnect never redownloads them.
            synchronized(lock) { prefs.edit().remove(key).remove("job_$id").remove("notified_$id").apply() }
        }
    }
}
