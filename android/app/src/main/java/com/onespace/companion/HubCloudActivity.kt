package com.onespace.companion

import android.app.Activity
import android.app.AlertDialog
import android.app.KeyguardManager
import android.content.Intent
import android.graphics.Color
import android.net.ConnectivityManager
import android.net.Uri
import android.os.Bundle
import android.provider.OpenableColumns
import android.text.InputType
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

/** Native storage UI; all Google credentials and resumable URLs stay on the Pi.
 * File bytes are streamed in bounded chunks only while this screen is unlocked.
 */
class HubCloudActivity : Activity() {
    private val worker = Executors.newSingleThreadExecutor()
    private val generation = AtomicInteger()
    @Volatile private var authorized = false
    @Volatile private var api: HubApi? = null
    @Volatile private var uploading = false
    private var revision = 0L
    private lateinit var root: LinearLayout
    private lateinit var status: TextView
    private lateinit var files: LinearLayout
    private lateinit var jobs: LinearLayout
    private lateinit var days: EditText
    private var nextPage = ""
    private var selected: Uri? = null
    private var selectedName = ""
    private var selectedSize = 0L
    private var bucket = "Temporary"
    private var recorder = JSONObject().put("bucket", "Temporary").put("temporaryDays", 7)
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
 window.addFlags(android.view.WindowManager.LayoutParams.FLAG_SECURE)
        authorized = HubUiSession.consume()
        revision = HubProfile(this).revision()
    }
    override fun onResume() { super.onResume(); if (authorized) showCloud() else showUnlock() }
    private fun column() = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
    private fun label(text: String, size: Float = 14f) = TextView(this).apply {
        this.text = text; textSize = size; setTextColor(Color.WHITE); setPadding(0, 12, 0, 12)
    }
    private fun button(text: String, action: () -> Unit) = Button(this).apply {
        this.text = text; isAllCaps = false; setOnClickListener { action() }
    }
    private fun field(hint: String, text: String = "") = EditText(this).apply {
        this.hint = hint; setText(text); setTextColor(Color.WHITE); setHintTextColor(Color.LTGRAY)
    }
    private fun scaffold(title: String) {
        root = column().apply { setPadding(24, 24, 24, 32); setBackgroundColor(Color.rgb(10, 16, 32)) }
        root.addView(label(title, 22f)); status = label("Ready"); root.addView(status)
        setContentView(ScrollView(this).apply { addView(root) })
    }
    private fun showUnlock() {
        scaffold("Cloud & recordings locked")
        root.addView(button("Unlock") {
            val keyguard = getSystemService(KEYGUARD_SERVICE) as KeyguardManager
            val intent = keyguard.createConfirmDeviceCredentialIntent("OneSpace cloud storage", "Confirm your Android screen lock")
            if (keyguard.isDeviceSecure && intent != null) startActivityForResult(intent, 900)
            else status.text = "Enable an Android screen lock first"
        })
    }
    private fun showCloud() {
        scaffold("Cloud & recordings")
        root.addView(label("Connect Google Drive on your Pi using DRIVE-SETUP.md. Only files created by OneSpace appear here. Google tokens stay on your Pi."))
        root.addView(button("Refresh status & files") { task { refreshStatus(it); refreshFiles(it, false); val account=it.request("/hub/v1/cloud/account"); ui { status.append("\nGoogle account: "+account.getJSONObject("user").optString("emailAddress")+" · Used "+account.getJSONObject("storageQuota").optString("usage")+" bytes") } } })
        val policy = button("Storage: $bucket") {}
        policy.setOnClickListener { bucket = if (bucket == "Temporary") "Permanent" else "Temporary"; policy.text = "Storage: $bucket" }
        root.addView(policy)
        days = field("Temporary days (1–365)", "7").apply { inputType = InputType.TYPE_CLASS_NUMBER }; root.addView(days)
        root.addView(label("Temporary files move to Drive Trash after expiry while the hub is online. Trash still uses space. Permanent files stay until deleted."))
        root.addView(button("Choose file to upload / resume") {
            startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply { type = "*/*"; addCategory(Intent.CATEGORY_OPENABLE) }, 700)
        })
        root.addView(label(if (selected != null) "Selected: $selectedName (${selectedSize / 1024} KiB)" else "No file selected"))
        root.addView(button("Upload selected file") { confirmTransfer { val uri = selected ?: throw IOException("Choose a file first"); upload(uri, null, policyDays(), bucket) } })
        root.addView(button("Pause upload") { api?.cancel(); generation.incrementAndGet(); status.text = "Pausing; refresh pending uploads to resume from verified progress" })
        val name = field("New text filename", "OneSpace-note.txt"); val text = field("Text file content")
        root.addView(name); root.addView(text)
        root.addView(button("Create text file in Drive") {
            val bytes = text.text.toString().toByteArray(); val filename = name.text.toString(); val count = try { policyDays() } catch (e: Exception) { status.text = e.message; return@button }
            if (bytes.isEmpty() || bytes.size > 65536) { status.text = "Enter 1–65536 bytes of text"; return@button }
            val selectedBucket = bucket
            task { client -> val j = client.request("/hub/v1/cloud/uploads", "POST", JSONObject().put("name", filename).put("size", bytes.size).put("bucket", selectedBucket).put("days", count)); client.cloudChunk("/hub/v1/cloud/uploads/${j.getString("id")}", bytes, 0); refreshStatus(client); refreshFiles(client, false) }
        })
        root.addView(label("Camera recording", 19f))
        root.addView(label("Uses the Pi camera encoder and short MP4 segments. A small SD buffer is required. Recording pauses if uploads fall behind; gaps can occur. Closing live view does not stop recording."))
        root.addView(button("Start recording to Drive") {
            val count = try { policyDays() } catch (e: Exception) { status.text = e.message; return@button }; val selectedBucket = bucket
            task { client -> client.request("/hub/v1/cloud/recording", "POST", JSONObject().put("enabled", true).put("bucket", selectedBucket).put("temporaryDays", count)); refreshStatus(client) }
        })
        root.addView(button("Stop recording") {
            val policyCopy = JSONObject(recorder.toString()).put("enabled", false)
            task { client -> client.request("/hub/v1/cloud/recording", "POST", policyCopy); refreshStatus(client) }
        })
        root.addView(label("Pending uploads", 19f)); jobs = column(); root.addView(jobs)
        root.addView(label("OneSpace files in Drive", 19f)); files = column(); root.addView(files)
        root.addView(button("Load more files") { if (nextPage.isNotEmpty()) task { refreshFiles(it, true) } })
        task { refreshStatus(it) }
    }
    private fun policyDays(): Int = days.text.toString().toInt().also { require(it in 1..365) { "Choose 1–365 temporary days" } }
    private fun confirmTransfer(action: () -> Unit) {
        if (uploading) { status.text = "An upload is already active"; return }
        val connection = getSystemService(CONNECTIVITY_SERVICE) as ConnectivityManager
        if (connection.isActiveNetworkMetered) AlertDialog.Builder(this).setTitle("Use mobile / metered data?")
            .setMessage("This sends the selected file through your Pi to Google Drive and may use significant data.")
            .setPositiveButton("Upload") { _, _ -> try { action() } catch (e: Exception) { status.text = e.message } }.setNegativeButton("Cancel", null).show()
        else try { action() } catch (e: Exception) { status.text = e.message }
    }
    private fun task(action: (HubApi) -> Unit) {
        val attempt = generation.get()
        worker.execute {
            var client: HubApi? = null
            try {
                if (!authorized || attempt != generation.get()) return@execute
                client = HubProfile(this).client(revision); api = client; action(client)
            } catch (e: Exception) { runOnUiThread { if (authorized && attempt == generation.get()) status.text = e.message ?: "Cloud operation failed; refresh to resume" } }
            finally { client?.cancel(); if (api === client) api = null }
        }
    }
    private fun ui(action: () -> Unit) {
        val attempt = generation.get()
        runOnUiThread { if (authorized && attempt == generation.get() && HubProfile(this).revision() == revision) action() }
    }
    private fun refreshStatus(client: HubApi) {
        val s = client.request("/hub/v1/cloud/status")
        ui {
            recorder = s.getJSONObject("recording")
            status.text = (if (s.optBoolean("credentialsPresent")) "Google credential present" else "Connect Google Drive on Pi first") +
                " · Recorder: " + (if (s.optBoolean("recordingActive")) "enabled; verify completed clips" else if (recorder.optBoolean("enabled")) "requested / paused" else "off") +
                " · ${s.optLong("spoolBytes") / 1048576} MiB buffered\n" + listOf("recordingError", "uploadError", "retentionError").map { s.optString(it) }.filter { it.isNotBlank() }.joinToString("\n")
            jobs.removeAllViews()
            val pending = s.optJSONArray("jobs") ?: JSONArray()
            for (i in 0 until pending.length()) {
                val j = pending.getJSONObject(i)
                jobs.addView(label("${j.optString("name")} · ${j.optLong("offset")} / ${j.optLong("size")} bytes"))
                if (j.optString("kind") == "file") {
                    jobs.addView(button("Resume ${j.optString("name")}") { confirmTransfer { val uri = selected ?: throw IOException("Choose the same original file first"); upload(uri, j, 7, j.getString("bucket")) } })
                    jobs.addView(button("Cancel pending upload") {
                        if (uploading) { status.text = "Pause the active upload first"; return@button }
                        AlertDialog.Builder(this).setMessage("Cancel this pending upload? A completed remote copy, if present, will move to Trash.")
                            .setPositiveButton("Cancel upload") { _, _ -> task { it.request("/hub/v1/cloud/uploads/${j.getString("id")}", "DELETE"); refreshStatus(it) } }.setNegativeButton("Keep", null).show()
                    })
                }
            }
        }
    }
    private fun refreshFiles(client: HubApi, more: Boolean) {
        val response = client.request("/hub/v1/cloud/files" + if (more && nextPage.isNotEmpty()) "?pageToken=${java.net.URLEncoder.encode(nextPage, "UTF-8")}" else "")
        ui {
            if (!more) files.removeAllViews()
            nextPage = response.optString("nextPageToken")
            val rows = response.getJSONArray("files")
            for (i in 0 until rows.length()) {
                val f = rows.getJSONObject(i); val id = f.getString("id")
                if (!id.matches(Regex("[A-Za-z0-9_-]{8,200}"))) continue
                val p = f.optJSONObject("appProperties") ?: JSONObject()
                files.addView(label("${f.optString("name")} · ${f.optString("size")} bytes\n${p.optString("bucket")} · ${p.optString("expiresAt")}"))
                files.addView(button("Open in Google Drive") { try { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://drive.google.com/file/d/$id/view"))) } catch (_: Exception) { status.text = "No browser installed" } })
                files.addView(button("Keep permanently") { changeRetention(id, "Permanent") })
                files.addView(button("Make temporary") { changeRetention(id, "Temporary") })
                files.addView(button("Move to Google Drive Trash") {
                    AlertDialog.Builder(this).setTitle("Move this file to Trash?").setMessage(f.optString("name"))
                        .setPositiveButton("Move to Trash") { _, _ -> task { it.request("/hub/v1/cloud/files/$id", "DELETE"); refreshFiles(it, false) } }.setNegativeButton("Cancel", null).show()
                })
            }
            if (files.childCount == 0) files.addView(label("No OneSpace files yet"))
        }
    }
    private fun changeRetention(id: String, value: String) {
        val count = try { policyDays() } catch (e: Exception) { status.text = e.message; return }
        task { it.request("/hub/v1/cloud/files/$id", "PATCH", JSONObject().put("bucket", value).put("days", count)); refreshFiles(it, false) }
    }
    private fun upload(uri: Uri, existing: JSONObject?, count: Int, selectedBucket: String) {
        require(!uploading) { "An upload is already active" }
        val name = selectedName; val size = selectedSize
        require(size > 0) { "File size unavailable; choose a local non-empty file" }
        require(existing == null || (existing.getString("name") == name && existing.getLong("size") == size)) { "Choose the same original file name and size" }
        uploading = true
        task { client ->
            try {
                var j = existing ?: client.request("/hub/v1/cloud/uploads", "POST", JSONObject().put("name", name).put("size", size).put("bucket", selectedBucket).put("days", count))
                val path = "/hub/v1/cloud/uploads/${j.getString("id")}"
                // A progress probe reconciles chunks committed before a timeout.
                val progress = try { client.request(path) } catch (e: Exception) {
                    val complete = try { client.request("/hub/v1/cloud/files/${j.getString("driveId")}").optString("size").toLongOrNull() == size } catch (_: Exception) { false }
                    if (complete) { refreshStatus(client); refreshFiles(client, false); return@task } else throw e
                }
                if (progress.optBoolean("complete")) { refreshStatus(client); refreshFiles(client, false); return@task }
                j = progress.getJSONObject("job")
                contentResolver.openInputStream(uri)?.use { input ->
                    var skip = j.getLong("offset")
                    while (skip > 0) { val n = input.skip(skip); if (n > 0) skip -= n else { if (input.read() < 0) throw IOException("Original file is shorter than expected"); skip-- } }
                    val buffer = ByteArray(1048576)
                    while (authorized && j.getLong("offset") < size) {
                        val offset = j.getLong("offset"); val length = minOf(buffer.size.toLong(), size - offset).toInt(); var n = 0
                        while (n < length) { val read = input.read(buffer, n, length - n); if (read < 0) throw IOException("Original file changed or is shorter than expected"); n += read }
                        val result = JSONObject(client.cloudChunk(path, if (n == buffer.size) buffer else buffer.copyOf(n), offset).body)
                        j = result.getJSONObject("job")
                        if (j.getLong("offset") != offset + n) throw IOException("Partial chunk acknowledged; refresh and resume from verified progress")
                        ui { status.text = "$name · ${j.getLong("offset") * 100 / size}% uploaded" }
                        if (result.optBoolean("complete")) break
                    }
                } ?: throw IOException("Cannot open original file")
                refreshStatus(client); refreshFiles(client, false)
            } finally { uploading = false }
        }
    }
    @Deprecated("Platform activity results avoid additional runtime dependencies")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == 900 && resultCode == RESULT_OK) { authorized = true; revision = HubProfile(this).revision(); return }
        if (requestCode == 700 && resultCode == RESULT_OK) {
            selected = data?.data; selectedName = ""; selectedSize = 0
            selected?.let { uri -> contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c -> if (c.moveToFirst()) { selectedName = c.getString(0) ?: "file"; selectedSize = if (c.isNull(1)) 0 else c.getLong(1) } } }
        }
    }
    override fun onPause() { uploading = false; authorized = false; generation.incrementAndGet(); api?.cancel(); super.onPause() }
    override fun onDestroy() { worker.shutdown(); super.onDestroy() }
}
