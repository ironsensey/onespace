# Install OneSpace v0.5

## Before updating

- Keep Windows and Android on the same reachable LAN (not guest Wi-Fi).
- Make a backup of the Windows OneSpace state folder if you rely on its history and pairing. The default is under your Windows user configuration directory, usually `%AppData%\OneSpace`, unless you started the program with `-data`.
- The Android app keeps the same application ID: `com.onespace.companion`.
- Android version code is **5**, version name **0.5.0**.
- Sign with the **same key** as the currently installed version. A debug build will not update an app signed with your private release key.

## Windows

1. Exit the older OneSpace process fully (not just the browser window).
2. Run `OneSpace-Windows-v0.5.exe` from this package. Allow access on trusted Private networks if Windows asks; never disable the firewall.
3. Make sure **Automatic Windows clipboard sharing** is enabled in OneSpace settings.
4. Use the previously paired trusted Android devices. Don't clear or regenerate the Windows data folder.

## Android (debug testing)

1. Extract `OneSpace-v0.5-Android-Project.zip` or use the `android` folder in the complete update.
2. On Windows, open a terminal in the `android` folder and run `./Build-OneSpace-Debug.bat` (or double-click it). This script uses the Gradle **8.13** wrapper and, if missing, attempts to create it with `Setup-Gradle.bat` (internet needed once).
3. If you already have a working Gradle wrapper, run `./gradlew.bat :app:assembleDebug`.
4. Output: `android\app\build\outputs\apk\debug\app-debug.apk`.
5. Install directly over the old *debug-signed* app. If using the Windows Updates dashboard, publish version code `5` / version name `0.5.0` and select this APK.

## Android (release signed)

Use Android Studio: **Build → Generate Signed App Bundle / APK → APK**, select the existing signing keystore and alias, choose the release variant, and build. Use the **same signing key** as the installed release app. Do not send or upload your private signing key.

## Turn on automatic functions

- Android: OneSpace → Settings/Background → enable background connectivity and Camera Sync; allow full media access as supported on your Android version. If your phone offers per-app background restrictions, configure OneSpace appropriately.
- Windows: enable automatic clipboard sharing.
- Android clipboard while other apps are open is still restricted by the OS. Use the Android Share action or bring OneSpace to the foreground where needed.

## Quick latency test

1. With Windows and Android awake on the same Wi-Fi, copy a short text while OneSpace Android is open; record seconds to appear on Windows.
2. Take a *new* photo in your regular Android Camera app; record from shutter press until Ctrl+V works in Paint on Windows. Repeat three times.
3. If there's a major delay, check Windows console/logs and Android Studio Logcat filtered by `OneSpace` for camera upload duration or connection failures. Do not share text/photograph contents or authentication tokens.

If the build fails due to Gradle 9.6 compatibility, set `android/gradle/wrapper/gradle-wrapper.properties` distributionUrl to `https\://services.gradle.org/distributions/gradle-8.13-bin.zip`, then run `gradlew.bat --version` and rebuild. Do not run Gradle 9.6 with the existing Android Gradle plugin.
