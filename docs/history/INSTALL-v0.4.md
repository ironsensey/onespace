# OneSpace v0.4 — Upgrade without losing pairing

## 1. Windows: back up and replace

1. Close all old OneSpace Windows processes (v0.3 and earlier) before running v0.4. Only one copy may listen on port 47601.
2. Back up your existing OneSpace data folder (usually `%APPDATA%\OneSpace`; if you previously used the `-data` option, back up that custom folder instead). Do not erase it; it includes encrypted service identity, device pairing and workspace state.
3. Run `OneSpace-Windows-v0.4.exe`. Use the same `-data` argument as your old version, if applicable.
4. Open `http://127.0.0.1:47600`. In Settings, turn on **Automatic Windows clipboard sharing** if you want it; this shares your copied text and supported images with authorized paired devices.

## 2. Android: compile v0.4

1. Extract the v0.4 Android Studio project. Open its `android` directory in Android Studio, or open a PowerShell window inside that `android` directory.
2. The correct Gradle version is **8.13**, not 9.6. The project has `gradle/wrapper/gradle-wrapper.properties` pointing to the Gradle 8.13 distribution.
3. If `gradlew.bat` exists, run `./gradlew.bat :app:assembleDebug` in PowerShell (on Windows, use `.\gradlew.bat :app:assembleDebug`). If `gradlew.bat` is missing, run `.\Build-OneSpace-Debug.bat` in the same `android` directory. The helper downloads/generates the Gradle 8.13 wrapper and compiles the APK.
4. The **debug APK** is at `android\app\build\outputs\apk\debug\app-debug.apk`.
5. **Update signing:** If v0.3 installed on your phone was signed with your own release `.jks`, do **not** install the debug APK. In Android Studio, use Build → Generate Signed Bundle / APK → APK → select your **original** release key → build release. Every update must use the same application ID and compatible signing identity, with a higher versionCode. v0.4 is version code **4**. Keep your keystore private and backed up.

## 3. Publish and install v0.4

1. In Windows OneSpace v0.4 → Updates, upload the newly signed v0.4 APK with **version code 4** and **version name 0.4.0**.
2. On Android OneSpace v0.3 → Updates → Check for updates → Download, then approve installation. Keep both devices on the same local network. If there is a signature mismatch, rebuild using the old signing key; **do not uninstall** unless you are ready to lose the app's private data.
3. If Windows cannot reach your phone, check the paired devices, PC IP, Windows firewall for TCP 47601 and that both devices are on a reachable local network.

## 4. Enable the fast background features

- Windows: Settings → Automatic Windows clipboard sharing **on**. Windows Devices → grant Files/Clipboard/Camera for the intended phone/tablet as needed.
- Android: Settings → Foreground connection service **on**, Receive shared files/inbox **on**, and Receive copied text/photos **on**. Camera Sync → Send new camera photos automatically **on**. Give **full Photos** access and approve relevant notifications.
- On Xiaomi/other Android devices, check OneSpace's battery/background restrictions if photo detection stops; even then Android may shut down the service.
- Take a **new** photo in the standard camera app, not an existing photo, while devices are paired on reachable Wi-Fi. Arrival can still take seconds depending on photo size and network.
- With OneSpace in the Android background, clipboard-received text/images are **saved**. Tap the received-item notification/open OneSpace to put the item on the Android paste clipboard. Android forbids fully automatic background clipboard writing in many situations.

## 5. Quick speed test

- On Windows, enable clipboard sync and copy a new short text string. Android should receive it into OneSpace quickly when service is running. Open its incoming notification for Android paste.
- Take a new photo with the phone camera and paste into Windows Paint with Ctrl+V once transfer finishes. A 10+ MB photo can take longer than a text snippet; photo completion and network throughput are relevant.
- If an item still takes 30–60 seconds, verify **both** Windows and Android were updated to v0.4; an old Windows server does not support long polling and an old Android app still fetches on the 60-second timer.
- For reliable measurement, test 10 text copies and 5 photos on your actual LAN, and note file size, elapsed time, Wi-Fi signal and Xiaomi battery policy. CPU/battery targets should be based on these measurements, not assumptions.
