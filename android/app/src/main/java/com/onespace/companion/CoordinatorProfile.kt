package com.onespace.companion

import android.content.Context
import org.json.JSONArray

/** Separate coordinator identity; it never replaces the pinned Pi hub profile. */
class CoordinatorProfile(context: Context) {
    private val secure = SecureStore(context, "onespace_coordinator", "onespace_coordinator_token")
    private val prefs = context.getSharedPreferences("onespace_coordinator_options", Context.MODE_PRIVATE)
    fun configured() = secure.paired() && device().isNotBlank() && target().isNotBlank()
    fun endpoint() = secure.host()
    fun fingerprint() = secure.fingerprint()
    fun token() = secure.token()
    fun device() = prefs.getString("device", "") ?: ""
    fun target() = prefs.getString("target", "") ?: ""
    fun iceServers(): JSONArray = try { JSONArray(prefs.getString("iceServers", "[]") ?: "[]") } catch (_: Exception) { JSONArray() }
    fun icePolicy() = prefs.getString("icePolicy", "all") ?: "all"
    fun revision() = prefs.getLong("revision", 0L)
    fun save(endpoint: String, fingerprint: String, token: String, device: String, target: String, iceServers: String = "[]", icePolicy: String = "all") = synchronized(this) {
        HubPolicy.coordinatorUrl(endpoint)
        HubPolicy.normalizedPin(fingerprint)
        require(token.length >= 32 && !token.any { it.isWhitespace() })
        require(HubPolicy.validTarget(device) && HubPolicy.validTarget(target) && device != target)
        require(JSONArray(iceServers).length() <= 8)
        require(icePolicy == "all" || icePolicy == "relay")
        secure.save(endpoint, HubPolicy.normalizedPin(fingerprint), token)
        prefs.edit().putString("device", device).putString("target", target).putString("iceServers", iceServers).putString("icePolicy", icePolicy).putLong("revision", revision() + 1L).apply()
    }
    fun clear() { secure.clear(); prefs.edit().clear().apply() }
    fun client(expectedRevision: Long? = null): CoordinatorApi {
        require(expectedRevision == null || revision() == expectedRevision)
        return CoordinatorApi(endpoint(), fingerprint(), token())
    }
}
