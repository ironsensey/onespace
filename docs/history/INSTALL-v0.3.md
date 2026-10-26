# Upgrade OneSpace from v0.2 to v0.3 (Windows + Android)

## 1. Back up existing Windows data

Exit OneSpace on Windows completely (ensure only one instance is running). Back up `%APPDATA%\OneSpace` or the custom directory you passed using `-data`. **Do not delete your original data folder**: it contains device pairing and certificate identity. Start `OneSpace-Windows-v0.3.exe` with the same data path. Visit `http://127.0.0.1:47600` on that PC.

## 2. Enable automatic Windows clipboard sharing

Go to **Settings → Privacy & performance → Automatic Windows clipboard sharing**. Switch it **ON** after considering whether your clipboard might contain passwords or other sensitive data. On the Devices page, make sure target devices are allowed **Files**, **Clipboard**, and **Camera** (as needed). Every device needs to be already paired.

## 3. Build the Android v0.3 APK

Unzip `OneSpace-v0.3-Android-Project.zip` and use the `android` folder. This project uses Android Gradle Plugin 8.13.0, Kotlin 2.2.20, and **Gradle 8.13** (NOT 9.6). It keeps applicationId `com.onespace.companion` and raises versionCode to **3**.

If you previously created a working `gradlew.bat` and Gradle wrapper `.jar`, you can copy those wrapper files from your **existing v0.2 Android project** into the new `android` folder. Confirm `gradle/wrapper/gradle-wrapper.properties` points to `gradle-8.13-bin.zip` (v0.3 ships this property file). Alternatively double-click `Setup-Gradle.bat` and allow it to generate them.

In PowerShell, while in the `android` folder, run:

```powershell
.\gradlew.bat --version
.\gradlew.bat :app:assembleDebug
```

You should see Gradle **8.13**, then `BUILD SUCCESSFUL`. Debug APK: `android\app\build\outputs\apk\debug\app-debug.apk`.

**Keep your signing identity:** A debug-signed app can be upgraded using the same debug keystore on the same computer. A release-signed installation must be updated using an APK signed with the **same original release keystore**. For release use Android Studio → Build → Generate Signed App Bundle / APK → APK → existing keystore → release. Do not send anyone your keystore or passwords.

## 4. Publish update from Windows

Windows OneSpace v0.3 → **Updates** → select the v0.3 APK, version name `0.3.0`, version code `3` → Publish. On your phone, open OneSpace → Updates → Check → Download → approve installation. If the installer reports a signature mismatch, do not uninstall; instead rebuild using your **original signing key** to preserve data.

## 5. Enable Android Camera + Inbox sync

After upgrading, open Android OneSpace → **Settings**. Enable **Foreground connection service**, **Automatic new camera photos**, **Receive shared files**, and **Receive Windows clipboard** (where offered). Grant **full Photos** access and notifications. On Xiaomi, visit Android Settings → Apps → OneSpace → Battery and choose a suitable background setting; permissions vary by model.

To test: take a **new** photo using your phone's usual Camera app. With Windows running on same LAN and Android's background service active, wait a few seconds, then try Ctrl+V in Paint. On another paired Android device, ensure background inbox is on; after the transfer, open OneSpace's notification to place the photo on its clipboard for paste. Note: Android clipboard access while another app is foregrounded is restricted by Android itself.

## 6. Troubleshooting

- PC isn't running, devices on different networks or firewall port 47601 blocked → the transfer won't reach Windows.
- New photos not sent: enable Full Photos permission, connected-device notification, Android background setting and check Windows → Devices permission Camera.
- Android incoming items don't appear: enable Inbox + background and make sure the other Android device is paired with the same Windows PC.
- Windows copies not sent: turn on the Windows clipboard toggle; supported text and DIB-based images only.
- System power and network measurements are not available yet; don't assume measured battery gains without testing.
