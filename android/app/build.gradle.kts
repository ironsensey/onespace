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
val buildVersionCode = if (packagingRequested || explicitCode != null) {
    val counter = rootProject.file(".onespace/version-counter")
    counter.parentFile.mkdirs()
    FileChannel.open(counter.toPath(), StandardOpenOption.CREATE, StandardOpenOption.READ, StandardOpenOption.WRITE).use { channel ->
        channel.lock().use {
            val bytes = ByteBuffer.allocate(channel.size().toInt())
            while (bytes.hasRemaining()) channel.read(bytes)
            val previous = bytes.array().toString(Charsets.UTF_8).trim().takeIf { it.isNotEmpty() }?.toLong() ?: 0L
            val next = explicitCode?.toLong() ?: (maxOf(previous, publishedFloor.toLong(), minimumCode.toLong() - 1L) + 1L)
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

dependencies { testImplementation("junit:junit:4.13.2") }
