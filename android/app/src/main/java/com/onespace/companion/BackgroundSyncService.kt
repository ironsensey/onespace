package com.onespace.companion

import android.Manifest
import android.app.*
import android.content.*
import android.content.pm.PackageManager
import android.database.ContentObserver
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Uri
import android.os.*
import android.provider.MediaStore
import org.json.JSONObject
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/** Visible, user-enabled connected-device foreground service.
 * A foreground service is NOT a guarantee of permanence on Android/OEM firmware.
 * Watches newly saved camera photos, and checks the per-device outbox while enabled.
 */
class BackgroundSyncService : Service() {
    private val queue = Executors.newSingleThreadExecutor()
    // A dedicated I/O thread can wait for incoming items without blocking camera uploads.
    private val inboxWorker = Executors.newFixedThreadPool(2)
    private val hubWorker = Executors.newSingleThreadExecutor()
    @Volatile private var hubClient: HubApi? = null
    private val handler = Handler(Looper.getMainLooper())
    private lateinit var settings: FeatureSettings
    private lateinit var store: SecureStore
    private var cameraWatcher: ContentObserver? = null
    @Volatile private var stopping = false
    @Volatile private var running = false
    @Volatile private var processing = false
    private var lastReceiveMode = ""
    // Coalesce camera changes without losing events that arrive during an upload.
    private val scanQueued = AtomicBoolean(false)
    private val scanAgain = AtomicBoolean(false)
    private var lastCleanupMs = 0L
    private val networkSignal = java.lang.Object()
    @Volatile private var wifiWasActive = false
    private var connectionManager: ConnectivityManager? = null
    private val networkListener = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: android.net.Network) { onNetworkChanged() }
        override fun onLost(network: android.net.Network) { hubClient?.cancel(); onNetworkChanged() }
        override fun onCapabilitiesChanged(network: android.net.Network, caps: NetworkCapabilities) { onNetworkChanged() }
    }
    private fun onNetworkChanged() {
        synchronized(networkSignal) { networkSignal.notifyAll() }
        val active = onLocalWifi()
        if (active && !wifiWasActive) {
            handler.post { if (running && !stopping) dispatchCameraScan() }
        }
        wifiWasActive = active
    }
    private val prefs by lazy { getSharedPreferences("onespace_background", MODE_PRIVATE) }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        settings = FeatureSettings(this)
        store = SecureStore(this)
        val channelId = "onespace_connected"
        val manager = getSystemService(NOTIFICATION_SERVICE) as NotificationManager
        manager.createNotificationChannel(NotificationChannel(channelId, "OneSpace connectivity", NotificationManager.IMPORTANCE_LOW))
        val pending = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val notification = Notification.Builder(this, channelId)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle("OneSpace connected")
            .setContentText("Selected background features are active")
            .setOngoing(true).setContentIntent(pending).build()
        try {
            if (Build.VERSION.SDK_INT >= 34)
                startForeground(47601, notification, android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
            else startForeground(47601, notification)
        } catch (_: Exception) {
            stopSelf()
            return
        }
        running = true
        try {
            connectionManager = getSystemService(CONNECTIVITY_SERVICE) as ConnectivityManager
            connectionManager?.registerDefaultNetworkCallback(networkListener)
        } catch (_: Exception) { /* Retry loop remains available. */ }
        setupObserver()
        handler.post(syncLoop)
        inboxWorker.execute { inboxLoop("text") }
        inboxWorker.execute { inboxLoop("files") }
        hubWorker.execute { hubLoop() }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == "STOP" || !settings.get("background") || (!store.paired() && !HubProfile(this).configured())) {
            stopSelf(); return START_NOT_STICKY
        }
        // Refresh the observer on settings changes (startService from Activity).
        setupObserver()
        val mode = "${settings.get("inbox")}:${settings.get("clip_receive")}:${settings.get("hub_receive")}:${HubProfile(this).revision()}:${PcRoute.revision(this)}"
        if (mode != lastReceiveMode) { activeClients.values.forEach { it.cancel() }; hubClient?.cancel(); lastReceiveMode = mode }
        synchronized(networkSignal) { networkSignal.notifyAll() }
        if (settings.get("camera")) dispatchCameraScan()
        return START_STICKY
    }

    private fun mediaPermission(): Boolean =
        checkSelfPermission(if (Build.VERSION.SDK_INT >= 33) Manifest.permission.READ_MEDIA_IMAGES
            else Manifest.permission.READ_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED

    private fun newestPhotoId(): Long {
        val projection = arrayOf(MediaStore.Images.Media._ID)
        return contentResolver.query(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, projection,
            null, null, "${MediaStore.Images.Media._ID} DESC")?.use { c ->
            if (c.moveToFirst()) c.getLong(0) else 0L
        } ?: 0L
    }

    private fun setupObserver() {
        cameraWatcher?.let { contentResolver.unregisterContentObserver(it) }
        cameraWatcher = null
        if (!settings.get("camera") || !mediaPermission()) return
        // Initialize at enabling time: never bulk-upload pre-existing private photos.
        if (!prefs.contains("last_photo_id")) {
            try { prefs.edit().putLong("last_photo_id", newestPhotoId()).apply() } catch (_: Exception) {}
        }
        cameraWatcher = object : ContentObserver(handler) {
            override fun onChange(selfChange: Boolean) { scheduleCameraScan() }
            override fun onChange(selfChange: Boolean, uri: Uri?) { scheduleCameraScan() }
        }.also { contentResolver.registerContentObserver(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, true, it) }
    }

    // Camera applications may first publish a pending image and finish writing it later.
    // Several bounded event-driven retries get it to Windows quickly without a fast
    // permanent background polling loop. A new event resets this burst.
    private val cameraDelaysMs = longArrayOf(250L, 1250L, 4000L, 9000L)
    private val cameraScans = cameraDelaysMs.map { Runnable { dispatchCameraScan() } }
    private fun scheduleCameraScan() {
        for (i in cameraScans.indices) {
            // Keep the earliest scheduled check during event bursts.
            if (!handler.hasCallbacks(cameraScans[i]))
                handler.postDelayed(cameraScans[i], cameraDelaysMs[i])
        }
    }

    private fun dispatchCameraScan() {
        if (!running || stopping || !settings.get("camera")) return
        if (!scanQueued.compareAndSet(false, true)) {
            scanAgain.set(true)
            return
        }
        try {
            queue.execute {
                try {
                    do {
                        scanAgain.set(false)
                        scanCamera()
                    } while (scanAgain.getAndSet(false) && running && !stopping)
                } finally {
                    scanQueued.set(false)
                    if (scanAgain.getAndSet(false) && running && !stopping) dispatchCameraScan()
                }
            }
        } catch (_: java.util.concurrent.RejectedExecutionException) {
            scanQueued.set(false) // Shutdown may race a final camera callback.
        }
    }

    private fun scanCamera() {
        if (!settings.get("camera") || !mediaPermission() || !store.paired() || processing || !onLocalWifi()) return
        processing = true
        try {
            // High-water IDs alone miss photos finalized after newer photos.
            // Use a bounded lookback and remember completed uploads to avoid duplicates.
            var baseline = prefs.getLong("camera_baseline", -1L)
            if (baseline < 0L) {
                baseline = prefs.getLong("last_photo_id", newestPhotoId())
                prefs.edit().putLong("camera_baseline", baseline).apply()
            }
            var highWater = prefs.getLong("camera_high_water", baseline).coerceAtLeast(baseline)
            val uploaded = (prefs.getString("camera_sent_ids", "") ?: "")
                .split(',').filter { it.isNotEmpty() }.toMutableSet()
            val cutoff = maxOf(baseline, highWater - 512L)
            val projection = arrayOf(MediaStore.Images.Media._ID,
                MediaStore.Images.Media.DISPLAY_NAME, MediaStore.Images.Media.RELATIVE_PATH)
            contentResolver.query(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, projection,
                "${MediaStore.Images.Media._ID} > ? AND ${MediaStore.Images.Media.IS_PENDING} = 0",
                arrayOf(cutoff.toString()), "${MediaStore.Images.Media._ID} ASC")?.use { c ->
                while (c.moveToNext() && !stopping) {
                    val id = c.getLong(0)
                    val name = c.getString(1) ?: "Camera-$id.jpg"
                    val dir = (c.getString(2) ?: "").replace('\\', '/').lowercase()
                    val cameraDirectory = dir.startsWith("dcim/camera/") ||
                        dir.startsWith("pictures/camera/") || dir.startsWith("dcim/100andro/")
                    if (id > baseline && cameraDirectory && !uploaded.contains(id.toString())) {
                        val uri = ContentUris.withAppendedId(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, id)
                        val started = SystemClock.elapsedRealtime()
                        OneSpaceApi(PcRoute.host(this, store), store.fingerprint(), store.token()) { PcRoute.recover(this, store) }
                            .upload(contentResolver, uri, name, false, true)
                        android.util.Log.i("OneSpace", "Camera delivery completed in ${SystemClock.elapsedRealtime() - started}ms")
                        uploaded.add(id.toString())
                        prefs.edit().putString("camera_sent_ids", uploaded.toList().takeLast(1024).joinToString(",")).apply()
                    }
                    if (id > highWater) highWater = id
                }
            }
            prefs.edit().putLong("camera_high_water", highWater).apply()
        } catch (error: Exception) {
            android.util.Log.w("OneSpace", "Camera delivery paused; will retry", error)
            // Camera-only users (Inbox disabled) also need to recover from PC
            // DHCP address changes. Never replay an uncertain POST immediately;
            // schedule a fresh scan only after a verified new address is found.
            if (error is java.io.IOException && PcRoute.recover(this, store) != null) {
                handler.postDelayed({ dispatchCameraScan() }, 1200L)
            }
        } finally { processing = false }
    }

    private val syncLoop = object : Runnable {
        override fun run() {
            if (!running || stopping) return
            if (settings.get("camera")) dispatchCameraScan() // rare fallback for missed OEM media events
            val now = SystemClock.elapsedRealtime()
            if (now - lastCleanupMs > 3600000L || lastCleanupMs == 0L) {
                lastCleanupMs = now
                try { queue.execute { ReceiveStore(this@BackgroundSyncService).cleanup(); InboxDelivery.cleanup(this@BackgroundSyncService) } }
                catch (_: java.util.concurrent.RejectedExecutionException) {}
            }
            handler.postDelayed(this, 10 * 60 * 1000L) // rare fallback; normally MediaStore events trigger scans
        }
    }

    private fun onLocalWifi(): Boolean = PcRoute.allowedNetwork(this)

    /** The Windows server holds an authenticated request until there is a new item
     * or the wait expires. This removes the old 0–60 second delivery delay.
     * Back off on failure to avoid waking the radio repeatedly when offline.
     */
    private fun inboxLoop(lane: String) {
        var retryMs = 1500L
        while (!stopping && !Thread.currentThread().isInterrupted) {
            if (!running || (!settings.get("inbox") && !settings.get("clip_receive")) || !store.paired() || !onLocalWifi()) {
                if (!sleepInterruptibly(30000L)) break
                continue
            }
            try {
                fetchInbox(50, lane)
                retryMs = 1500L
            } catch (ex: InterruptedException) {
                Thread.currentThread().interrupt()
                break
            } catch (ex: Exception) {
                android.util.Log.w("OneSpace", "Inbox connection unavailable; retrying", ex)
                if (!sleepInterruptibly(retryMs)) break
                retryMs = InboxPolicy.retryDelay(retryMs)
            }
        }
    }

    private fun sleepInterruptibly(milliseconds: Long): Boolean = try {
        synchronized(networkSignal) { networkSignal.wait(milliseconds) }
        true
    } catch (_: InterruptedException) {
        Thread.currentThread().interrupt()
        false
    }

    private val activeClients = java.util.concurrent.ConcurrentHashMap<String, OneSpaceApi>()
    private fun fetchInbox(waitSeconds: Int, lane: String) {
        val client = OneSpaceApi(PcRoute.host(this, store), store.fingerprint(), store.token()) { PcRoute.recover(this, store) }
        activeClients[lane] = client
        try { InboxDelivery.fetch(this, client, waitSeconds, if (lane == "files" && !settings.get("inbox")) "images" else lane) { running && !stopping } }
        finally { activeClients.remove(lane, client) }
    }

    private fun hubLoop() {
        var retry = 1500L
        while (!stopping && !Thread.currentThread().isInterrupted) {
            val hub = HubProfile(this)
            val connectivity = getSystemService(CONNECTIVITY_SERVICE) as ConnectivityManager
            if (!running || !settings.get("hub_receive") || !hub.configured() || connectivity.activeNetwork == null) {
                if (!sleepInterruptibly(30000L)) break
                continue
            }
            try {
                val revision = hub.revision()
                val client = hub.client(revision)
                hubClient = client
                HubSync.receive(this, client) { running && !stopping && settings.get("hub_receive") && hub.configured() && hub.revision() == revision }
                retry = 1500L
            } catch (_: InterruptedException) { Thread.currentThread().interrupt(); break }
            catch (_: Exception) {
                // Never log clipboard data, tokens, or server response bodies.
                if (!sleepInterruptibly(retry)) break
                retry = InboxPolicy.retryDelay(retry)
            } finally { hubClient = null }
        }
    }

    override fun onDestroy() {
        stopping = true; running = false
        handler.removeCallbacks(syncLoop); cameraScans.forEach { handler.removeCallbacks(it) }
        cameraWatcher?.let { contentResolver.unregisterContentObserver(it) }
        try { connectionManager?.unregisterNetworkCallback(networkListener) } catch (_: Exception) {}
        synchronized(networkSignal) { networkSignal.notifyAll() }
        activeClients.values.forEach { it.cancel() }
        hubClient?.cancel()
        hubWorker.shutdownNow()
        inboxWorker.shutdownNow()
        queue.shutdownNow()
        super.onDestroy()
    }
}
