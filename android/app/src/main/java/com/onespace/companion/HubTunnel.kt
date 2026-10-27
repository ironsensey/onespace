package com.onespace.companion

import java.io.IOException
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.net.SocketAddress
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.X509Certificate
import javax.net.SocketFactory
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

/** No VPN interface, external account or separate background application. */
data class HubTunnelConfig(val address: String, val fingerprint: String, val token: String, val target: String, val localAddress: String = "")

class HubTunnelSocketFactory(private val config: HubTunnelConfig) : SocketFactory() {
    override fun createSocket(): Socket = RelaySocket(config)
    override fun createSocket(host: String, port: Int): Socket = createSocket().apply { connect(InetSocketAddress(host, port)) }
    override fun createSocket(host: String, port: Int, local: InetAddress, localPort: Int): Socket = createSocket(host, port)
    override fun createSocket(host: InetAddress, port: Int): Socket = createSocket(host.hostAddress!!, port)
    override fun createSocket(host: InetAddress, port: Int, local: InetAddress, localPort: Int): Socket = createSocket(host, port)
}

private class RelaySocket(private val config: HubTunnelConfig) : Socket() {
    @Volatile private var delegate: Socket? = null
    @Volatile private var stopped = false
    private var timeout = 65000
    private var noDelay = true
    private var keepAliveEnabled = false
    override fun connect(endpoint: SocketAddress) = connect(endpoint, 4000)
    override fun connect(endpoint: SocketAddress, timeoutMs: Int) {
        require(HubPolicy.validTarget(config.target))
        require(config.token.matches(Regex("[a-fA-F0-9]{64}"))) { "Invalid hub token" }
        val uri = java.net.URI(HubApi(config.address, config.fingerprint, config.token, config.localAddress).connectionAddress())
        val pin = HubPolicy.normalizedPin(config.fingerprint)
        val factory = HubApi.pinnedSocketFactory(pin)
        val raw = Socket().apply { this.tcpNoDelay = noDelay; this.keepAlive = keepAliveEnabled }
        delegate = raw
        try {
            if (stopped) throw IOException("Connection cancelled")
            raw.connect(InetSocketAddress(uri.host, if (uri.port < 0) 443 else uri.port), if(timeoutMs > 0) timeoutMs else 4000)
            val secure = factory.createSocket(raw, uri.host, if (uri.port < 0) 443 else uri.port, true) as javax.net.ssl.SSLSocket
            delegate = secure
            secure.soTimeout = 8000
            secure.startHandshake()
            val request = "POST /hub/v1/connect/open?to=${config.target} HTTP/1.1\r\nHost: ${uri.rawAuthority}\r\nAuthorization: Bearer ${config.token}\r\nConnection: Upgrade\r\nUpgrade: onespace-connect-v1\r\nContent-Length: 0\r\n\r\n"
            secure.outputStream.write(request.toByteArray(Charsets.US_ASCII))
            secure.outputStream.flush()
            // Read one byte at a time: bytes after headers belong to the inner PC TLS.
            val headers = java.io.ByteArrayOutputStream()
            var tail = 0
            while (headers.size() < 16384) {
                val byte = secure.inputStream.read()
                if (byte < 0) throw IOException("Hub closed connection")
                headers.write(byte)
                tail = (tail shl 8) or byte
                if (headers.size() >= 4 && tail == 0x0d0a0d0a) break
            }
            val response = headers.toString("US-ASCII")
            if (!response.endsWith("\r\n\r\n") || !response.startsWith("HTTP/1.1 101 ") || !response.lowercase().contains("upgrade: onespace-connect-v1\r\n"))
                throw IOException("Pi connection unavailable; check PC listener, device scopes and hub address")
            secure.soTimeout = timeout
            if (stopped) throw IOException("Connection cancelled")
        } catch (e: Exception) { HubApi.invalidateRoute(); close(); throw e }
    }
    override fun getInputStream() = (delegate ?: throw IOException("Not connected")).getInputStream()
    override fun getOutputStream() = (delegate ?: throw IOException("Not connected")).getOutputStream()
    override fun close() { stopped = true; delegate?.close() }
    override fun isConnected() = delegate?.isConnected ?: false
    override fun isClosed() = stopped || delegate?.isClosed == true
    override fun getInetAddress() = delegate?.inetAddress
    override fun getPort() = delegate?.port ?: 0
    override fun getLocalAddress() = delegate?.localAddress ?: InetAddress.getLoopbackAddress()
    override fun getLocalPort() = delegate?.localPort ?: -1
    override fun setSoTimeout(value: Int) { timeout = value; delegate?.soTimeout = value }
    override fun getSoTimeout() = delegate?.soTimeout ?: timeout
    override fun setTcpNoDelay(value: Boolean) { noDelay = value; delegate?.tcpNoDelay = value }
    override fun getTcpNoDelay() = delegate?.tcpNoDelay ?: noDelay
    override fun setKeepAlive(value: Boolean) { keepAliveEnabled = value; delegate?.keepAlive = value }
    override fun shutdownInput() { delegate?.shutdownInput() }
    override fun shutdownOutput() { delegate?.shutdownOutput() }
}
