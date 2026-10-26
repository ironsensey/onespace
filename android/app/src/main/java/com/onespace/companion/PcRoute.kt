package com.onespace.companion

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities

/** Explicit private-overlay route; leaves original LAN host and identity intact. */
object PcRoute {
    private fun prefs(context: Context) = context.getSharedPreferences("onespace_pc_route", Context.MODE_PRIVATE)
    fun enabled(context: Context) = prefs(context).getBoolean("enabled", false)
    fun metered(context: Context) = prefs(context).getBoolean("metered", false)
    fun savedHost(context: Context) = prefs(context).getString("host", "") ?: ""
    fun revision(context: Context) = prefs(context).getLong("revision", 0L)
    fun host(context: Context, store: SecureStore) = if (enabled(context)) savedHost(context) else store.host()
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
    fun recover(context: Context, store: SecureStore): String? = if (enabled(context)) null else AddressRecovery.recover(context, store)
    fun allowedNetwork(context: Context): Boolean {
        val connectivity = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val caps = connectivity.getNetworkCapabilities(connectivity.activeNetwork ?: return false) ?: return false
        if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) || caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) return true
        // An unrelated available Wi-Fi network does not prove that a VPN is
        // using it. Fail closed when Android reports only VPN transport.
        return enabled(context) && metered(context) &&
            (caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) || caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN))
    }
}
