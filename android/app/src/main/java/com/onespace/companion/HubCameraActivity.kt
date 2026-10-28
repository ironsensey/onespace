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
        if (web != null || !HubProfile(this).configured()) return
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
                val client = HubProfile(this@HubCameraActivity).client(viewerRevision)
                val response = client.raw("/hub/v1/camera/status")
                val iceServers = JSONObject(response.body).optJSONArray("iceServers") ?: org.json.JSONArray()
                runOnUiThread {
                    if (authorized && web === view && HubProfile(this@HubCameraActivity).revision() == viewerRevision) {
                        view.evaluateJavascript("setIceServers(${iceServers})", null)
                    }
                }
            } catch (_: Exception) {
                runOnUiThread {
                    if (authorized && web === view) status.text = "Live view unavailable: camera network configuration could not be loaded"
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
                    session = null; sessionOwner = null
                    if (old != null) try { oldOwner?.freshForCleanup()?.raw(old, "DELETE") } catch (_: Exception) {}
                    if (generation.get() != attempt || !authorized) return@execute
                    val client = HubProfile(this@HubCameraActivity).client(viewerRevision)
                    api = client
                    val answer = client.raw("/hub/v1/camera/whep", "POST", sdp, "application/sdp")
                    val location = answer.location ?: throw java.io.IOException("Camera session missing")
                    val path = HubPolicy.sessionPath(client.baseUrl, location)
                    if (generation.get() != attempt || !authorized) {
                        try { client.freshForCleanup().raw(path, "DELETE") } catch (_: Exception) {}
                        return@execute
                    }
                    session = path
                    sessionOwner = client
                    runOnUiThread {
                        if (generation.get() == attempt && authorized) {
                            web?.evaluateJavascript("acceptAnswer(${JSONObject.quote(answer.body)},$clientAttempt)", null)
                            status.text = "Live view · video connection negotiating"
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
        session = null; sessionOwner = null
        if (close != null) try { worker.execute { try { closeOwner?.freshForCleanup()?.raw(close, "DELETE") } catch (_: Exception) {} } } catch (_: java.util.concurrent.RejectedExecutionException) {}
        super.onPause()
    }
    override fun onDestroy() { worker.shutdown(); super.onDestroy() }
}
