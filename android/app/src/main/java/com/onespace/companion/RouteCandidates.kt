package com.onespace.companion

/** Probe before choosing a route; never retry a transfer that may have committed. */
object RouteCandidates {
    fun choose(local: String, remote: String, verify: (String) -> Boolean): String? =
        listOf(local, remote).filter { it.isNotBlank() }.distinct().firstOrNull(verify)
}
