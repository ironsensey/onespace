package com.onespace.companion

import android.app.Activity
import android.content.ClipData
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.Settings
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.security.MessageDigest

/** Full-APK updater; nothing is installed automatically. The Android installer
 * makes the final signature decision and shows its own system confirmation. */
class OneSpaceUpdater(private val activity: Activity) {
    data class Release(val versionCode: Long, val versionName: String, val size: Long, val sha256: String)
    private val apkFile: File get() = File(File(activity.cacheDir, "updates"), "OneSpace-update.apk")
    private val maxSize = 150L * 1024L * 1024L
    private val pm get() = activity.packageManager
    private val myPackage get() = activity.packageName

    fun check(api: UpdateSource): Release? {
        val data = try {api.request("/v1/update/manifest")} catch (e: IOException) {
            if (e.message?.contains("No Android update has been published yet") == true) return null
            throw e
        }
        if (data.getString("packageName") != myPackage) throw IOException("Update belongs to a different application")
        val code = data.getLong("versionCode")
        if (code <= currentCode()) return null
        val size = data.getLong("size")
        val digest = data.getString("sha256")
        if (size <= 0 || size > maxSize || !digest.matches(Regex("[a-fA-F0-9]{64}")))
            throw IOException("Invalid update metadata")
        if (data.getString("downloadPath") != "/v1/update/apk") throw IOException("Unexpected update download URL")
        return Release(code, data.getString("versionName"), size, digest.lowercase())
    }

    private fun currentCode(): Long = pm.getPackageInfo(myPackage, 0).longVersionCode

    fun download(api: UpdateSource, release: Release) {
        apkFile.parentFile?.mkdirs()
        val temp = File(apkFile.parentFile, "pending.apk")
        try {
            val md = MessageDigest.getInstance("SHA-256")
            var written = 0L
            api.downloadUpdate { input ->
                temp.outputStream().buffered().use { output ->
                    val buf = ByteArray(65536)
                    while (true) {
                        val n = input.read(buf)
                        if (n < 0) break
                        written += n
                        if (written > maxSize || written > release.size) throw IOException("Update larger than advertised")
                        md.update(buf, 0, n)
                        output.write(buf, 0, n)
                    }
                }
            }
            val digest = md.digest().joinToString("") { "%02x".format(it.toInt() and 0xff) }
            if (written != release.size || !digest.equals(release.sha256, ignoreCase=true))
                throw IOException("APK integrity verification failed")
            // Compare the package and signing identity of the archive to the
            // installed OneSpace app BEFORE offering installation.
            @Suppress("DEPRECATION")
            val archive = pm.getPackageArchiveInfo(temp.absolutePath, PackageManager.GET_SIGNING_CERTIFICATES)
                ?: throw IOException("File is not a valid Android APK")
            if (archive.packageName != myPackage || archive.longVersionCode != release.versionCode || archive.longVersionCode <= currentCode())
                throw IOException("APK package/version does not match the published release")
            @Suppress("DEPRECATION")
            val installed = pm.getPackageInfo(myPackage, PackageManager.GET_SIGNING_CERTIFICATES)
            val archiveSigners = archive.signingInfo?.apkContentsSigners?.map { it.toByteArray().toList() }?.toSet()
            val installedSigners = installed.signingInfo?.apkContentsSigners?.map { it.toByteArray().toList() }?.toSet()
            if (archiveSigners.isNullOrEmpty() || installedSigners.isNullOrEmpty() || archiveSigners != installedSigners)
                throw IOException("APK signing certificate differs from installed OneSpace. Update rejected.")
            if (apkFile.exists() && !apkFile.delete()) throw IOException("Cannot replace cached update")
            if (!temp.renameTo(apkFile)) throw IOException("Unable to stage verified update")
        } finally { temp.delete() }
    }

    /** A cached APK is not the installed version. Discard stale downloads and
     * allow installation only if its version is newer than the app actually running. */
    fun cachedVersion(): Long? {
        if (!apkFile.isFile) return null
        @Suppress("DEPRECATION")
        val archive = pm.getPackageArchiveInfo(apkFile.absolutePath, 0)
        val code = archive?.longVersionCode
        if (archive?.packageName != myPackage || code == null || code <= currentCode()) {
            apkFile.delete()
            return null
        }
        return code
    }
    fun isCached(release: Release): Boolean = cachedVersion() == release.versionCode
    fun isDownloaded() = cachedVersion() != null

    /** Returns false if Android needs permission first. User must opt in. */
    fun launchInstaller(): Boolean {
        if (cachedVersion() == null) throw IOException("No newer downloaded APK. Check for updates first.")
        if (Build.VERSION.SDK_INT >= 26 && !pm.canRequestPackageInstalls()) {
            val settings = Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                Uri.parse("package:$myPackage"))
            activity.startActivity(settings)
            return false
        }
        val uri = Uri.parse("content://$myPackage.updates/apk")
        val view = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            clipData = ClipData.newUri(activity.contentResolver,"OneSpace Android update",uri)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        activity.startActivity(view)
        return true
    }

    // Used only by OneSpaceUpdateProvider, accessible by the OS installer via
    // the single granted content URI.
    fun cachedApkForProvider(): File = apkFile
}
