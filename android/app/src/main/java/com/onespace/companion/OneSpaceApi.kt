package com.onespace.companion

import android.content.ContentResolver
import android.net.Uri
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.net.URLEncoder
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager
import org.json.JSONArray
import org.json.JSONObject
import okio.BufferedSink

/** The same PC certificate and bearer token are required on LAN and inside a Pi relay. */
class OneSpaceApi(host: String, fingerprint: String, var token: String = "",
                  private val tunnel: HubTunnelConfig? = null,
                  private val onNetworkFailure: (() -> Unit)? = null,
                  private val recoverHost: (() -> String?)? = null) : UpdateSource {
    @Volatile private var currentHost = host
    @Volatile private var cancelled = false
    private val active = ConcurrentHashMap.newKeySet<Call>()
    private val pin = HubPolicy.normalizedPin(fingerprint)
    init { require(host.matches(Regex("[a-zA-Z0-9.:-]+"))) { "Invalid host address" } }
    companion object {
        private val clients = LinkedHashMap<String, OkHttpClient>()
        @Synchronized private fun client(pin: String, tunnel: HubTunnelConfig?): OkHttpClient {
            val key = pin + ":" + (tunnel?.let { MessageDigest.getInstance("SHA-256").digest(it.toString().toByteArray()).joinToString("") { b -> "%02x".format(b) } } ?: "direct")
            clients[key]?.let { return it }
            val trust = object : X509TrustManager {
                override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
                override fun checkClientTrusted(c: Array<X509Certificate>, a: String) = throw java.security.cert.CertificateException("Client trust unsupported")
                override fun checkServerTrusted(c: Array<X509Certificate>, a: String) {
                    if (c.isEmpty()) throw java.security.cert.CertificateException("No PC certificate")
                    c[0].checkValidity()
                    val expected = pin.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
                    if (!MessageDigest.isEqual(expected, MessageDigest.getInstance("SHA-256").digest(c[0].encoded)))
                        throw java.security.cert.CertificateException("PC identity mismatch. Do NOT continue.")
                }
            }
            val context = SSLContext.getInstance("TLS").apply { init(null, arrayOf<TrustManager>(trust), SecureRandom()) }
            val builder = OkHttpClient.Builder().sslSocketFactory(context.socketFactory, trust)
                .hostnameVerifier { _, _ -> true }.proxy(java.net.Proxy.NO_PROXY)
                .connectTimeout(4, TimeUnit.SECONDS).readTimeout(65, TimeUnit.SECONDS).writeTimeout(65, TimeUnit.SECONDS)
                .retryOnConnectionFailure(false).followRedirects(false).followSslRedirects(false)
            tunnel?.let {
                builder.socketFactory(HubTunnelSocketFactory(it))
                // The logical PC host is an identity label, not the relay's TCP destination.
                builder.dns(object : Dns { override fun lookup(hostname: String) = listOf(java.net.InetAddress.getLoopbackAddress()) })
                builder.protocols(listOf(Protocol.HTTP_1_1))
            }
            val result = builder.build()
            if (clients.size >= 8) { clients.values.forEach { it.connectionPool.evictAll() }; clients.clear() }
            clients[key] = result
            return result
        }
    }
    fun cancel() { cancelled = true; active.forEach { it.cancel() } }
    private class HttpFailure(message: String) : IOException(message)
    private fun <T> withRecovery(method: String, action: () -> T): T {
        try { return action() } catch (e: IOException) {
            // Never replay writes after an uncertain outcome, or change endpoint trust.
            if (method != "GET" || tunnel != null || (e is HttpFailure || e is javax.net.ssl.SSLException || cancelled)) throw e
            val next = recoverHost?.invoke()
            if (next.isNullOrBlank() || next == currentHost) throw e
            currentHost = next
            return action()
        }
    }
    private fun <T> exchange(path: String, method: String = "GET", body: RequestBody? = null, timeoutMs: Int? = null, receive: (Response) -> T): T {
        require(path.startsWith("/v1/") && !path.contains('\r') && !path.contains('\n'))
        if (cancelled) throw IOException("Connection cancelled")
        val base = client(pin, tunnel)
        val http = if (timeoutMs == null) base else base.newBuilder().connectTimeout(timeoutMs.toLong(), TimeUnit.MILLISECONDS).readTimeout(timeoutMs.toLong(), TimeUnit.MILLISECONDS).callTimeout(timeoutMs.toLong() * 2, TimeUnit.MILLISECONDS).build()
        val request = Request.Builder().url("https://$currentHost:47601$path").header("Accept", "application/json")
            .method(method, body ?: if(method in listOf("POST", "PUT", "PATCH")) ByteArray(0).toRequestBody() else null)
        if (token.isNotEmpty()) request.header("Authorization", "Bearer $token")
        val call = http.newCall(request.build())
        active.add(call)
        try {
            if (cancelled) throw IOException("Connection cancelled")
            return call.execute().use { response ->
                if (!response.isSuccessful) {
                    val message = try { JSONObject(boundedBody(response)).optString("error", "HTTP ${response.code}") } catch (_: Exception) { "HTTP ${response.code}" }
                    throw HttpFailure(message.take(240))
                }
                receive(response)
            }
        } catch (e: IOException) {
            if (e !is HttpFailure && e !is javax.net.ssl.SSLException && !cancelled) onNetworkFailure?.invoke()
            throw e
        } finally { active.remove(call) }
    }
    private fun boundedBody(response: Response, limit: Long = 1048576): String {
        val source = response.body?.source() ?: return ""
        source.request(limit + 1)
        if (source.buffer.size > limit) throw IOException("PC response too large")
        return source.readUtf8()
    }
    fun verifyRoute(timeoutMs: Int = 800) = exchange("/v1/info", timeoutMs = timeoutMs) { JSONObject(boundedBody(it, 16384)).optString("app") == "OneSpace" }
    override fun request(path: String, method: String, obj: JSONObject?): JSONObject = withRecovery(method) {
        exchange(path, method, obj?.toString()?.toRequestBody("application/json".toMediaType())) { JSONObject(boundedBody(it).ifBlank { "{}" }) }
    }
    fun list(path: String): JSONArray = withRecovery("GET") { exchange(path) { JSONArray(boundedBody(it)) } }
    fun upload(resolver: ContentResolver, uri: Uri, name: String, permanent: Boolean, toClipboard: Boolean): JSONObject {
        val query = "?name=" + URLEncoder.encode(name, "UTF-8") + "&bucket=" + (if(permanent) "Permanent" else "Temporary") + "&clipboard=" + (if(toClipboard) "1" else "0")
        val body = object : RequestBody() {
            override fun contentType() = "application/octet-stream".toMediaType()
            override fun isOneShot() = true
            override fun writeTo(sink: BufferedSink) {
                resolver.openInputStream(uri)?.use { input ->
                    val bytes = ByteArray(65536)
                    var total = 0L
                    while (true) {
                        val n = input.read(bytes)
                        if (n < 0) break
                        total += n
                        if (total > 512L * 1024 * 1024) throw IOException("File exceeds PC transfer limit")
                        sink.write(bytes, 0, n)
                    }
                } ?: throw IOException("Cannot read selected file")
            }
        }
        return exchange("/v1/items/file$query", "POST", body) { JSONObject(boundedBody(it)) }
    }
    override fun downloadUpdate(receiver: (java.io.InputStream) -> Unit) = exchange("/v1/update/apk") { it.body?.byteStream()?.let(receiver) ?: throw IOException("Missing update") }
    fun download(id: String, receiver: (java.io.InputStream) -> Unit) {
        require(id.matches(Regex("[a-f0-9]{24}")))
        exchange("/v1/items/$id/content") { it.body?.byteStream()?.let(receiver) ?: throw IOException("Missing file") }
    }
}
