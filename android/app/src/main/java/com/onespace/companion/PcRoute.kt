package com.onespace.companion

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities

/** Prefers a pinned LAN connection, with an explicitly enabled verified remote fallback. */
object PcRoute {
    private fun prefs(context: Context) = context.getSharedPreferences("onespace_pc_route", Context.MODE_PRIVATE)
    fun enabled(context: Context) = prefs(context).getBoolean("enabled", false)
    fun builtin(context: Context) = prefs(context).getBoolean("builtin", false)
    fun metered(context: Context) = prefs(context).getBoolean("metered", false)
    fun savedHost(context: Context) = prefs(context).getString("host", "") ?: ""
    fun revision(context: Context) = prefs(context).getLong("revision", 0L)
    private data class Selection(val key: String, val host: String, val until: Long, val hub: Boolean = false)
    @Volatile private var selected: Selection? = null
    fun host(context: Context, store: SecureStore): String {
        if (!enabled(context) && !builtin(context)) return store.host()
        val connectivity = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val key = "${revision(context)}:${HubProfile(context).revision()}:${connectivity.activeNetwork}:${store.host()}:${store.fingerprint()}"
        val now = android.os.SystemClock.elapsedRealtime()
        selected?.let { if (it.key == key && it.until > now) return it.host }
        // Callers on the UI thread must never perform a connection probe.
        if (android.os.Looper.myLooper() == android.os.Looper.getMainLooper()) return store.host()
        val destination = RouteCandidates.choose(store.host(), if (enabled(context)) savedHost(context) else "") { candidate ->
            try { OneSpaceApi(candidate, store.fingerprint()).verifyRoute() }
            catch (_: Exception) { false }
        } ?: run {
            val rediscovered = AddressRecovery.recover(context, store)
            if (rediscovered != null) {
                selected = Selection(key, rediscovered, now + 30000L)
                return rediscovered
            }
            if (!builtin(context)) return store.host()
            val hub = HubProfile(context)
            require(hub.configured() && HubPolicy.validTarget(hub.target())) { "Configure the Pi and PC target first" }
            val tunnel = HubTunnelConfig(hub.host(), hub.fingerprint(), hub.token(), hub.target(), hub.localHost())
            require(OneSpaceApi(store.host(), store.fingerprint(), tunnel = tunnel).verifyRoute(4000)) { "Pi could not reach the paired PC" }
            selected = Selection(key, store.host(), now + 30000L, true)
            return store.host()
        }
        selected = Selection(key, destination, now + 30000L)
        return destination
    }
    fun api(context: Context, store: SecureStore): OneSpaceApi {
        val destination = host(context, store)
        val hub = HubProfile(context)
        val relay = if (builtin(context) && selected?.hub == true) HubTunnelConfig(hub.host(), hub.fingerprint(), hub.token(), hub.target(), hub.localHost()) else null
        return OneSpaceApi(destination, store.fingerprint(), store.token(), relay, onNetworkFailure = { selected = null }) { recover(context, store) }
    }
    @Synchronized fun setBuiltin(context: Context, value: Boolean) {
        val hub = HubProfile(context)
        require(!value || hub.configured() && HubPolicy.validTarget(hub.target())) { "Configure a Pi and PC target first" }
        selected = null
        prefs(context).edit().putBoolean("builtin", value).putLong("revision", revision(context) + 1L).apply()
    }
    @Synchronized fun save(context: Context, host: String) {
        require(host.matches(Regex("[A-Za-z0-9][A-Za-z0-9.-]{0,252}"))) { "Use the PC overlay DNS name or IPv4 address, without port or URL" }
        prefs(context).edit().putString("host", host).putLong("revision", revision(context) + 1L).apply()
    }
    @Synchronized fun setEnabled(context: Context, value: Boolean) {
        require(!value || savedHost(context).isNotEmpty()) { "Verify an overlay PC address first" }
        prefs(context).edit().putBoolean("enabled", value).putLong("revision", revision(context) + 1L).apply()
    }
    @Synchronized fun setMetered(context: Context, value: Boolean) {
        prefs(context).edit().putBoolean("metered", value).putLong("revision", revision(context) + 1L).apply()
    }
    fun recover(context: Context, store: SecureStore): String? {
        if (!enabled(context) && !builtin(context)) return AddressRecovery.recover(context, store)
        selected = null
        AddressRecovery.recover(context, store)
        return host(context, store)
    }
    fun allowedNetwork(context: Context): Boolean {
        val connectivity = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val caps = connectivity.getNetworkCapabilities(connectivity.activeNetwork ?: return false) ?: return false
        if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) || caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) return true
        // An unrelated available Wi-Fi network does not prove that a VPN is
        // using it. Fail closed when Android reports only VPN transport.
        return (enabled(context) || builtin(context)) && metered(context) &&
            (caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) || caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN))
    }
}
