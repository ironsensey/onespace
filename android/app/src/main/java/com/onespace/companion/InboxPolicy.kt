package com.onespace.companion

/** Pure policies shared by receiving workers; testable without an Android device. */
object InboxPolicy {
    fun matchesLane(kind: String, lane: String): Boolean = when (lane) {
        "text" -> kind == "text"
        "files", "images" -> kind != "text"
        else -> true
    }
    fun clipboardSupported(kind: String, name: String): Boolean = kind == "text" ||
        listOf(".png", ".jpg", ".jpeg", ".webp").any { name.lowercase().endsWith(it) }
    fun acceptsClipboard(previous: Long, incoming: Long): Boolean = incoming >= previous
    fun retryDelay(previous: Long): Long = (previous * 2).coerceAtMost(30000L)
}
