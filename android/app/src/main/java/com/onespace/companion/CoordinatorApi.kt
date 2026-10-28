package com.onespace.companion

import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.net.URL
import java.security.MessageDigest
import java.security.cert.X509Certificate
import java.util.concurrent.ConcurrentHashMap
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

class CoordinatorApi(endpoint: String, fingerprint: String, private val token: String) {
    private val base = HubPolicy.coordinatorUrl(endpoint)
    private val pin = HubPolicy.normalizedPin(fingerprint)
    private val active = ConcurrentHashMap.newKeySet<HttpsURLConnection>()
    data class Response(val status: Int, val body: String)
    companion object {
        private fun context(pin: String): SSLContext {
            val trust = object : X509TrustManager {
                override fun getAcceptedIssuers() = emptyArray<X509Certificate>()
                override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = Unit
                override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
                    if (chain.isEmpty()) throw java.security.cert.CertificateException("Missing coordinator certificate")
                    val actual = MessageDigest.getInstance("SHA-256").digest(chain[0].encoded)
                    val expected = pin.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
                    if (!MessageDigest.isEqual(actual, expected)) throw java.security.cert.CertificateException("Coordinator certificate changed")
                }
            }
            return SSLContext.getInstance("TLS").apply { init(null, arrayOf<TrustManager>(trust), java.security.SecureRandom()) }
        }
    }
    fun cancel() { active.forEach { it.disconnect() } }
    fun raw(path: String, method: String = "GET", body: String? = null): Response {
        require(path.startsWith("/v1/coord/") && !path.contains("..") && !path.contains('\\'))
        val c = URL(base + path).openConnection() as HttpsURLConnection
        active.add(c)
        try {
            c.sslSocketFactory = context(pin).socketFactory
            c.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, _ -> true }
            c.requestMethod = method
            c.connectTimeout = 5000
            c.readTimeout = if (path.contains("/poll")) 60000 else 10000
            c.setRequestProperty("Authorization", "Bearer $token")
            body?.let {
                val data = it.toByteArray()
                c.doOutput = true; c.setRequestProperty("Content-Type", "application/json")
                c.setFixedLengthStreamingMode(data.size); c.outputStream.use { out -> out.write(data) }
            }
            val status = c.responseCode
            val stream = if (status in 200..299) c.inputStream else c.errorStream
            return Response(status, stream?.use { it.readBytes().toString(Charsets.UTF_8) } ?: "")
        } finally { active.remove(c); c.disconnect() }
    }
    fun create(device: String, target: String) = raw("/v1/coord/sessions", "POST", JSONObject().put("viewer", device).put("target", target).toString())
    fun send(id: String, device: String, type: String, body: String) = raw("/v1/coord/sessions/$id/messages?from=$device", "POST", JSONObject().put("type", type).put("body", body).toString())
    fun poll(id: String, device: String) = raw("/v1/coord/sessions/$id/poll?for=$device")
    fun close(id: String, device: String) = raw("/v1/coord/sessions/$id/close?by=$device", "DELETE")
    fun acknowledge(id: String) = raw("/v1/coord/requests/$id", "POST")
}
