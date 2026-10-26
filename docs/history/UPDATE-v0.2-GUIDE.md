# OneSpace v0.2 — Update from an existing paired installation

This package contains Windows v0.2 EXE plus Android Studio source (not a compiled Android APK).

## First: back up

1. Stop the current OneSpace Windows EXE. Copy its **whole** data folder (typically `%APPDATA%\OneSpace`) to a private backup location. It holds TLS identity, pairing tokens, settings, and shared files. Do **not** post the backup or signing key online.
2. Do not uninstall OneSpace Android before trying an in-place update. Uninstalling may delete pairing information.
3. Keep the original Android `.jks` release signing key and password backed up securely.

## Windows v0.2

1. Close the old Windows OneSpace process completely (not just its browser window).
2. Copy `OneSpace-Windows-v0.2.exe` to a stable location, e.g. `C:\Program Files\OneSpace\` where you have permission, or `%LOCALAPPDATA%\Programs\OneSpace\`.
3. Run it. Windows may request firewall permission for **Private networks**; approve only if you trust this build.
4. The UI should open in an app-style Microsoft Edge window if Edge is installed; otherwise your browser opens. Its background connectivity engine is still a separate executable, not a Windows service or fully native GUI.
5. Visit the **Devices** page to see the new server-enforced permission switches for each paired device.
6. **Settings → Launch at Windows login** enables auto-start for your Windows user. Auto-start must point to a stable EXE location (not Downloads).
7. Your existing pairing and certificate fingerprint should stay the same **if you reused the same data folder**.

### Windows folder locations

- Actual app data location shown in **Settings → App data & shared folder**.
- Permanent and Temporary live inside that folder.
- To change the whole data directory safely, **fully quit the app**, copy the **entire** folder to the new absolute path, then launch `OneSpace-Windows-v0.2.exe -data "D:\\YourChosenOneSpaceFolder"` with that new path. Keep the original as backup until confirmed. Changing the path inside the dashboard with a native folder picker is **not implemented yet**.

## Android v0.2 build and upgrade

Open the extracted `android` directory with Android Studio. The Android package ID is unchanged:

- `applicationId`: `com.onespace.companion`
- `versionCode`: **2**
- `versionName`: **0.2.0**

This is necessary for an in-place APK update.

### If your current phone app is a signed **release APK**

1. In Android Studio: Build → Generate Signed App Bundle / APK → APK.
2. Choose **Existing** keystore. Select the exact same `.jks` and key alias you used for v0.1. **Do not create a new signing key.**
3. Choose `release`, build, and locate the `app-release.apk`.
4. From the new Windows OneSpace: **Updates → Publish an Android update**; choose the APK and specify code **2** / name **0.2.0**.
5. On the phone in the current OneSpace app: **Updates → Check for an update → Download → Install**. Accept Android's installer prompt.
6. Verify your existing pairing, tasks, files and fingerprint are intact.

### If your current phone app is a **debug APK**

1. Run `android\Build-OneSpace-Debug.bat`, or run `./gradlew.bat :app:assembleDebug` from the Android folder. It might need Gradle wrapper setup and Android SDK.
2. `android\app\build\outputs\apk\debug\app-debug.apk` should be produced.
3. **Only if using the same computer/debug keystore** as v0.1, the debug v0.2 APK can update it in place. You can publish this debug v0.2 APK through the Windows updater for testing, using code 2/name 0.2.0.
4. A **release-signed** APK cannot replace a **debug-signed** APK unless its signing identity matches. To switch from debug to release, preserve or export your important data first, then uninstall/reinstall as needed.

Never send `.jks`, signing passwords, or OneSpace Windows data directory to anyone for troubleshooting.

## Phone: feature switches

On Android v0.2: **Settings** (themes, clipboard, background) and **Camera** (auto-photo, share now) and **Files** (Android `Downloads/OneSpace/Permanent`, `Downloads/OneSpace/Temporary`) and **Devices** (trusted host details) and **Updates**.

**Camera automatic sync** is opt-in and requires full media access and a foreground service. Android 14/15+ and Xiaomi battery management can interrupt it. You may need Android Settings → Apps → OneSpace → Battery → Unrestricted for the best possible reliability, but even then there is no guarantee of running at all times. A persistent notification indicates when OneSpace's foreground service is active. No boot-start service is included.

**Clipboard limitation:** Android 10+ normally allows clipboard reading only while the app is focused. The Android foreground clipboard switch enables automatic **text** sync during app use; it cannot lawfully bypass OS restrictions to monitor all Android clipboard changes while another app is open.

**Multiple devices:** Android-originated shared clipboard text/photos are routed by the Windows hub to other paired devices' inbox. A receiving phone needs background Inbox turned on or manual **Fetch Inbox**. Received images can be copied to the Android clipboard while OneSpace is focused; background insertion is not guaranteed.

## Tests / what is still missing

Windows v0.2 was cross-compiled and all available Go tests passed. Dashboard JavaScript syntax was checked. Android v0.2 source was created here, but could not be compiled because the environment lacks an Android SDK; you must compile and test it on your PC and phone before treating it as stable.

Not yet implemented: fully native Windows UI, visual Windows folder picker/migration, always-on Android clipboard, guaranteed permanent Android service, complete cross-device image clipboard in background, Windows automatic clipboard-change broadcasting, remote Android power off/reboot, Bluetooth handoff, full automation engine and additional dedicated Android widgets.
