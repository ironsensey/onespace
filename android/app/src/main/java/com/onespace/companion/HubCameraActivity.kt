package com.onespace.companion

import android.app.Activity
import android.app.KeyguardManager
import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import android.os.SystemClock
import android.webkit.JavascriptInterface
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import org.json.JSONObject
import org.json.JSONArray
import java.io.ByteArrayInputStream
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

/** One-use in-memory permission to open the live screen after MainActivity unlock. */
object HubUiSession {
    private var until = 0L
    @Synchronized fun allowLaunch() { until = SystemClock.elapsedRealtime() + 30000L }
    @Synchronized fun consume(): Boolean {
        val allowed = until > 0 && SystemClock.elapsedRealtime() <= until
        until = 0
        return allowed
    }
}

/** Local-only viewer document; the JS bridge handles pinned authenticated SDP,
 * never hands credentials to JavaScript, and cannot browse arbitrary pages. */
class HubCameraActivity : Activity() {
    private val worker = Executors.newSingleThreadExecutor()
    private val generation = AtomicInteger()
    @Volatile private var api: HubApi? = null
    @Volatile private var session: String? = null
    @Volatile private var sessionOwner: HubApi? = null
    @Volatile private var coordinatorSession: String? = null
    @Volatile private var coordinatorOwner: CoordinatorApi? = null
    private var viewerRevision = 0L
    @Volatile private var authorized = false
    private var web: WebView? = null
    private lateinit var root: LinearLayout
    private lateinit var status: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        authorized = HubUiSession.consume()
        root = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setBackgroundColor(Color.rgb(10,16,32)); setPadding(20,20,20,20) }
        status = TextView(this).apply { setTextColor(Color.CYAN); text = "Pi live camera · recording managed in Cloud"; textSize = 18f }
        root.addView(status)
        setContentView(root)
    }
    override fun onResume() {
        super.onResume()
        if (authorized) showViewer() else showUnlock()
    }
    private fun showUnlock() {
        while (root.childCount > 1) root.removeViewAt(1)
        status.text = "Live camera locked"
        root.addView(Button(this).apply {
            text = "Unlock live view"
            setOnClickListener {
                val keyguard = getSystemService(KEYGUARD_SERVICE) as KeyguardManager
                val intent = keyguard.createConfirmDeviceCredentialIntent("OneSpace live camera", "Confirm your Android screen lock")
                if (keyguard.isDeviceSecure && intent != null) startActivityForResult(intent, 900)
                else status.text = "Enable an Android screen lock first"
            }
        })
    }
    @Deprecated("Platform activity result used without additional runtime dependencies")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == 900 && resultCode == RESULT_OK) { authorized = true; showViewer() }
    }
    @android.annotation.SuppressLint("SetJavaScriptEnabled") // Only the bundled, non-navigable viewer is loaded.
    private fun showViewer() {
        if (web != null || (!HubProfile(this).configured() && !CoordinatorProfile(this).configured())) return
        while (root.childCount > 1) root.removeViewAt(1)
        viewerRevision = HubProfile(this).revision()
        val view = WebView(this)
        web = view
        view.settings.javaScriptEnabled = true
        view.settings.allowFileAccess = false
        view.settings.allowContentAccess = false
        view.settings.domStorageEnabled = false
        view.settings.javaScriptCanOpenWindowsAutomatically = false
        view.settings.mediaPlaybackRequiresUserGesture = false
        view.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView?, request: WebResourceRequest?) = true
            override fun shouldInterceptRequest(view: WebView?, request: WebResourceRequest?): WebResourceResponse {
                // No scripts, navigation or network resources may be fetched by this document.
                return WebResourceResponse("text/plain", "UTF-8", ByteArrayInputStream(byteArrayOf()))
            }
        }
        view.addJavascriptInterface(Bridge(), "HubNative")
        root.addView(view, LinearLayout.LayoutParams(-1,0,1f))
        val html = assets.open("hub-live.html").bufferedReader().use { it.readText() }
        view.loadDataWithBaseURL("https://onespace.invalid/live/", html, "text/html", "UTF-8", null)
        status.text = "Live view · establishing authenticated WebRTC"
        worker.execute {
            try {
                val hub = HubProfile(this@HubCameraActivity)
                val coordinator = CoordinatorProfile(this@HubCameraActivity)
                val iceServers = if (hub.configured()) {
                    val response = hub.client(viewerRevision).raw("/hub/v1/camera/status")
                    JSONObject(response.body).optJSONArray("iceServers") ?: org.json.JSONArray()
                } else coordinator.iceServers()
                runOnUiThread {
                    if (authorized && web === view && HubProfile(this@HubCameraActivity).revision() == viewerRevision) {
                        view.evaluateJavascript("setIcePolicy('${CoordinatorProfile(this@HubCameraActivity).icePolicy()}');setIceServers(${iceServers})", null)
                    }
                }
            } catch (_: Exception) {
                runOnUiThread {
                    if (authorized && web === view && CoordinatorProfile(this@HubCameraActivity).configured()) {
                        view.evaluateJavascript("setIcePolicy('${CoordinatorProfile(this@HubCameraActivity).icePolicy()}');setIceServers(${CoordinatorProfile(this@HubCameraActivity).iceServers()})", null)
                    } else if (authorized && web === view) status.text = "Live view unavailable: camera network configuration could not be loaded"
                }
            }
        }
    }
    inner class Bridge {
        @JavascriptInterface fun offer(sdp: String, clientAttempt: Int) {
            if (!authorized || HubProfile(this@HubCameraActivity).revision() != viewerRevision || sdp.toByteArray().size > 131072) return
            api?.cancel()
            val attempt = generation.incrementAndGet()
            try { worker.execute {
                try {
                    val old = session
                    val oldOwner = sessionOwner
                    val oldCoordinator = coordinatorSession
                    val oldCoordinatorOwner = coordinatorOwner
                    session = null; sessionOwner = null
                    coordinatorSession = null; coordinatorOwner = null
                    if (old != null) try { oldOwner?.freshForCleanup()?.raw(old, "DELETE") } catch (_: Exception) {}
                    if (oldCoordinator != null && oldCoordinatorOwner != null) try { oldCoordinatorOwner.close(oldCoordinator, CoordinatorProfile(this@HubCameraActivity).device()) } catch (_: Exception) {}
                    if (generation.get() != attempt || !authorized) return@execute
                    var delivered = false
                    if (HubProfile(this@HubCameraActivity).configured()) {
                        try {
                            val client = HubProfile(this@HubCameraActivity).client(viewerRevision)
                            api = client
                            val answer = client.raw("/hub/v1/camera/whep", "POST", sdp, "application/sdp")
                            val location = answer.location ?: throw java.io.IOException("Camera session missing")
                            val path = HubPolicy.sessionPath(client.baseUrl, location)
                            if (generation.get() != attempt || !authorized) {
                                try { client.freshForCleanup().raw(path, "DELETE") } catch (_: Exception) {}
                                return@execute
                            }
                            session = path; sessionOwner = client; delivered = true
                            runOnUiThread {
                                if (generation.get() == attempt && authorized) {
                                    web?.evaluateJavascript("acceptAnswer(${JSONObject.quote(answer.body)},$clientAttempt)", null)
                                    status.text = "Live view · LAN/direct hub signaling"
                                }
                            }
                        } catch (_: Exception) { }
                    }
                    if (!delivered && CoordinatorProfile(this@HubCameraActivity).configured()) {
                        val profile = CoordinatorProfile(this@HubCameraActivity)
                        val coordinator = profile.client(profile.revision())
                        val created = coordinator.create(profile.device(), profile.target())
                        if (created.status !in 200..299) throw java.io.IOException("Coordinator denied camera session")
                        val sessionId = JSONObject(created.body).getString("session")
                        try {
                            val sent = coordinator.send(sessionId, profile.device(), "offer", sdp)
                            if (sent.status !in 200..299) throw java.io.IOException("Coordinator rejected offer")
                            val messages = coordinator.poll(sessionId, profile.device())
                            if (messages.status !in 200..299) throw java.io.IOException("Remote camera answer unavailable")
                            val list = JSONArray(messages.body)
                            var answer: String? = null
                            for (i in 0 until list.length()) {
                                val message = list.getJSONObject(i)
                                if (message.optString("type") == "answer") { answer = message.optString("body"); break }
                            }
                            val answerSdp = answer
                            if (answerSdp.isNullOrBlank()) throw java.io.IOException("Remote camera answer missing")
                            runOnUiThread {
                                if (generation.get() == attempt && authorized) {
                                    web?.evaluateJavascript("acceptAnswer(${JSONObject.quote(answerSdp)},$clientAttempt)", null)
                                    status.text = "Live view · coordinator/direct or TURN negotiating"
                                }
                            }
                            coordinatorSession = sessionId
                            coordinatorOwner = coordinator
                        } catch (error: Exception) {
                            try { coordinator.close(sessionId, profile.device()) } catch (_: Exception) {}
                            throw error
                        }
                    }
                } catch (_: Exception) {
                    runOnUiThread { if (generation.get() == attempt) status.text = "Live view unavailable: check camera scope, hub, camera publisher and overlay ICE route" }
                } finally { api = null }
            } } catch (_: java.util.concurrent.RejectedExecutionException) {}
        }
        @JavascriptInterface fun state(value: String) {
            if (!authorized) return
            val safe = when (value) { "connected" -> "Live view connected · recording managed separately"; "failed", "disconnected" -> "Video connection interrupted"; else -> "Live view · connecting" }
            runOnUiThread { if (authorized) status.text = safe }
        }
    }
    override fun onPause() {
        authorized = false
        generation.incrementAndGet()
        api?.cancel()
        web?.let { view -> view.evaluateJavascript("stopVideo()", null); view.removeJavascriptInterface("HubNative"); view.stopLoading(); view.onPause(); root.removeView(view); view.destroy() }
        web = null
        val close = session
        val closeOwner = sessionOwner
        val remoteClose = coordinatorSession
        val remoteOwner = coordinatorOwner
        session = null; sessionOwner = null
        coordinatorSession = null; coordinatorOwner = null
        if (close != null || remoteClose != null) try { worker.execute {
            try { if (close != null) closeOwner?.freshForCleanup()?.raw(close, "DELETE") } catch (_: Exception) {}
            try { if (remoteClose != null && remoteOwner != null) remoteOwner.close(remoteClose, CoordinatorProfile(this@HubCameraActivity).device()) } catch (_: Exception) {}
        } } catch (_: java.util.concurrent.RejectedExecutionException) {}
        super.onPause()
    }
    override fun onDestroy() { worker.shutdown(); super.onDestroy() }
}
