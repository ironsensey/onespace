package com.onespace.companion

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.app.KeyguardManager
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentValues
import android.content.Context
import android.content.BroadcastReceiver
import android.content.IntentFilter
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.hardware.biometrics.BiometricPrompt
import android.media.AudioManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.CancellationSignal
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.text.InputType
import android.view.View
import android.view.WindowInsets
import android.graphics.drawable.GradientDrawable
import android.os.Handler
import android.os.Looper
import android.widget.*
import org.json.JSONObject
import java.io.IOException
import java.util.concurrent.Executors

/** Android companion, Java platform API only. Network calls run off the main/UI thread. */
class MainActivity : Activity() {
    private val worker = Executors.newSingleThreadExecutor()
    // Clipboard transfers must not queue behind note loads, file downloads or UI requests.
    private val clipboardWorker = Executors.newSingleThreadExecutor()
    private val clipboardGeneration = java.util.concurrent.atomic.AtomicInteger()
    private lateinit var features: FeatureSettings
    private val mainHandler = Handler(Looper.getMainLooper())
    private var currentPage = "Home"
    private var lastPushed = ""
    private var lastPulled = ""
    private var clipboardListener: ClipboardManager.OnPrimaryClipChangedListener? = null
    private var inboxReceiverRegistered = false
    private val inboxReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (unlocked && ::features.isInitialized && (features.get("clip_receive") || features.get("hub_receive")) && window.decorView.hasWindowFocus())
                ClipboardInbox.pasteIntoClipboard(this@MainActivity)
        }
    }
    private var clipPullBusy = false
    private lateinit var store: SecureStore
    private lateinit var received: ReceiveStore
    private lateinit var root: LinearLayout
    private lateinit var status: TextView
    private var pendingCameraUri: Uri? = null
    private var unlocked = false
    private var updateCheckedThisSession = false
    private var updateDialog: AlertDialog? = null
    private var backgroundSince = 0L
    private var permanent = false
    private var pairingHost = ""
    private var pairingPin = ""
    private var pendingRequest = ""
    private var pendingTicket = ""
    private val filePick = 301
    private val cameraCapture = 302
    private val credentialUnlock = 303
    private val imagePermission = 304

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.statusBarColor = Color.rgb(11, 17, 32)
        window.navigationBarColor = Color.rgb(11, 17, 32)
        store = SecureStore(this)
        features = FeatureSettings(this)
        received = ReceiveStore(this)
        val cm = getSystemService(CLIPBOARD_SERVICE) as ClipboardManager
        clipboardListener = ClipboardManager.OnPrimaryClipChangedListener {
            // Suppress echo: content received from OneSpace must not be sent straight back.
            val fromOneSpace = cm.primaryClipDescription?.label?.toString()?.startsWith("OneSpace ") == true
            if (!fromOneSpace && unlocked && ((features.get("hub_send") && HubProfile(this).configured()) || (!features.get("hub_send") && features.get("clip_send") && store.paired())) && window.decorView.hasWindowFocus()) {
                val value = try { cm.primaryClip?.getItemAt(0)?.coerceToText(this)?.toString() ?: "" } catch (_: Exception) { "" }
                if (value.isNotBlank() && value != lastPulled && value.length < 250000) {
                    lastPushed = value
                    val generation = clipboardGeneration.incrementAndGet()
                    val viaHub = features.get("hub_send")
                    val hubRevision = HubProfile(this).revision()
                    val hubTarget = HubProfile(this).target()
                    val pcFingerprint = store.fingerprint()
                    val pcRevision = PcRoute.revision(this)
                    clipboardWorker.execute {
                        // When copying rapidly, only the newest value needs to be sent.
                        if (generation != clipboardGeneration.get()) return@execute
                        for (attempt in 0..2) {
                            if (generation != clipboardGeneration.get() || viaHub != features.get("hub_send")) break
                            if (viaHub && (hubRevision != HubProfile(this).revision() || hubTarget != HubProfile(this).target())) break
                            if (!viaHub && (pcFingerprint != store.fingerprint() || pcRevision != PcRoute.revision(this) || !features.get("clip_send"))) break
                            try {
                                if (viaHub) {
                                    val hub = HubProfile(this)
                                    hub.client(hubRevision).sendText(hubTarget, value)
                                } else api().request("/v1/items/text", "POST", JSONObject()
                                    .put("text", value).put("bucket", "Temporary").put("clipboard", true))
                                break
                            } catch (e: Exception) {
                                android.util.Log.w("OneSpace", "Clipboard transfer attempt ${attempt + 1} failed", e)
                                if (viaHub) break
                                if (attempt < 2) try { Thread.sleep(350L shl attempt) }
                                catch (_: InterruptedException) { Thread.currentThread().interrupt(); break }
                            }
                        }
                    }
                }
            }
        }
        cm.addPrimaryClipChangedListener(clipboardListener!!)
        showScreen()
        if(store.paired() || HubProfile(this).configured()) requestUnlock() else handleShareIntent(intent)
    }
    override fun onNewIntent(intent: Intent) { super.onNewIntent(intent); setIntent(intent); if(unlocked)handleShareIntent(intent) }
    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus && ::features.isInitialized && ::store.isInitialized &&
            unlocked && (features.get("clip_receive") || features.get("hub_receive"))) ClipboardInbox.pasteIntoClipboard(this)
    }
    @android.annotation.SuppressLint("UnspecifiedRegisterReceiverFlag") // Pre-33 branch is protected by our signature-only permission.
    override fun onResume() {
        super.onResume()
        if (!inboxReceiverRegistered) {
            val filter = IntentFilter(ClipboardInbox.ACTION_INBOX_READY)
            if (Build.VERSION.SDK_INT >= 33) registerReceiver(inboxReceiver, filter, Context.RECEIVER_NOT_EXPORTED)
            else registerReceiver(inboxReceiver, filter, "$packageName.permission.INBOX", null)
            inboxReceiverRegistered = true
        }
        if (::features.isInitialized && ::store.isInitialized &&
            (store.paired() || HubProfile(this).configured()) && unlocked && features.get("background")) refreshBackgroundService()
        if (unlocked && (features.get("clip_receive") || features.get("hub_receive")))
            mainHandler.post { if (!isFinishing && unlocked) ClipboardInbox.pasteIntoClipboard(this) }
    }
    override fun onPause() {
        if (inboxReceiverRegistered) {
            unregisterReceiver(inboxReceiver)
            inboxReceiverRegistered = false
        }
        super.onPause()
    }
    override fun onStop() {
        super.onStop()
        backgroundSince = System.currentTimeMillis()
    }
    override fun onStart() {
        super.onStart()
        if (::store.isInitialized && (store.paired() || HubProfile(this).configured()) && unlocked && backgroundSince > 0 &&
            System.currentTimeMillis() - backgroundSince > 5 * 60 * 1000) {
            unlocked = false
            showScreen()
            requestUnlock()
        }
    }
    override fun onDestroy() {
        clipboardListener?.let { (getSystemService(CLIPBOARD_SERVICE) as ClipboardManager).removePrimaryClipChangedListener(it) }
        super.onDestroy(); worker.shutdownNow(); clipboardWorker.shutdownNow()
    }

    // Windows now delivers clipboard updates through the authenticated inbox.
    // Do not poll /v1/clipboard/pull every 8 seconds: stale text could overwrite
    // an incoming photo, waste Wi-Fi and make clipboard behavior appear random.
    private fun dp(x: Int) = (x * resources.displayMetrics.density).toInt()
    private fun baseColor(): Int = when (features.theme) {
        "Forest" -> Color.rgb(8, 26, 22)
        "Plum" -> Color.rgb(27, 15, 38)
        "Graphite" -> Color.rgb(22, 24, 29)
        else -> Color.rgb(9, 16, 30)
    }
    private fun accentColor(): Int = when (features.theme) {
        "Forest" -> Color.rgb(31, 169, 126)
        "Plum" -> Color.rgb(169, 102, 232)
        "Graphite" -> Color.rgb(122, 148, 182)
        else -> Color.rgb(25, 175, 205)
    }
    private fun panel(bg: Int? = null): GradientDrawable = GradientDrawable().apply {
        cornerRadius = dp(16).toFloat()
        setColor(bg ?: Color.rgb(27, 40, 61))
        setStroke(dp(1), Color.rgb(51, 66, 89))
    }
    private fun topbar(title: String, sub: String = "") {
        val bar = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL; gravity = android.view.Gravity.CENTER_VERTICAL }
        if (title != "OneSpace") {
            bar.addView(button("‹", { navigate("Home") }).apply { minWidth = dp(48); textSize = 23f }, LinearLayout.LayoutParams(dp(56), dp(54)))
        }
        val texts = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setPadding(dp(6), 0, 0, 0) }
        texts.addView(label(title, 26f, true))
        if (sub.isNotBlank()) texts.addView(label(sub, 12f))
        bar.addView(texts, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
        root.addView(bar)
    }
    private fun navigate(page: String) { currentPage=page; showScreen() }
    @Deprecated("Navigate back to OneSpace dashboard")
    override fun onBackPressed() { if (currentPage != "Home" && store.paired() && unlocked) navigate("Home") else super.onBackPressed() }


    private fun label(text: String, size: Float = 14f, bold: Boolean = false): TextView = TextView(this).apply {
        this.text=text; textSize=size; setTextColor(if(bold) Color.WHITE else Color.rgb(183, 202, 229))
        setPadding(0, 9, 0, 9);if(bold)setTypeface(null, android.graphics.Typeface.BOLD)
    }
    private fun field(hint: String, password: Boolean = false): EditText = EditText(this).apply {
        this.hint=hint; setSingleLine(true); textSize=15f; setTextColor(Color.WHITE)
        setHintTextColor(Color.rgb(145, 165, 190)); setPadding(16, 14, 16, 14)
        setBackgroundColor(Color.rgb(31, 44, 66));if(password)inputType=InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
    }
    private fun button(text: String, action: () -> Unit): Button = Button(this).apply {
        this.text = text; isAllCaps = false; textSize = 14f; setTextColor(Color.WHITE)
        background = panel(accentColor()); setPadding(dp(14), dp(11), dp(14), dp(11))
        val layout = LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, dp(54))
        layout.topMargin = dp(9); layoutParams = layout
        setOnClickListener { action() }
    }
    private fun section(title: String) {
        val t=label(title,20f,true);t.setPadding(0,25,0,12);root.addView(t)
    }
    private fun info(msg: String) { runOnUiThread { status.text = msg; Toast.makeText(this, msg.take(160), Toast.LENGTH_SHORT).show() } }
    private fun runTask(action: () -> Unit) { worker.execute { try{action()}catch(e: Exception){info("⚠ ${e.message ?: "Operation failed"}")} } }
    private fun api(): OneSpaceApi {
        if(!store.paired())throw IllegalStateException("Pair a Windows PC first")
        return OneSpaceApi(PcRoute.host(this,store),store.fingerprint(),store.token()) { PcRoute.recover(this,store) }
    }
    private fun showScreen() {
        val scroll=ScrollView(this).apply { setBackgroundColor(baseColor()); isFillViewport=true; clipToPadding=false }
        if (Build.VERSION.SDK_INT >= 30) scroll.setOnApplyWindowInsetsListener { _, insets ->
            val bars = insets.getInsets(WindowInsets.Type.systemBars())
            scroll.setPadding(0, bars.top, 0, bars.bottom)
            insets
        }
        root=LinearLayout(this).apply{orientation=LinearLayout.VERTICAL;setPadding(dp(18),dp(14),dp(18),dp(28))}
        scroll.addView(root);setContentView(scroll)
        if (!store.paired() && !HubProfile(this).configured()) topbar("OneSpace", "Connect your first device")
        else if (!unlocked) topbar("OneSpace", "Private workspace locked")
        else topbar(if (currentPage == "Home") "OneSpace" else currentPage,
            if (currentPage == "Home") "Your connected workspace · v${installedVersion()}" else "Windows ↔ Android")
        status=label(if(store.paired())"Paired PC · ${store.host()} · availability not yet checked" else if (HubProfile(this).configured()) "Pi hub configured · ${HubProfile(this).host()}" else "Not paired", 12f)
        root.addView(status)
        if(!store.paired() && !HubProfile(this).configured()) {
            if (currentPage == "Hub") hubPage() else {
                onboarding()
                root.addView(button("Set up Raspberry Pi hub instead") { navigate("Hub") })
            }
            return
        }
        if(!unlocked){root.addView(button("Unlock with biometrics / device PIN") { requestUnlock() });return}
        when (currentPage) {
            "Home" -> homePage()
            "Hub" -> hubPage()
            "Pair PC" -> onboarding()
            "Clipboard" -> clipboardPage()
            "Camera" -> cameraPage()
            "Files" -> filesPage()
            "Workspace" -> workspacePage()
            "Devices" -> devicesPage()
            "Controls" -> controlsPage()
            "Updates" -> updatesPage()
            "Settings" -> settingsPage()
            else -> homePage()
        }
        if (store.paired()) autoCheckForUpdatesOnce()
    }
    private fun onboarding() {
        section("Connect to your Windows PC")
        root.addView(label("On Windows, open http://127.0.0.1:47600 → Devices. Copy the PC IP and the complete SHA-256 fingerprint. This verifies that no one on the Wi-Fi is impersonating your PC."))
        val ip=field("PC IP, e.g. 192.168.1.10");val fingerprint=field("64-character PC fingerprint");val pass=field("Ecosystem password",true)
        val device=field("This phone's name").apply{text?.append(Build.MANUFACTURER + " " + Build.MODEL)}
        listOf(ip,fingerprint,pass,device).forEach {root.addView(it)}
        root.addView(button("Request secure pairing") {
            val host=ip.text.toString().trim();val hash=fingerprint.text.toString();val pwd=pass.text.toString();val name=device.text.toString().trim()
            if(pwd.isEmpty()||name.isEmpty()){info("Enter a password and device name");return@button}
            runTask {
                val client=OneSpaceApi(host,hash)
                val req=client.request("/v1/pair/request","POST", JSONObject().put("password",pwd).put("name",name))
                pairingHost=host;pairingPin=hash;pendingRequest=req.getString("id");pendingTicket=req.getString("ticket")
                info("Pair request sent. Approve this phone in the Windows dashboard.")
                // Poll with a time limit; user must approve locally on the PC.
                for (i in 0 until 65) {
                    if(Thread.currentThread().isInterrupted)return@runTask
                    Thread.sleep(3000)
                    val result=client.request("/v1/pair/status?id=$pendingRequest&ticket=$pendingTicket")
                    when(result.getString("state")) {
                        "approved" -> {store.save(pairingHost,pairingPin,result.getString("token"));runOnUiThread {onUnlocked()};info("Windows paired successfully");return@runTask}
                        "rejected" -> throw IOException("Pair request was rejected on Windows")
                    }
                }
                info("Pairing timed out; request again")
            }
        })
    }
    private fun onUnlocked() {
        unlocked = true
        showScreen()
        if (features.get("background")) refreshBackgroundService()
        handleShareIntent(intent)
    }
    private fun receiveSettingChanged(enabled: Boolean) {
        if (enabled) {
            // This explicit receive opt-in starts the visible connection service.
            features.set("background", true)
            askNotificationThenStart()
        } else refreshBackgroundService()
    }

    private fun requestUnlock() {
        val keyguard=getSystemService(KEYGUARD_SERVICE) as KeyguardManager
        if(!keyguard.isDeviceSecure){
            info("Please enable an Android screen lock to protect OneSpace")
            try {startActivity(Intent(android.provider.Settings.ACTION_SECURITY_SETTINGS))} catch (_: Exception) {}
            return
        }
        if(Build.VERSION.SDK_INT >= 30){
            try {
                val manager=getSystemService(BIOMETRIC_SERVICE) as android.hardware.biometrics.BiometricManager
                val available=manager.canAuthenticate(android.hardware.biometrics.BiometricManager.Authenticators.BIOMETRIC_STRONG or android.hardware.biometrics.BiometricManager.Authenticators.DEVICE_CREDENTIAL)
                if(available==android.hardware.biometrics.BiometricManager.BIOMETRIC_SUCCESS){
                    val prompt=BiometricPrompt.Builder(this)
                        .setTitle("Unlock OneSpace")
                        .setSubtitle("Access your private cross-device workspace")
                        .setAllowedAuthenticators(android.hardware.biometrics.BiometricManager.Authenticators.BIOMETRIC_STRONG or android.hardware.biometrics.BiometricManager.Authenticators.DEVICE_CREDENTIAL)
                        .build()
                    prompt.authenticate(CancellationSignal(), mainExecutor, object: BiometricPrompt.AuthenticationCallback(){
                        override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult){onUnlocked()}
                        override fun onAuthenticationError(errorCode: Int, errString: CharSequence){info("Authentication cancelled: $errString")}
                    });return
                }
            }catch(_:Exception){}
        }
        val confirm=keyguard.createConfirmDeviceCredentialIntent("Unlock OneSpace","Confirm your device screen lock")
        if(confirm!=null)startActivityForResult(confirm,credentialUnlock)else info("Device credential authentication unavailable")
    }
    private fun tile(title: String, detail: String, symbol: String, destination: String) {
        val row = LinearLayout(this).apply {
            orientation=LinearLayout.HORIZONTAL; gravity=android.view.Gravity.CENTER_VERTICAL
            background=panel(); setPadding(dp(16),dp(12),dp(14),dp(12))
            isClickable=true; isFocusable=true
            setOnClickListener { navigate(destination) }
        }
        val icon = label(symbol,25f,true).apply {gravity=android.view.Gravity.CENTER}
        row.addView(icon,LinearLayout.LayoutParams(dp(48),dp(54)))
        val group=LinearLayout(this).apply{orientation=LinearLayout.VERTICAL;setPadding(dp(10),0,0,0)}
        group.addView(label(title,16f,true));group.addView(label(detail,12f))
        row.addView(group,LinearLayout.LayoutParams(0,LinearLayout.LayoutParams.WRAP_CONTENT,1f))
        row.addView(label("›",24f,true))
        val lp=LinearLayout.LayoutParams(-1,-2);lp.topMargin=dp(10)
        root.addView(row,lp)
    }
    private fun note(text: String) {
        val t=label(text,13f).apply {setPadding(dp(16),dp(14),dp(16),dp(14));background=panel()}
        val lp=LinearLayout.LayoutParams(-1,-2);lp.topMargin=dp(10);root.addView(t,lp)
    }
    private fun switchRow(key: String, title: String, explanation: String, onChange: ((Boolean)->Unit)?=null) {
        val row=LinearLayout(this).apply { orientation=LinearLayout.HORIZONTAL;gravity=android.view.Gravity.CENTER_VERTICAL;setPadding(dp(13),dp(9),dp(9),dp(9)); background=panel() }
        val col=LinearLayout(this).apply {orientation=LinearLayout.VERTICAL}
        col.addView(label(title,15f,true));col.addView(label(explanation,12f))
        row.addView(col,LinearLayout.LayoutParams(0,-2,1f))
        val sw=Switch(this).apply {isChecked=features.get(key);setPadding(dp(8),0,0,0)}
        row.addView(sw)
        sw.setOnCheckedChangeListener { _,enabled ->
            features.set(key,enabled)
            onChange?.invoke(enabled)
        }
        val lp=LinearLayout.LayoutParams(-1,-2);lp.topMargin=dp(9);root.addView(row,lp)
    }
    private fun sendHubAction(action: (HubApi, String) -> Unit) {
        val hub = HubProfile(this)
        val revision = hub.revision()
        val destination = hub.target()
        runTask { action(hub.client(revision), destination) }
    }

    private fun hubPage() {
        val hub = HubProfile(this)
        section("Raspberry Pi hub")
        note("Pi Hub Preview · optional Google Drive storage and recording. Use a trusted LAN or your private Tailscale network. Existing PC pairing stays unchanged. Hub text mode is explicit; it does not silently reroute uncertain LAN sends.")
        val host = field("Pi host or overlay DNS name:47603").apply { setText(hub.host()) }
        val pin = field("Pi SHA-256 certificate fingerprint").apply { setText(hub.fingerprint()) }
        val token = field("Device token from Pi enrollment", true)
        val target = field("Target device ID, e.g. windows").apply { setText(hub.target()) }
        listOf(host, pin, token, target).forEach { root.addView(it) }
        note("Copy the fingerprint directly from your Pi. Leave token blank to retain your saved encrypted credential. Give this phone only the scopes it needs; camera and clipboard are separate permissions.")
        root.addView(button("Verify and save hub") {
            val address = host.text.toString().trim()
            val fingerprint = pin.text.toString().trim()
            val enteredCredential = token.text.toString().trim()
            val destination = target.text.toString().trim()
            token.text.clear()
            runTask {
                val credential = enteredCredential.ifBlank {
                    require(hub.configured() && HubPolicy.normalizedPin(fingerprint) == hub.fingerprint()) { "Enter a new token when changing hub identity" }
                    hub.token()
                }
                val client = HubApi(address, fingerprint, credential)
                require(client.request("/hub/v1/info").optString("app") == "OneSpace Hub") { "Unexpected hub" }
                client.list("/hub/v1/devices") // Verify token authorization before retaining it.
                hub.save(address, fingerprint, credential, destination)
                runOnUiThread {
                    if (unlocked) { refreshBackgroundService(); showScreen() } else requestUnlock()
                    info("Hub identity and credential verified")
                }
            }
        })
        if (!hub.configured() || !unlocked) return
        root.addView(button("Refresh trusted devices") { runTask {
            val devices = hub.client().list("/hub/v1/devices")
            val lines = (0 until devices.length()).map { i -> val d = devices.getJSONObject(i)
                "${d.optString("id")} · ${d.optString("name")} · ${if (d.optBoolean("online")) "recently connected" else "offline"}" }
            runOnUiThread { AlertDialog.Builder(this).setTitle("Hub devices").setMessage(lines.joinToString("\n").ifBlank { "No other devices" }).setPositiveButton("Close", null).show() }
        } })
        switchRow("hub_send", "Use hub for copied text", "Explicit hub route while OneSpace is focused; LAN remains the default") { enabled ->
            if (enabled && !HubPolicy.validTarget(hub.target())) { features.set("hub_send", false); info("Save a valid target device ID first"); showScreen() }
        }
        switchRow("hub_receive", "Receive hub clipboard text", "Visible background connection; mobile data may be used") { enabled ->
            if (enabled) { features.set("background", true); askNotificationThenStart() } else refreshBackgroundService()
        }
        note("Hub clipboard messages expire quickly. Paste is activated only while OneSpace is unlocked and focused. This preview relays text and media commands; pictures/files keep using the existing direct PC connection.")
        val text = field("Text to send through Pi")
        root.addView(text)
        root.addView(button("Send hub text now") { val value = text.text.toString(); sendHubAction { client, destination -> client.sendText(destination, value); info("Text queued; recipient delivery is separate") } })
        if (store.paired()) {
            section("Direct PC over private overlay")
            note("Optional: carry existing files, images, camera photos and APK updates over your private overlay. Same PC certificate and token; original LAN address is preserved. No automatic uncertain-POST fallback.")
            val pcHost = field("PC Tailscale DNS name or IPv4 address").apply { setText(PcRoute.savedHost(this@MainActivity)) }
            root.addView(pcHost)
            root.addView(button("Verify private PC address") {
                val address = pcHost.text.toString().trim()
                runTask {
                    require(address.matches(Regex("[A-Za-z0-9][A-Za-z0-9.-]{0,252}"))) { "Enter a DNS name or IPv4 address, without port" }
                    val result = OneSpaceApi(address, store.fingerprint(), store.token()).request("/v1/info")
                    require(result.optString("app") == "OneSpace") { "Unexpected PC endpoint" }
                    PcRoute.save(this, address)
                    info("PC identity verified with existing certificate; enable overlay route below")
                }
            })
            val route = Switch(this).apply { this.text = "Enable verified remote PC fallback (LAN preferred)"; setTextColor(Color.WHITE); isChecked = PcRoute.enabled(this@MainActivity) }
            route.setOnCheckedChangeListener { _, enabled ->
                try { PcRoute.setEnabled(this, enabled); refreshBackgroundService() }
                catch (_: Exception) { route.isChecked = false; info("Verify an overlay PC address first") }
            }
            root.addView(route)
            val mobile = Switch(this).apply { this.text = "Allow PC background sync on mobile data"; setTextColor(Color.WHITE); isChecked = PcRoute.metered(this@MainActivity) }
            mobile.setOnCheckedChangeListener { _, enabled -> PcRoute.setMetered(this, enabled); refreshBackgroundService() }
            root.addView(mobile)
            note("Mobile transfer is off by default. Enabling it may send large camera photos and files using billable data. Automatic background sync otherwise stays on Wi-Fi/Ethernet. VPN networks with unknown physical routing also require this consent. The updated PC must explicitly allow its private overlay subnet.")
        }
        section("Media controls")
        note("Connect-style controls target the chosen Windows device. It must opt in locally and be online. These are system media keys, not a Spotify account integration.")
        listOf("play_pause" to "Play / Pause", "next" to "Next track", "previous" to "Previous track", "stop" to "Stop").forEach { (action, title) ->
            root.addView(button(title) { sendHubAction { client, destination -> client.media(destination, action); info("Media command queued") } })
        }
        section("Pi live camera")
        note("Receive-only WebRTC. Recording is managed separately in Cloud & recordings. No microphone or phone-camera access. Playback stops when this screen leaves the foreground.")
        root.addView(button("Open live camera") {
            HubUiSession.allowLaunch()
            startActivity(Intent(this, HubCameraActivity::class.java))
        })
        root.addView(button("Cloud storage & recordings") {
 HubUiSession.allowLaunch();startActivity(Intent(this, HubCloudActivity::class.java))
 })
 root.addView(button("Disconnect hub") {
            features.set("hub_send", false); features.set("hub_receive", false)
            hub.clear(); refreshBackgroundService(); currentPage = "Home"; showScreen()
        })
    }

    private fun homePage() {
        note("OneSpace · Local first\nPC and Pi hub credentials remain separate. Connection status is verified on request.")
        section("Quick access")
        tile("Pi Hub & Live Camera", "Live view, remote text, media controls", "◉", "Hub")
        if (!store.paired()) {
            note("Your Pi works independently of Windows. Add a separate PC pairing only for direct PC features.")
            root.addView(button("Add direct Windows pairing") { navigate("Pair PC") })
            root.addView(button("Settings") { navigate("Settings") })
            return
        }
        tile("Clipboard", "Copy & paste sync options", "▤", "Clipboard")
        tile("Camera Sync", "New photos → Windows clipboard", "◎", "Camera")
        tile("Files & storage", "Permanent / 24-hour temporary", "▣", "Files")
        tile("Workspace", "Tasks, notes and received items", "☷", "Workspace")
        tile("Devices & permissions", "Trusted PCs and access rules", "⌘", "Devices")
        tile("Remote controls", "Volume, sleep, restart and more", "◉", "Controls")
        tile("Updates", "Install full signed APK releases", "↻", "Updates")
        tile("Appearance & features", "Themes, privacy, background behavior", "⚙", "Settings")
    }
    private fun clipboardPage() {
        section("Seamless clipboard")
        note("Windows can automatically monitor text/image changes when enabled in Windows Settings. This phone can automatically receive them into OneSpace while background sync is enabled. To PASTE on Android, bring OneSpace to the foreground using its received-item notification. Android doesn't allow ordinary background apps to read other apps' clipboard.")
        switchRow("clip_send","Share Android text clipboard","Send new text copied while OneSpace is foregrounded")
        switchRow("clip_receive","Receive Windows clipboard","Starts a visible connection; paste when OneSpace is foregrounded") { receiveSettingChanged(it) }
        section("Manual quick share")
        val input=field("Text or link to share");root.addView(input)
        root.addView(button("Send text to Windows") {
            val content=input.text.toString().trim()
            if(content.isBlank()){info("Enter text first");return@button}
            runTask{api().request("/v1/items/text","POST",JSONObject().put("text",content).put("bucket","Temporary").put("clipboard",true));info("Sent to Windows clipboard")}
        })
        root.addView(button("Get Windows clipboard now") {runTask {
            val text=api().request("/v1/clipboard/pull").getString("text")
            lastPulled=text
            runOnUiThread {(getSystemService(CLIPBOARD_SERVICE) as ClipboardManager).setPrimaryClip(ClipData.newPlainText("OneSpace",text));info("Ready to paste on Android")}
        }})
    }
    private fun cameraPage() {
        section("Instant Camera Sync")
        note("When enabled, OneSpace runs a visible foreground service and watches NEW pictures saved in standard Camera folders. Photos are sent to your trusted Windows PC. Android and phone manufacturers can suspend background work; this is best effort, not guaranteed 24/7.")
        switchRow("camera","Send new camera photos automatically","Requires full photo access and background connection") { enabled ->
            if(enabled) {features.set("background",true);askMediaPermissionThenStart();showScreen()} else refreshBackgroundService()
        }
        switchRow("background","Keep OneSpace connection active","Visible notification; some phones may stop it") { enabled ->
            if(enabled) askNotificationThenStart() else refreshBackgroundService()
        }
        section("Share a photo now")
        root.addView(button("Take photo → Windows Ctrl+V") {permanent=false;launchCamera()})
        root.addView(button("Send last camera photo") {permanent=false;latestPhoto()})
        note("New camera photos are sent to Windows and queued for other paired Android devices. Other phones receive the image in their OneSpace folder; open the notification/OneSpace to activate Android clipboard paste. Android blocks universal background clipboard insertion.")
    }
    private fun filesPage() {
        section("Two storage locations")
        note("PERMANENT\nDownloads/OneSpace/Permanent\n\nTEMPORARY · deleted after 24 hours\nDownloads/OneSpace/Temporary\nCleanup occurs when Android permits scheduled/background work or when the app opens.")
        root.addView(button("Choose file or photo to send") {permanent=false;openPicker()})
        root.addView(button("Choose file to save permanently") {permanent=true;openPicker()})
        root.addView(button("Receive files waiting on Windows") {runTask {fetchInbox()} })
        root.addView(button("Create folders / clear expired temporary files") {runTask {
            received.ensureFolders();val count=received.cleanup();info("Folders ready. Cleaned $count files.")
        }})
        root.addView(button("Open Android Files picker") {
            try{startActivity(Intent(Intent.ACTION_OPEN_DOCUMENT_TREE))}catch(e:Exception){info("Open Files → Downloads → OneSpace")}
        })
        note("Current Android destination uses system Downloads so your received files remain visible in Files. Custom Android folder selection will need SAF grant persistence and is planned for a later update.")
    }
    private fun workspacePage() {
        section("Notes & tasks")
        root.addView(button("View shared tasks and notes") {runTask {loadWorkspace()} })
        root.addView(button("Add task") {editTask()})
        root.addView(button("Add note") {editNote()})
        section("Incoming files")
        root.addView(button("Fetch queued files") {runTask{fetchInbox()} })
        switchRow("inbox","Automatically fetch PC inbox","Starts a visible background connection") { receiveSettingChanged(it) }
    }
    private fun devicesPage() {
        section("Trusted devices")
        note("Paired Windows server: ${store.host()}\nServer certificate SHA-256: ${store.fingerprint().chunked(8).joinToString(" ")}\n\nOnly the Windows administrator can grant/revoke another device's access. Device permission switches must be enforced by Windows, not merely hidden in the phone UI.")
        root.addView(button("Unpair this phone") {AlertDialog.Builder(this).setTitle("Unpair OneSpace?")
            .setMessage("Local credentials will be cleared. Revoke this phone in Windows → Devices too.")
            .setPositiveButton("Unpair") {_,_->stopBackground();store.clear();unlocked=false;currentPage="Home";showScreen()}
            .setNegativeButton("Cancel",null).show()})
    }
    private fun controlsPage() {
        section("Volume")
        val volume=SeekBar(this).apply{max=100;progress=50};root.addView(volume)
        root.addView(button("Set Windows volume") {runTask{api().request("/v1/actions/volume","POST",JSONObject().put("percent",volume.progress));info("Windows volume: ${volume.progress}%")}})
        root.addView(button("Set Android media volume") {
            val audio=getSystemService(AUDIO_SERVICE) as AudioManager
            val max=audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC)
            audio.setStreamVolume(AudioManager.STREAM_MUSIC,(volume.progress*max/100).coerceIn(0,max),0)
            info("Phone volume set")
        })
        section("Windows power")
        root.addView(button("Lock / Sleep / Restart / Shutdown") {powerMenu()})
        note("Windows requires approval for each remote power request. Ordinary Android applications cannot silently restart or shut down a personal phone.")
    }
    private fun installedVersion(): String = try {
        @Suppress("DEPRECATION")
        packageManager.getPackageInfo(packageName,0).versionName ?: "unknown"
    } catch (_: Exception) { "unknown" }
    private fun installedVersionCode(): Long = try {
        @Suppress("DEPRECATION")
        packageManager.getPackageInfo(packageName,0).longVersionCode
    } catch (_: Exception) { 0 }
    private fun updatesPage() {
        section("Update Center")
        note("OneSpace v${installedVersion()} (code ${installedVersionCode()})\nFull APK updates are downloaded from your authenticated, certificate-pinned Windows PC. Android verifies the release signature and asks to install it.")
        root.addView(button("Check for an update") {checkUpdates()})
        root.addView(button("Install downloaded update") {
            try{if(!OneSpaceUpdater(this).launchInstaller())info("Allow OneSpace to install apps when Android asks, then return")}
            catch(e:Exception){info("Update installer: ${e.message}")}
        })
    }
    private fun settingsPage() {
        section("Appearance")
        note("Choose a theme. It changes the entire app immediately.")
        listOf("Midnight","Forest","Plum","Graphite").forEach{theme->
            root.addView(button((if(features.theme==theme) "✓  " else "")+theme) {features.theme=theme;showScreen()})
        }
        section("Background and privacy")
        switchRow("background","Foreground connection service","Android shows a notification; not guaranteed permanent") {enabled->
            if(enabled)askNotificationThenStart() else refreshBackgroundService()
        }
        switchRow("camera","Automatic new camera photos","Send to paired PC; full media permission needed") {enabled->
            if(enabled) {features.set("background",true);askMediaPermissionThenStart();showScreen()} else refreshBackgroundService()
        }
        switchRow("inbox","Receive shared files","Receive queued files in the background") { receiveSettingChanged(it) }
        switchRow("clip_send","Send copied text","Works while OneSpace is open and focused")
        switchRow("clip_receive","Receive copied text/photos","Starts background reception; paste when app is foregrounded") { receiveSettingChanged(it) }
        root.addView(button("Android battery/background settings") {
            // Phone vendors, including Xiaomi, sometimes require user approval for unrestricted background work.
            try { startActivity(Intent(android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                Uri.parse("package:$packageName"))) }
            catch (_: Exception) { info("Open Android Settings → Apps → OneSpace → Battery") }
        })
        note("For reliable camera detection on Xiaomi: permit full Photos access, allow notifications, and choose a suitable Battery setting for OneSpace. Android may still stop background services.")
        section("Security")
        root.addView(button("Lock app now") {unlocked=false;currentPage="Home";showScreen()})
        note("Biometric or Android screen lock remains required when reopening protected OneSpace content.")
    }
    private fun hasPhotoPermission(): Boolean = checkSelfPermission(if(Build.VERSION.SDK_INT>=33)
        Manifest.permission.READ_MEDIA_IMAGES else Manifest.permission.READ_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
    private fun askMediaPermissionThenStart() {
        if (!hasPhotoPermission()) {
            requestPermissions(arrayOf(if(Build.VERSION.SDK_INT>=33) Manifest.permission.READ_MEDIA_IMAGES
                else Manifest.permission.READ_EXTERNAL_STORAGE), 405)
        } else askNotificationThenStart()
    }
    private fun askNotificationThenStart() {
        if(Build.VERSION.SDK_INT>=33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS),406)
        } else refreshBackgroundService()
    }
    private fun refreshBackgroundService() {
        if (features.get("camera") && !hasPhotoPermission()) {
            info("Camera Sync needs full photo access; clipboard reception can still run")
        }
        if (features.get("background") && (store.paired() || HubProfile(this).configured())) {
            try { startForegroundService(Intent(this,BackgroundSyncService::class.java)) }
            catch (e:Exception) { info("Cannot start background service: ${e.message}") }
        } else stopBackground()
    }
    private fun stopBackground() {
        try {stopService(Intent(this,BackgroundSyncService::class.java))}catch(_:Exception){}
    }
    private fun openPicker() {val i=Intent(Intent.ACTION_OPEN_DOCUMENT).apply{type="*/*";addCategory(Intent.CATEGORY_OPENABLE)};startActivityForResult(i,filePick)}
    private fun launchCamera() {
        val values=ContentValues().apply {put(MediaStore.MediaColumns.DISPLAY_NAME,"OneSpace-${System.currentTimeMillis()}.jpg");put(MediaStore.MediaColumns.MIME_TYPE,"image/jpeg")}
        pendingCameraUri=contentResolver.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values)
        val camera=Intent(MediaStore.ACTION_IMAGE_CAPTURE).apply{putExtra(MediaStore.EXTRA_OUTPUT,pendingCameraUri);addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION or Intent.FLAG_GRANT_READ_URI_PERMISSION)}
        try{startActivityForResult(camera,cameraCapture)}catch(e:Exception){info("No available camera app: ${e.message}")}
    }
    private fun latestPhoto() {
        val permitted=if(Build.VERSION.SDK_INT>=33)checkSelfPermission(Manifest.permission.READ_MEDIA_IMAGES)==PackageManager.PERMISSION_GRANTED
          else checkSelfPermission(Manifest.permission.READ_EXTERNAL_STORAGE)==PackageManager.PERMISSION_GRANTED
        if(!permitted){requestPermissions(arrayOf(if(Build.VERSION.SDK_INT>=33) Manifest.permission.READ_MEDIA_IMAGES else Manifest.permission.READ_EXTERNAL_STORAGE),imagePermission);return}
        runTask {
            val columns=arrayOf(MediaStore.Images.Media._ID,MediaStore.Images.Media.DISPLAY_NAME)
            contentResolver.query(MediaStore.Images.Media.EXTERNAL_CONTENT_URI,columns,null,null,"${MediaStore.Images.Media.DATE_ADDED} DESC")?.use {cursor->
                if(cursor.moveToFirst()){
                    val id=cursor.getLong(0);val name=cursor.getString(1)?:"photo.jpg"
                    val uri=android.content.ContentUris.withAppendedId(MediaStore.Images.Media.EXTERNAL_CONTENT_URI,id)
                    sendUri(uri,name,true)
                }else info("No photos found")
            } ?: info("Unable to read photos")
        }
    }
    private fun sendUri(uri: Uri, name: String, photo: Boolean) {val client=api()
        val result = client.upload(contentResolver,uri,name,permanent,photo)
        if (photo && !result.optBoolean("clipboardApplied", true)) {
            info("Photo stored on Windows, but clipboard activation failed; check Windows logs"); return
        }
        info(if(photo)"Photo sent to Windows clipboard — use Ctrl+V" else "File securely transferred")
    }
    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode,permissions,grantResults)
        if(requestCode==imagePermission && grantResults.firstOrNull()==PackageManager.PERMISSION_GRANTED) latestPhoto()
        else if(requestCode==imagePermission)info("Photo access was not granted")
        if(requestCode==405) {
            if(hasPhotoPermission()){features.set("background",true);askNotificationThenStart()}
            else {features.set("camera",false);info("Automatic Camera Sync requires full photo access")}
            showScreen()
        }
        if(requestCode==406){
            if(grantResults.firstOrNull()==PackageManager.PERMISSION_GRANTED)refreshBackgroundService()
            else {info("Notifications disabled; Android may show the service in Task Manager only");refreshBackgroundService()}
        }
    }
    @Deprecated("Android activity results used to avoid non-platform dependencies")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode,resultCode,data)
        if(requestCode==credentialUnlock){if(resultCode==RESULT_OK){onUnlocked()};return}
        if(resultCode!=RESULT_OK){if(requestCode==cameraCapture)pendingCameraUri?.let{contentResolver.delete(it,null,null)};return}
        val uri=if(requestCode==cameraCapture)pendingCameraUri else data?.data
        if(uri!=null && (requestCode==filePick||requestCode==cameraCapture)){
            val name=displayName(uri)?:if(requestCode==cameraCapture)"camera-photo.jpg" else "shared-file"
            runTask{sendUri(uri,name,requestCode==cameraCapture||contentResolver.getType(uri)?.startsWith("image/")==true)}
        }
    }
    private fun displayName(uri: Uri): String? {
        return try{contentResolver.query(uri,arrayOf(OpenableColumns.DISPLAY_NAME),null,null,null)?.use{c->if(c.moveToFirst())c.getString(0)else null}}catch(_:Exception){null}
    }
    private fun handleShareIntent(i: Intent?) {
        if(!unlocked||!store.paired()||i?.action!=Intent.ACTION_SEND)return
        if(i.type=="text/plain"){
            val text=i.getStringExtra(Intent.EXTRA_TEXT)?:return
            runTask{api().request("/v1/items/text","POST",JSONObject().put("text",text).put("bucket","Temporary").put("clipboard",true));info("Shared text from Android to Windows")}
        } else {
            @Suppress("DEPRECATION") val uri:Uri?=if(Build.VERSION.SDK_INT>=33)i.getParcelableExtra(Intent.EXTRA_STREAM,Uri::class.java)else i.getParcelableExtra(Intent.EXTRA_STREAM)
            if(uri!=null)runTask{sendUri(uri,displayName(uri)?:"shared-image",i.type?.startsWith("image/")==true)}
        }
        setIntent(Intent(Intent.ACTION_MAIN))
    }
    private fun fetchInbox() {
        val saved = InboxDelivery.fetch(this, api())
        received.cleanup()
        InboxDelivery.cleanup(this)
        info(if (saved == 0) "Inbox empty or already received" else "Saved $saved items to Downloads/OneSpace")
    }
    private fun loadWorkspace() {
        val client=api();val tasks=client.list("/v1/tasks");val notes=client.list("/v1/notes")
        runOnUiThread {
            val view=LinearLayout(this).apply{orientation=LinearLayout.VERTICAL;setPadding(18,10,18,10)}
            view.addView(label("Tasks",19f,true))
            for (i in 0 until tasks.length()){
                val t=tasks.getJSONObject(i);val check=CheckBox(this).apply {text=t.getString("text");isChecked=t.optBoolean("done");setTextColor(Color.WHITE)}
                check.setOnCheckedChangeListener { _,checked->runTask {api().request("/v1/tasks","POST",JSONObject().put("id",t.getString("id")).put("text",t.getString("text")).put("done",checked));info("Task updated")}}
                view.addView(check)
            }
            view.addView(label("Notes",19f,true))
            for(i in 0 until notes.length()){
                val n=notes.getJSONObject(i)
                view.addView(label("${n.getString("title")}\n${n.optString("body")}",14f))
            }
            AlertDialog.Builder(this).setTitle("Shared workspace").setView(ScrollView(this).apply{addView(view)}).setPositiveButton("Close",null).show()
        }
    }
    private fun editTask() {
        val edit=field("New task")
        AlertDialog.Builder(this).setTitle("Create shared task").setView(edit).setPositiveButton("Save") {_,_->
            val text=edit.text.toString().trim();if(text.isNotEmpty())runTask{api().request("/v1/tasks","POST",JSONObject().put("text",text).put("done",false));info("Task saved")}
        }.setNegativeButton("Cancel",null).show()
    }
    private fun editNote() {
        val view=LinearLayout(this).apply{orientation=LinearLayout.VERTICAL}
        val title=field("Note title");val content=field("Note content")
        view.addView(title);view.addView(content)
        AlertDialog.Builder(this).setTitle("Create shared note").setView(view).setPositiveButton("Save") {_,_->
            if(title.text.isNotBlank())runTask{api().request("/v1/notes","POST",JSONObject().put("title",title.text.toString()).put("body",content.text.toString()));info("Note saved")}
        }.setNegativeButton("Cancel",null).show()
    }
    private fun showUpdateOffer(release: OneSpaceUpdater.Release, updater: OneSpaceUpdater, client: OneSpaceApi) {
        runOnUiThread {
            if (isFinishing || isDestroyed || !unlocked || updateDialog?.isShowing == true) return@runOnUiThread
            val cached = updater.isCached(release)
            updateDialog = AlertDialog.Builder(this)
                .setTitle(if (cached) "OneSpace ${release.versionName} ready to install" else "OneSpace ${release.versionName} available")
                .setMessage(if (cached) "Already downloaded and verified. Approve installation to update the app without removing your saved data."
                    else "Download the complete signed update (%.1f MB) from your trusted PC?".format(release.size / 1048576.0))
                .setNegativeButton("Later", null)
                .setPositiveButton(if (cached) "Install" else "Download") { _, _ ->
                    if (cached) {
                        try {
                            if (!updater.launchInstaller()) info("Allow OneSpace to install updates, then return")
                        } catch (e: Exception) { info("Update installer: ${e.message}") }
                    } else runTask {
                        updater.download(client, release)
                        info("Update downloaded and verified. Open Updates → Install downloaded update.")
                    }
                }.show()
            getSharedPreferences("onespace_update_prompts", MODE_PRIVATE).edit()
                .putLong("prompted_version", release.versionCode).apply()
        }
    }
    private fun checkUpdates() {
        runTask {
            val updater = OneSpaceUpdater(this)
            val client = api()
            val release = updater.check(client)
            if (release == null) info("You're running the latest published release on this PC")
            else showUpdateOffer(release, updater, client)
        }
    }
    /** Offer a particular release just once automatically. Manual Check always works. */
    private fun autoCheckForUpdatesOnce() {
        if (updateCheckedThisSession) return
        updateCheckedThisSession = true
        runTask {
            try {
                val updater = OneSpaceUpdater(this)
                val client = api()
                val release = updater.check(client)
                if (release != null && !updater.isCached(release)) {
                    val prefs = getSharedPreferences("onespace_update_prompts", MODE_PRIVATE)
                    if (prefs.getLong("prompted_version", 0L) != release.versionCode) {
                        showUpdateOffer(release, updater, client)
                    }
                }
            } catch (_: Exception) { /* PC may be offline; check again manually. */ }
        }
    }
    private fun powerMenu() {
        val actions=arrayOf("lock","sleep","restart","shutdown")
        AlertDialog.Builder(this).setTitle("Request Windows action")
            .setItems(actions){_,index->
                val action=actions[index]
                AlertDialog.Builder(this).setTitle("Request ${action}?")
                    .setMessage("This action still requires confirmation on the PC in OneSpace → Controls.")
                    .setPositiveButton("Request") {_,_->runTask{api().request("/v1/actions/power","POST",JSONObject().put("action",action));info("Requested $action. Approve on Windows.")}}
                    .setNegativeButton("Cancel",null).show()
            }.show()
    }
}
