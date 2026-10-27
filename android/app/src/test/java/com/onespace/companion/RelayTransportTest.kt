package com.onespace.companion

import java.io.File
import java.io.IOException
import java.net.InetAddress
import java.security.KeyStore
import java.security.MessageDigest
import java.security.cert.X509Certificate
import java.util.concurrent.atomic.AtomicReference
import javax.net.ssl.KeyManagerFactory
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLServerSocket
import javax.net.ssl.SSLSocket
import org.junit.Assert.*
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.Rule

/** Real nested TLS and OkHttp on the JVM, without Android lifecycle mocks. */
class RelayTransportTest {
    @get:Rule val temporary = TemporaryFolder()
    private data class Identity(val context: SSLContext, val pin: String)
    private fun identity(name: String): Identity {
        val file = File(temporary.root, "$name.p12")
        val keytool = File(System.getProperty("java.home"), "bin/keytool" + if (System.getProperty("os.name", "").startsWith("Windows")) ".exe" else "")
        val process = ProcessBuilder(keytool.absolutePath, "-genkeypair", "-alias", "fixture", "-keyalg", "RSA", "-keysize", "2048", "-validity", "2", "-dname", "CN=localhost", "-ext", "SAN=dns:localhost,ip:127.0.0.1", "-storetype", "PKCS12", "-keystore", file.absolutePath, "-storepass", "fixture-password", "-keypass", "fixture-password", "-noprompt").redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().readText()
        assertEquals(output, 0, process.waitFor())
        val store = KeyStore.getInstance("PKCS12").apply { file.inputStream().use { load(it, "fixture-password".toCharArray()) } }
        val km = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm()).apply { init(store, "fixture-password".toCharArray()) }
        val context = SSLContext.getInstance("TLS").apply { init(km.keyManagers, null, null) }
        val cert = store.getCertificate("fixture") as X509Certificate
        val pin = MessageDigest.getInstance("SHA-256").digest(cert.encoded).joinToString("") { "%02x".format(it) }
        return Identity(context, pin)
    }
    private fun headers(socket: SSLSocket): String {
        val b = StringBuilder()
        while (b.length < 16384) {
            val n = socket.inputStream.read()
            if (n < 0) throw IOException("connection closed")
            b.append(n.toChar())
            if (b.endsWith("\r\n\r\n")) return b.toString()
        }
        throw IOException("oversized headers")
    }
    private fun fixture(wrongHub: Boolean = false, wrongPC: Boolean = false, unavailableLocal: Boolean = false) {
        val hub = identity("hub")
        val pc = identity("pc")
        val server = hub.context.serverSocketFactory.createServerSocket(0, 5, InetAddress.getLoopbackAddress()) as SSLServerSocket
        server.soTimeout = 8000
        val error = AtomicReference<Throwable?>()
        val thread = Thread {
            try {
                (server.accept() as SSLSocket).use { outer ->
                    outer.soTimeout = 8000
                    val first = headers(outer)
                    assertTrue(first.startsWith("POST /hub/v1/connect/open?to=windows HTTP/1.1"))
                    assertTrue(first.contains("Authorization: Bearer " + "a".repeat(64)))
                    outer.outputStream.write("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: onespace-connect-v1\r\n\r\n".toByteArray())
                    outer.outputStream.flush()
                    val inner = pc.context.socketFactory.createSocket(outer, "localhost", server.localPort, false) as SSLSocket
                    inner.useClientMode = false
                    inner.soTimeout = 8000
                    inner.startHandshake()
                    val request = headers(inner)
                    assertTrue(request.startsWith("GET /v1/update/apk HTTP/1.1"))
                    assertTrue(request.contains("Authorization: Bearer paired-PC-token"))
                    val payload = "authenticated-update-fixture".toByteArray()
                    inner.outputStream.write(("HTTP/1.1 200 OK\r\nContent-Length: ${payload.size}\r\nConnection: close\r\n\r\n").toByteArray() + payload)
                    inner.outputStream.flush()
                }
            } catch (e: Throwable) { error.set(e) }
        }.apply { isDaemon = true; start() }
        try {
            val config = HubTunnelConfig("localhost:${server.localPort}", if(wrongHub) "0".repeat(64) else hub.pin, "a".repeat(64), "windows", if(unavailableLocal) "localhost:1" else "")
            val client = OneSpaceApi("does-not-resolve.invalid", if(wrongPC) "0".repeat(64) else pc.pin, "paired-PC-token", config)
            if (wrongHub || wrongPC) {
                try { client.downloadUpdate { it.readBytes() }; fail("wrong identity accepted") } catch (_: IOException) { }
            } else {
                var received = ""
                client.downloadUpdate { received = it.bufferedReader().readText() }
                assertEquals("authenticated-update-fixture", received)
            }
        } finally { thread.join(9000); server.close() }
        assertFalse("relay fixture did not terminate", thread.isAlive)
        if (!wrongHub && !wrongPC) error.get()?.let { throw AssertionError("relay server failed", it) }
    }
    @Test fun unavailableLocalHubFallsBackToPublicAddress() = fixture(unavailableLocal = true)
    @Test fun innerPCIdentityAndBearerSurviveHubRelay() = fixture()
    @Test fun wrongHubIdentityIsRejected() = fixture(wrongHub = true)
    @Test fun wrongPCIdentityIsRejectedEvenThroughTrustedHub() = fixture(wrongPC = true)
}
