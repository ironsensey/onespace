package com.onespace.companion

import android.content.Context

/** Separate encrypted credential binding; never replaces the existing PC pairing. */
class HubProfile(context: Context) {
    companion object { private val profileLock = Any() }
    private val secure = SecureStore(context, "onespace_hub", "onespace_hub_device_token")
    private val prefs = context.getSharedPreferences("onespace_hub_options", Context.MODE_PRIVATE)
    fun configured() = secure.paired()
    fun host() = secure.host()
    fun localHost() = prefs.getString("localHost", "") ?: ""
    fun fingerprint() = secure.fingerprint()
    fun token() = secure.token()
    fun revision() = prefs.getLong("revision", 0L)
    fun target() = prefs.getString("target", "") ?: ""
    fun save(host: String, fingerprint: String, token: String, target: String, localHost: String = "") = synchronized(profileLock) {
        HubPolicy.baseUrl(host)
        if (localHost.isNotBlank()) HubPolicy.baseUrl(localHost)
        val pin = HubPolicy.normalizedPin(fingerprint)
        require(token.length in 32..1024 && !token.any { it.isWhitespace() }) { "Invalid hub credential" }
        require(target.isEmpty() || HubPolicy.validTarget(target)) { "Invalid target device ID" }
        secure.save(host, pin, token)
        prefs.edit().putString("localHost",localHost).putString("target", target).putLong("revision", revision() + 1L).apply()
    }
    fun clear() = synchronized(profileLock) { val next = revision() + 1L; secure.clear(); prefs.edit().clear().putLong("revision", next).apply() }
    fun client(expectedRevision: Long? = null): HubApi = synchronized(profileLock) {
        require(expectedRevision == null || revision() == expectedRevision) { "Hub profile changed; copy again" }
        HubApi(host(), fingerprint(), token(), localHost())
    }
}
