package com.onespace.companion

import android.net.Uri
import android.content.ContentResolver
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.net.URL
import java.net.URLEncoder
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.X509Certificate
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

/** Certificate fingerprint must first be copied from the physical PC's dashboard. Never trust TOFU. */
class OneSpaceApi(host: String, fingerprint: String, var token: String = "",
                  private val recoverHost: (() -> String?)? = null) {
    @Volatile private var currentHost: String = host
    private val activeConnections = java.util.concurrent.ConcurrentHashMap.newKeySet<HttpsURLConnection>()
    @Volatile private var cancelled = false
    fun cancel() { cancelled = true; activeConnections.forEach { it.disconnect() } }
    private val pin = fingerprint.lowercase().replace(Regex("[^0-9a-f]"), "")
    init { require(pin.length == 64) { "Expected 64-character SHA-256 fingerprint" }; require(host.matches(Regex("[a-zA-Z0-9.:-]+"))) { "Invalid host address" } }
    companion object {
        // Reusing the same pinned TLS context lets the OS resume trusted sessions.
        // Previously each clipboard/photo request built a fresh TLS context, forcing
        // extra certificate handshakes and wasting battery.
        private val tlsContexts = java.util.concurrent.ConcurrentHashMap<String, SSLContext>()
        private fun pinnedContext(pin: String): SSLContext = tlsContexts.computeIfAbsent(pin) { fingerprint ->
        val trust = object : X509TrustManager {
            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
            override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = Unit
            override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
                if (chain.isEmpty()) throw java.security.cert.CertificateException("No certificate")
                val expected = fingerprint.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
                val actual = MessageDigest.getInstance("SHA-256").digest(chain[0].encoded)
                if (!MessageDigest.isEqual(expected, actual)) throw java.security.cert.CertificateException("PC identity mismatch. Do NOT continue.")
                chain[0].checkValidity()
            }
        }
        val ctx = SSLContext.getInstance("TLS")
        ctx.init(null, arrayOf<TrustManager>(trust), SecureRandom())
        ctx
        }
    }
    private fun connect(path: String, method: String = "GET"): HttpsURLConnection {
        if (cancelled) throw IOException("Connection cancelled")
        val ctx = pinnedContext(pin)
        val url = URL("https://$currentHost:47601$path")
        val c = (url.openConnection() as HttpsURLConnection)
        c.sslSocketFactory = ctx.socketFactory
        // Pinning the exact cert, rather than a DNS name, establishes the host identity.
        c.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, _ -> true }
        c.requestMethod = method
        c.connectTimeout = 4000
        c.readTimeout = 65000
        c.useCaches = false
        c.setRequestProperty("Accept", "application/json")
        if (token.isNotEmpty()) c.setRequestProperty("Authorization", "Bearer $token")
        activeConnections.add(c)
        if (cancelled) { activeConnections.remove(c); c.disconnect(); throw IOException("Connection cancelled") }
        return c
    }
    private fun networkRetryable(error: java.io.IOException): Boolean =
        error is java.net.ConnectException || error is java.net.NoRouteToHostException ||
        error is java.net.SocketTimeoutException || error is java.net.UnknownHostException

    /** Retry failed reads once only after discovering and cert-verifying a new IP.
     * POST operations are intentionally not replayed: they might have succeeded
     * before the connection broke. */
    private fun <T> withRecovery(method: String, action: () -> T): T {
        try { return action() } catch (error: java.io.IOException) {
            if (method != "GET" || !networkRetryable(error)) throw error
            val next = recoverHost?.invoke()
            if (next.isNullOrBlank() || next == currentHost) throw error
            currentHost = next
            return action()
        }
    }
    /** Short, pinned preflight. Never reads or replays clipboard/file content. */
    fun verifyRoute(timeoutMs: Int = 800): Boolean {
        val c = connect("/v1/info")
        try {
            c.connectTimeout = timeoutMs
            c.readTimeout = timeoutMs
            if (c.responseCode != 200) return false
            val body = c.inputStream.use { input ->
                val bytes = ByteArray(16385)
                var size = 0
                while (size < bytes.size) {
                    val count = input.read(bytes, size, bytes.size - size)
                    if (count < 0) break
                    size += count
                }
                require(size <= 16384) { "Oversized PC identity response" }
                String(bytes, 0, size, Charsets.UTF_8)
            }
            return JSONObject(body).optString("app") == "OneSpace"
        } finally { activeConnections.remove(c); c.disconnect() }
    }
    fun request(path: String, method: String = "GET", obj: JSONObject? = null): JSONObject =
        withRecovery(method) { requestOnce(path,method,obj) }
    private fun requestOnce(path: String, method: String, obj: JSONObject?): JSONObject {
        val c = connect(path, method)
        var complete = false
        try {
            if (obj != null) {
                c.doOutput = true
                c.setRequestProperty("Content-Type", "application/json")
                c.outputStream.use { it.write(obj.toString().toByteArray(Charsets.UTF_8)) }
            }
            val status = c.responseCode
            val response = (if (status in 200..299) c.inputStream else c.errorStream)
                ?.bufferedReader()?.use { it.readText() } ?: "{}"
            val answer = JSONObject(response)
            if (status !in 200..299) throw IOException(answer.optString("error", "HTTP $status"))
            complete = true
            return answer
        } finally { activeConnections.remove(c); if (!complete || cancelled) c.disconnect() }
    }
    fun list(path: String): JSONArray = withRecovery("GET") { listOnce(path) }
    private fun listOnce(path: String): JSONArray {
        val c = connect(path)
        var complete = false
        try {
            val status = c.responseCode
            val body = (if(status in 200..299)c.inputStream else c.errorStream)
                ?.bufferedReader()?.use {it.readText()} ?: "[]"
            if (status !in 200..299) throw IOException(JSONObject(body).optString("error", "HTTP $status"))
            val result = JSONArray(body)
            complete = true
            return result
        } finally { activeConnections.remove(c); if (!complete || cancelled) c.disconnect() }
    }
    fun upload(resolver: ContentResolver, uri: Uri, name: String, permanent: Boolean, toClipboard: Boolean): JSONObject {
        val bucket = if (permanent) "Permanent" else "Temporary"
        val query = "?name=" + URLEncoder.encode(name, "UTF-8") + "&bucket=$bucket&clipboard=" + if (toClipboard) "1" else "0"
        val c = connect("/v1/items/file$query", "POST")
        var complete = false
        try {
            c.doOutput = true
            c.setRequestProperty("Content-Type", "application/octet-stream")
            c.setChunkedStreamingMode(65536)
            resolver.openInputStream(uri)?.use { input -> c.outputStream.use { output -> input.copyTo(output, 65536) } }
                ?: throw IOException("Cannot read selected file")
            val code = c.responseCode
            val response = (if(code in 200..299)c.inputStream else c.errorStream)
                ?.bufferedReader()?.use { it.readText() } ?: "{}"
            val answer = JSONObject(response)
            if(code !in 200..299)throw IOException(answer.optString("error","HTTP $code"))
            complete = true
            return answer
        } finally { activeConnections.remove(c); if (!complete || cancelled) c.disconnect() }
    }
    /** Streams a release over the existing certificate-pinned, token-authenticated HTTPS connection. */
    fun downloadUpdate(receiver: (java.io.InputStream) -> Unit) {
        val c = connect("/v1/update/apk")
        var complete = false
        try {
            if (c.responseCode !in 200..299) throw IOException("Update download failed (HTTP ${c.responseCode})")
            c.inputStream.use(receiver)
            complete = true
        } finally { activeConnections.remove(c); if (!complete || cancelled) c.disconnect() }
    }
    fun download(id: String, receiver: (java.io.InputStream) -> Unit) {
        require(id.matches(Regex("[a-f0-9]{24}")))
        val c = connect("/v1/items/$id/content")
        var complete = false
        try {
            if(c.responseCode !in 200..299)throw IOException("Download failed (${c.responseCode})")
            c.inputStream.use(receiver)
            complete = true
        } finally { activeConnections.remove(c); if (!complete || cancelled) c.disconnect() }
    }
}
