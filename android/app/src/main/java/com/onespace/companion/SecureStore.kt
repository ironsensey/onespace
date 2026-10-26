package com.onespace.companion

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** The device token is protected by a non-exportable Android Keystore key. */
class SecureStore(context: Context, preferenceName: String = "onespace",
                  private val alias: String = "onespace_device_token") {
    private val prefs = context.getSharedPreferences(preferenceName, Context.MODE_PRIVATE)
    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(alias,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256).build())
            generateKey()
        }
    }
    fun save(host: String, fingerprint: String, token: String) {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val encrypted = cipher.doFinal(token.toByteArray(Charsets.UTF_8))
        prefs.edit().putString("host", host)
            .putString("fingerprint", fingerprint)
            .putString("token", Base64.encodeToString(cipher.iv + encrypted, Base64.NO_WRAP)).apply()
    }
    fun host(): String = prefs.getString("host", "") ?: ""
    fun updateVerifiedHost(host: String) {
        require(paired()) { "Only paired devices can update the PC address" }
        require(host.matches(Regex("[0-9.]+"))) { "Expected LAN IP address" }
        prefs.edit().putString("host", host).apply()
    }
    fun fingerprint(): String = prefs.getString("fingerprint", "") ?: ""
    fun paired(): Boolean = prefs.contains("token")
    fun token(): String {
        val bytes = Base64.decode(prefs.getString("token", "") ?: "", Base64.DEFAULT)
        require(bytes.size > 12) { "Stored credentials missing" }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, bytes.copyOfRange(0, 12)))
        return cipher.doFinal(bytes.copyOfRange(12, bytes.size)).toString(Charsets.UTF_8)
    }
    fun clear() { prefs.edit().clear().apply() }
    fun preference() = prefs
}
