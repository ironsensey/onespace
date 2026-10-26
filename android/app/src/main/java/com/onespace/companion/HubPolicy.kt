package com.onespace.companion

import java.net.URI

/** Protocol bounds independent of Android so security rules run in JVM tests. */
object HubPolicy {
    const val MAX_TEXT_BYTES = 16384
    fun baseUrl(address: String): String {
        val uri = URI("https://$address")
        require(uri.scheme == "https" && uri.host != null && uri.userInfo == null &&
            uri.rawPath.isNullOrEmpty() && uri.rawQuery == null && uri.rawFragment == null) { "Enter a host or host:port, without a URL path" }
        require(uri.port == -1 || uri.port in 1..65535) { "Invalid port" }
        val host = uri.host.removePrefix("[").removeSuffix("]")
        require(host.matches(Regex("[A-Za-z0-9.:-]+"))) { "Invalid host" }
        return "https://${if (host.contains(':')) "[$host]" else host}:${if (uri.port == -1) 47603 else uri.port}"
    }
    fun normalizedPin(value: String): String {
        val pin = value.trim().lowercase().replace(":", "").replace(" ", "")
        require(pin.matches(Regex("[a-f0-9]{64}"))) { "Expected a SHA-256 fingerprint" }
        return pin
    }
    fun validTarget(value: String) = value.matches(Regex("[A-Za-z0-9_-]{1,64}"))
    fun validText(value: String) = value.isNotBlank() && value.toByteArray(Charsets.UTF_8).size <= MAX_TEXT_BYTES
    fun sessionPath(base: String, location: String): String {
        val resolved = URI(base).resolve(location)
        val origin = URI(base)
        require(resolved.scheme == origin.scheme && resolved.host == origin.host && resolved.port == origin.port && resolved.userInfo == null && resolved.rawQuery == null && resolved.rawFragment == null) { "Untrusted camera session URL" }
        require(resolved.rawPath.matches(Regex("/hub/v1/camera/session/[A-Za-z0-9_-]{1,128}"))) { "Invalid camera session path" }
        return resolved.rawPath
    }
}
