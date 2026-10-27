package com.onespace.companion

import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.net.URL
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

/** All hub HTTP is authenticated and exact-certificate pinned, including native WebRTC signaling. */
class HubApi(address: String, fingerprint: String, private val token: String, private val localAddress: String = "") {
    val baseUrl = HubPolicy.baseUrl(address)
    private val pin = HubPolicy.normalizedPin(fingerprint)
    private val active = ConcurrentHashMap.newKeySet<HttpsURLConnection>()
    @Volatile private var cancelled = false
    data class Response(val status: Int, val body: String, val location: String?)
    companion object {
        internal fun pinnedSocketFactory(fingerprint: String) = context(HubPolicy.normalizedPin(fingerprint)).socketFactory
        @Volatile private var route: Triple<String, String, Long>? = null
        internal fun invalidateRoute() { route = null }
        @Volatile private var cached: Pair<String, SSLContext>? = null
        @Synchronized private fun context(pin: String): SSLContext {
            cached?.takeIf { it.first == pin }?.let { return it.second }
            val trust = object : X509TrustManager {
                override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
                override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = throw java.security.cert.CertificateException("Client trust unsupported")
                override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
                    if (chain.isEmpty()) throw java.security.cert.CertificateException("Missing hub certificate")
                    chain[0].checkValidity()
                    val actual = MessageDigest.getInstance("SHA-256").digest(chain[0].encoded)
                    val expected = pin.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
                    if (!MessageDigest.isEqual(actual, expected)) throw java.security.cert.CertificateException("Hub certificate changed")
                }
            }
            return SSLContext.getInstance("TLS").apply {
                init(null, arrayOf<TrustManager>(trust), SecureRandom())
                cached = pin to this
            }
        }
    }
    internal fun connectionAddress(): String {
        if (localAddress.isBlank()) return baseUrl
        val local = HubPolicy.baseUrl(localAddress)
        val key = "$baseUrl:$local:$pin"
        val now = System.nanoTime() / 1000000
        route?.let { if (it.first == key && it.third > now) return it.second }
        var selected = baseUrl
        val probe = URL(local + "/hub/v1/info").openConnection() as HttpsURLConnection
        try {
            probe.sslSocketFactory = context(pin).socketFactory
            probe.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, _ -> true }
            probe.instanceFollowRedirects = false
            probe.connectTimeout = 800
            probe.readTimeout = 800
            if (probe.responseCode == 200) selected = local
        } catch (_: IOException) { } finally { probe.disconnect() }
        route = Triple(key, selected, now + 30000)
        return selected
    }
    fun downloadUpdate(receiver: (java.io.InputStream) -> Unit) {
        if (cancelled) throw IOException("Hub request cancelled")
        val connection = URL(connectionAddress() + "/hub/v1/updates/apk").openConnection() as HttpsURLConnection
        active.add(connection)
        try {
            connection.sslSocketFactory = context(pin).socketFactory
            connection.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, _ -> true }
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 4000
            connection.readTimeout = 65000
            connection.setRequestProperty("Authorization", "Bearer $token")
            if (cancelled) throw IOException("Hub request cancelled")
            if (connection.responseCode != 200) throw IOException("Pi update download failed")
            connection.inputStream.use(receiver)
        } finally { active.remove(connection); connection.disconnect() }
    }
    fun freshForCleanup() = HubApi(baseUrl.removePrefix("https://"), pin, token, localAddress)
    fun cancel() { cancelled = true; active.forEach { it.disconnect() } }
    fun raw(path: String, method: String = "GET", body: String? = null, contentType: String = "application/json"): Response {
        val bytes = body?.toByteArray(Charsets.UTF_8)
        require(bytes == null || bytes.size <= 131072) { "Hub request too large" }
        return exchange(path, method, bytes, contentType, null)
    }
    fun cloudChunk(path: String, bytes: ByteArray, offset: Long): Response {
        require(path.matches(Regex("/hub/v1/cloud/uploads/[a-f0-9]{32}")) && bytes.isNotEmpty() && bytes.size <= 1048576 && offset >= 0)
        return exchange(path, "PUT", bytes, "application/octet-stream", offset)
    }
    private fun exchange(path: String, method: String, payload: ByteArray?, contentType: String, offset: Long?): Response {
        require(path.startsWith("/hub/v1/") && !path.contains("..") && !path.contains('\\'))
        if (cancelled) throw IOException("Hub request cancelled")
        var complete = false
        val connection = URL(connectionAddress() + path).openConnection() as HttpsURLConnection
        active.add(connection)
        try {
            connection.sslSocketFactory = context(pin).socketFactory
            connection.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, _ -> true } // Exact cert pin is the endpoint identity.
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 4000
            connection.readTimeout = if (path.contains("wait=") || path.startsWith("/hub/v1/cloud/")) 60000 else 10000
            connection.requestMethod = method
            connection.useCaches = false
            connection.setRequestProperty("Authorization", "Bearer $token")
            connection.setRequestProperty("Accept", if (contentType == "application/sdp") "application/sdp" else "application/json")
            if (cancelled) throw IOException("Hub request cancelled")
            offset?.let { connection.setRequestProperty("X-Upload-Offset", it.toString()) }
            payload?.let { bytes ->
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", contentType)
                connection.setFixedLengthStreamingMode(bytes.size)
                connection.outputStream.use { out -> out.write(bytes) }
            }
            val code = connection.responseCode
            val stream = if (code in 200..299) connection.inputStream else connection.errorStream
            val bytes = stream?.use { input ->
                val output = java.io.ByteArrayOutputStream()
                val buffer = ByteArray(8192)
                while (output.size() <= 1024 * 1024) {
                    val n = input.read(buffer, 0, minOf(buffer.size, 1024 * 1024 + 1 - output.size()))
                    if (n < 0) break
                    output.write(buffer, 0, n)
                }
                output.toByteArray()
            } ?: byteArrayOf()
            if (bytes.size > 1024 * 1024) throw IOException("Hub response too large")
            if (code !in 200..299) {
                val explanation = if (path.startsWith("/hub/v1/cloud/")) try { JSONObject(bytes.toString(Charsets.UTF_8)).optString("error").take(240) } catch (_: Exception) { "" } else ""
                throw IOException(explanation.ifBlank { "Hub request failed (HTTP $code)" })
            }
            complete = true
            return Response(code, bytes.toString(Charsets.UTF_8), connection.getHeaderField("Location"))
        } catch (error: IOException) { invalidateRoute(); throw error } finally { active.remove(connection); if (!complete || cancelled) connection.disconnect() }
    }
    fun request(path: String, method: String = "GET", body: JSONObject? = null) = JSONObject(raw(path, method, body?.toString()).body.ifBlank { "{}" })
    fun list(path: String) = JSONArray(raw(path).body)
    fun sendText(target: String, text: String, eventId: String = UUID.randomUUID().toString()) {
        require(HubPolicy.validTarget(target) && HubPolicy.validText(text)) { "Select a valid target and text up to 16 KiB" }
        request("/hub/v1/events", "POST", JSONObject().put("id", eventId).put("to", target).put("kind", "clipboard.text").put("text", text))
    }
    fun media(target: String, action: String) {
        require(HubPolicy.validTarget(target) && action in listOf("play_pause", "next", "previous", "stop"))
        request("/hub/v1/events", "POST", JSONObject().put("id", UUID.randomUUID().toString()).put("to", target).put("kind", "media").put("action", action))
    }
}
