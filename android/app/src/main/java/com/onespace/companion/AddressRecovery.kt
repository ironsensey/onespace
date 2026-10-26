package com.onespace.companion

import android.content.Context
import android.os.SystemClock
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import org.json.JSONObject
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.security.SecureRandom

/** Occasional UDP broadcast discovery; only a matching, verified TLS identity may
 * change the paired PC address. This channel never carries secrets.
 */
object AddressRecovery {
    private var lastAttemptMs = -60000L
    @Synchronized
    fun recover(context: Context, store: SecureStore): String? {
        val now = SystemClock.elapsedRealtime()
        if (now - lastAttemptMs < 20000L) return null
        lastAttemptMs = now
        if (!store.paired()) return null
        val connectivity = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val caps = connectivity.getNetworkCapabilities(connectivity.activeNetwork ?: return null) ?: return null
        if (!caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) &&
            !caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) return null
        val pin = store.fingerprint().lowercase().replace(Regex("[^0-9a-f]"), "")
        val nonce = ByteArray(16).also { SecureRandom().nextBytes(it) }
            .joinToString("") { "%02x".format(it.toInt() and 255) }
        val query = "ONESPACE_DISCOVER_V1 $nonce".toByteArray(Charsets.US_ASCII)
        val expected = "ONESPACE_HERE_V1 $nonce $pin"
        try {
            DatagramSocket().use { socket ->
                socket.broadcast = true
                socket.soTimeout = 850
                socket.send(DatagramPacket(query, query.size,
                    InetAddress.getByName("255.255.255.255"), 47602))
                val deadline = SystemClock.elapsedRealtime() + 900L
                val buf = ByteArray(256)
                while (SystemClock.elapsedRealtime() < deadline) {
                    val packet = DatagramPacket(buf, buf.size)
                    try { socket.receive(packet) } catch (_: java.net.SocketTimeoutException) { break }
                    val response = String(packet.data, packet.offset, packet.length, Charsets.US_ASCII)
                    if (response != expected) continue
                    val candidate = packet.address.hostAddress ?: continue
                    if (candidate == store.host()) continue
                    // A matching discovery message alone is insufficient: require
                    // the trusted PC certificate over an actual HTTPS connection.
                    try {
                        val result = OneSpaceApi(candidate, pin, store.token()).request("/v1/info")
                        if (result.optString("app") == "OneSpace") {
                            store.updateVerifiedHost(candidate)
                            return candidate
                        }
                    } catch (_: Exception) { /* Ignore spoofed or stale responders. */ }
                }
            }
        } catch (e: Exception) { android.util.Log.d("OneSpace", "LAN rediscovery not available", e) }
        return null
    }
}
