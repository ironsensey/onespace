import java.net.URI
import java.net.URL
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.Properties
import java.nio.ByteBuffer
import java.nio.channels.FileChannel
import java.nio.file.StandardOpenOption

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}
// Reserve one code per explicit APK/bundle build, not per IDE sync.
// Gaps after failed builds are intentional: a reserved code is never reused locally.
val versionFile = rootProject.file("version.properties")
val versionMetadata = Properties().apply { versionFile.inputStream().use { load(it) } }
val publishedFloor = versionMetadata.getProperty("publishedVersionCode", "9").toInt()
val minimumCode = versionMetadata.getProperty("versionCode", "10").toInt()
val explicitCode = providers.gradleProperty("onespaceVersionCode").orNull?.toInt()
val packagingRequested = gradle.startParameter.taskNames.any {
    it.substringAfterLast(':').let { task -> task.startsWith("assemble") || task.startsWith("bundle") || task.startsWith("install") }
}
fun reserveSharedCode(minimum: Long): Long {
    val address = System.getenv("ONESPACE_HUB_URL") ?: throw GradleException("Set ONESPACE_HUB_URL for shared version codes")
    val pin = System.getenv("ONESPACE_HUB_FINGERPRINT")?.lowercase() ?: throw GradleException("Set the verified hub fingerprint")
    val token = System.getenv("ONESPACE_HUB_TOKEN") ?: throw GradleException("Set a publisher-scoped hub token")
    val origin = URI(address)
    require(origin.scheme == "https" && origin.host != null && origin.userInfo == null && origin.path in listOf("", "/") && origin.query == null && origin.fragment == null)
    require(pin.matches(Regex("[a-f0-9]{64}")) && token.matches(Regex("[a-fA-F0-9]{64}")))
    val trust = object : javax.net.ssl.X509TrustManager {
        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        override fun checkClientTrusted(chain: Array<X509Certificate>, type: String) = throw CertificateException("Client trust unsupported")
        override fun checkServerTrusted(chain: Array<X509Certificate>, type: String) {
            if (chain.isEmpty()) throw CertificateException("Missing hub certificate")
            chain[0].checkValidity()
            val actual = MessageDigest.getInstance("SHA-256").digest(chain[0].encoded)
            val expected = pin.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
            if (!MessageDigest.isEqual(actual, expected)) throw CertificateException("Hub identity mismatch")
        }
    }
    val tls = javax.net.ssl.SSLContext.getInstance("TLS").apply { init(null, arrayOf<javax.net.ssl.TrustManager>(trust), null) }
    val connection = URL(address.trimEnd('/') + "/hub/v1/updates/reserve").openConnection() as javax.net.ssl.HttpsURLConnection
    try {
        connection.sslSocketFactory = tls.socketFactory
        connection.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, _ -> true }
        connection.instanceFollowRedirects = false
        connection.connectTimeout = 4000
        connection.readTimeout = 12000
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Authorization", "Bearer $token")
        connection.setRequestProperty("Content-Type", "application/json")
        connection.outputStream.use { it.write("""{"minimumCode":$minimum}""".toByteArray()) }
        if (connection.responseCode != 201) throw GradleException("Pi version reservation failed; code was not reused")
        val bytes = connection.inputStream.use { it.readNBytes(4097) }
        require(bytes.size <= 4096)
        val result = groovy.json.JsonSlurper().parseText(String(bytes, Charsets.UTF_8)) as Map<*, *>
        return (result["versionCode"] as Number).toLong()
    } finally { connection.disconnect() }
}
val buildVersionCode = if (packagingRequested || explicitCode != null) {
    val counter = rootProject.file(".onespace/version-counter")
    counter.parentFile.mkdirs()
    FileChannel.open(counter.toPath(), StandardOpenOption.CREATE, StandardOpenOption.READ, StandardOpenOption.WRITE).use { channel ->
        channel.lock().use {
            val bytes = ByteBuffer.allocate(channel.size().toInt())
            while (bytes.hasRemaining()) channel.read(bytes)
            val previous = bytes.array().toString(Charsets.UTF_8).trim().takeIf { it.isNotEmpty() }?.toLong() ?: 0L
            val localNext = maxOf(previous, publishedFloor.toLong(), minimumCode.toLong() - 1L) + 1L
            val next = explicitCode?.toLong() ?: if (System.getenv("ONESPACE_SHARED_VERSION_CODES") == "true") reserveSharedCode(localNext) else localNext
            require(next > publishedFloor && next >= minimumCode && next <= 2100000000L && next >= previous) {
                "Version code must exceed the published floor, meet the project minimum, and not regress the local counter"
            }
            channel.position(0); channel.truncate(0)
            val output = ByteBuffer.wrap(next.toString().toByteArray())
            while (output.hasRemaining()) channel.write(output)
            channel.force(true)
            next.toInt()
        }
    }
} else minimumCode
logger.lifecycle("OneSpace ${versionMetadata.getProperty("versionName")} (versionCode $buildVersionCode)")

android {
    namespace = "com.onespace.companion"
    compileSdk = 36
    defaultConfig {
        applicationId = "com.onespace.companion"
        minSdk = 29
        targetSdk = 35
        versionCode = buildVersionCode
        versionName = versionMetadata.getProperty("versionName", "0.6.2")
    }
    buildTypes { release { isMinifyEnabled = false } }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}

dependencies { implementation("com.squareup.okhttp3:okhttp:4.12.0"); testImplementation("junit:junit:4.13.2") }
