# Build OneSpace v0.6.2 in Android Studio

## Keep your existing data and signing identity

Do not uninstall your existing Android OneSpace app. An in-place upgrade requires the same `com.onespace.companion` application ID and the same signing certificate as the installed app. Use your original release keystore and alias. If your installed app is a debug build, build on the machine with its original debug keystore. A newly generated debug key cannot update it.

The ZIP contains source and a Windows executable. It does not contain your private signing key or a prebuilt Android upgrade APK. The cloud debug APK was used only for validation and is deliberately excluded.

## 1. Extract and open the Android project

1. Extract `OneSpace-v0.6.2-Clipboard-Fix.zip` to a normal, writable folder, such as `C:\Projects`.
2. Install/open a current stable **Android Studio**.
3. Choose **Open** and select `OneSpace-v0.6.2-Clipboard-Fix\android`, which contains `settings.gradle.kts`. Do not open the `app` subfolder alone.
4. Let Gradle sync finish. This project includes the Gradle wrapper, pinned to **8.13** with its distribution checksum. Do not replace it with Gradle 9.
5. In **Tools → SDK Manager**, install **Android SDK Platform 36**. Under **SDK Tools**, enable **Show Package Details** and install **Android SDK Build-Tools 35.0.0**, Android SDK Platform-Tools and Android SDK Command-line Tools.
6. In **Settings → Build, Execution, Deployment → Build Tools → Gradle**, choose Android Studio's embedded JDK/JBR 21, or a full JDK 17/21. A Java runtime without `javac` is insufficient. The application's Java/Kotlin bytecode target remains 17.

Android Studio creates your machine-specific `local.properties`. This file is not shipped. The project retains minSdk 29, targetSdk 35, and compileSdk 36.

## 2. Set the known published version floor

Before making an APK intended for publication, open Android Studio's Terminal in the `android` folder:

```powershell
.\Prepare-Release.ps1
```

This reads the already running local Windows OneSpace publisher and records its latest APK code. It does not print or persist the dashboard's local authentication key.

If that PC is offline or has no published manifest, supply the highest Android code already installed/published in your ecosystem:

```powershell
.\Prepare-Release.ps1 -PublishedVersionCode 9
```

Use `9` only if 9 is actually your highest existing release. If Windows Updates shows a higher code, supply that higher number. If PowerShell prevents local scripts from running, follow your machine's script policy or edit only `publishedVersionCode` in `version.properties` to the verified value.

The app's version name is **0.6.2**, its minimum code is **10**, and each explicit assemble/bundle/install build automatically reserves the next local code in `.onespace/version-counter`. IDE sync does not increment it. Failed builds can leave gaps; gaps are safe. Never delete the counter to force a smaller code. Preserve it when continuing development on the same project. After copying to a new machine, refresh the published floor before release builds.

This local allocator is not a distributed release service. If several developers/CI jobs publish independently, use one shared release-code authority and pass its reserved code using `-PonespaceVersionCode=N`. Explicit codes must not regress the local counter or known published floor. The Windows publisher still rejects duplicate/lower codes.

## 3. Build an APK

For an upgrade to a release-signed installation:

1. Choose **Build → Generate Signed App Bundle / APK**.
2. Select **APK**, then **Next**.
3. Select your **existing** keystore, key alias and passwords. Do not create a replacement key for an existing installed app.
4. Choose the `release` variant and complete the wizard.
5. Use the APK at the destination shown by Android Studio. The usual Gradle location is `app\build\outputs\apk\release\app-release.apk`, but the signing wizard may use the destination you selected.

For development/debug testing:

```powershell
.\gradlew.bat :app:assembleDebug
```

Debug APK: `app\build\outputs\apk\debug\app-debug.apk`. It can upgrade an existing app only when the debug signing certificate matches. The **Run** button installs a debug variant on a selected emulator/phone; do not use it to overwrite an existing release installation with an incompatible key.

To validate the project:

```powershell
.\gradlew.bat :app:testDebugUnitTest :app:lintDebug
```

## 4. Update Windows and publish your signed APK

1. Fully exit the old Windows OneSpace process. Closing its dashboard window alone does not stop it; check Task Manager if ports 47600/47601 are occupied.
2. Back up the entire existing `%AppData%\OneSpace` directory, or your custom `-data` directory. Do not delete or regenerate the certificates or state file.
3. Run the included `OneSpace-Windows-v0.6.2.exe` from a stable location on Windows 10/11. It is a cross-built developer executable, not a signed production installer.
4. Keep the same data directory. Confirm the dashboard displays **v0.6.2** and your existing paired devices remain listed.
5. Allow TCP 47601 and optional UDP 47602 only on your trusted Private network. Keep your firewall enabled.
6. In Windows **Updates**, choose your correctly signed APK and publish it. Windows reads the actual name/code from the APK; do not rename an old APK to pretend it is newer.
7. On Android, open OneSpace → **Updates → Check for an update**, download, then choose **Install downloaded update** and approve Android's installer. If Android requests install permission, grant it only if you trust this build, return and tap Install again.
8. Verify the installed version, pairing, settings and saved files after the upgrade. If signing verification fails, rebuild with the original key; do not uninstall to bypass the check.

## 5. Enable and test Windows → Android clipboard reception

1. In Windows **Settings**, enable **Automatic Windows clipboard sharing**.
2. In Windows **Devices**, confirm the phone's **File transfer & inbox**, **Clipboard text**, and **Camera to clipboard** permissions are enabled as appropriate.
3. On Android, unlock OneSpace, then enable **Receive Windows clipboard** on its Clipboard page. This starts the visible background connection service; allow notifications when asked. A separate Inbox toggle is no longer required just to receive copied text/photos.
4. Keep both devices on a reachable local Wi-Fi/Ethernet network. Bring OneSpace to the foreground on Android to activate received content for paste.
5. Copy small text on Windows, then paste into an Android app after opening/unlocking OneSpace. Repeat with a supported image. While a large photo/file is transferring, copy new text and verify it arrives through the independent text lane.
6. Compare ten text copies and several images before/after. Note file sizes and delays. Test manual Fetch Inbox while background reception is active; it should not create a second copy of the same delivered job.

Android 10+ limits reading other apps' clipboard in the background. This app intentionally activates incoming clipboard content only while OneSpace is focused/unlocked. Receiving into Downloads and preparing the global clipboard are different steps. Android/OEM battery policy, force-stop, permissions, camera finalization and Wi-Fi throughput still affect delivery. No zero-delay or permanently running background-service promise is made.

This package passed automated build/test checks described in `VALIDATION-v0.6.2.md`; native Windows clipboard, real-phone battery/reconnection and same-key upgrade tests remain necessary before treating it as a production release.
