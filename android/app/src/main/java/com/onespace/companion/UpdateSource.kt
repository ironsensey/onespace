package com.onespace.companion

import java.io.InputStream
import org.json.JSONObject

/** Both transports feed the unchanged package/hash/signer verification. */
interface UpdateSource {
    fun request(path: String, method: String = "GET", obj: JSONObject? = null): JSONObject
    fun downloadUpdate(receiver: (InputStream) -> Unit)
}
class HubUpdateSource(private val hub: HubApi) : UpdateSource {
    override fun request(path: String, method: String, obj: JSONObject?): JSONObject {
        require(path == "/v1/update/manifest" && method == "GET" && obj == null)
        return hub.request("/hub/v1/updates/manifest")
    }
    override fun downloadUpdate(receiver: (InputStream) -> Unit) = hub.downloadUpdate(receiver)
}
