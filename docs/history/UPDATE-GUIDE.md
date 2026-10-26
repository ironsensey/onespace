# OneSpace update system — Windows → Android (preview)

This revision **adds an updater to the Android source app and a Windows Update Center**. It does not contain a compiled Android APK. Android SDK/Gradle and a real phone were unavailable in the build environment, so Android compilation, permission prompts and installation are **not yet tested**. The Windows executable compiles and update API tests run successfully.

## The key rule: install the updater-equipped Android build first

A previously installed version of OneSpace without an updater cannot check for updates. Before installing your *first* OneSpace Android APK, build the Android project supplied **in this archive**, not an earlier version.

### Initial Android install

1. Extract this archive. Open its `android/` folder in Android Studio, not the outer ZIP or root folder. Resolve Gradle sync and Android SDK downloads; the included `android/Setup-Gradle.bat` can generate a Gradle wrapper using Java and the official Gradle download.
2. Open `android/app/build.gradle.kts`. Do not change its `applicationId` (`com.onespace.companion`). The initial updater-enabled build uses `versionCode = 1`.
3. In Android Studio select **Build → Generate Signed App Bundle / APK → APK** and create a strong, private release-signing keystore (`.jks`). Back up the key AND its passwords somewhere safe. Do not put the keystore inside the source/project ZIP and do not upload it anywhere.
4. Select release and generate the APK; install this release-signed APK on Android. Android may require explicit permission to install the APK from your chosen source.
5. Pair the Android app with the updated Windows executable on the same trusted network.

The app's **OneSpace Updates** section contains **Check for Android app updates** and **Install downloaded update**. On opening the unlocked workspace, it also performs one lightweight check per app session. It doesn't run an always-on background update service.

### Publish a new *complete* Android update

1. Modify the app source. Increment `versionCode` from `1` to `2` (then 3, 4, and so on) in `android/app/build.gradle.kts`. Update `versionName` to match what you want users to see (for example `0.2.0`).
2. Build a **release APK signed with the EXACT SAME app-signing key** as the initial APK.
3. Stop the old Windows `OneSpace-Windows.exe` before launching the one included in this archive (both listen on ports 47600 and 47601).
4. On the Windows dashboard at `http://127.0.0.1:47600`, open **Updates**. Enter the *APK's real* versionCode and versionName. Select the signed `.apk`. Click **Publish Android update**.
5. Android must be on the same private network and paired with the updated Windows service. Open OneSpace and unlock, or manually tap **Check for Android app updates**.
6. When prompted, download the new full APK. The companion verifies the announced SHA-256 checksum, the internal package ID, versionCode, and APK signer against the currently installed app. Choose **Install downloaded update** and approve the Android installation prompt. If Android opens **Install unknown apps** settings, allow OneSpace if you trust the update, return to the app, then tap Install downloaded update again.
7. Android's package installer makes the final decision about signing identities and compatibility. It won't install a differently signed build as an upgrade.

### Windows updates

The current Update Center **distributes Android APKs only**. It does not yet self-update the Windows executable. For this version, replace/restart the Windows executable manually after closing the old instance. Your OneSpace Windows data is stored in the application's data directory independently from the executable.

### What the update center can and cannot do

- ✅ Publish a complete APK from the trusted Windows dashboard; serve to authorized, paired phones over HTTPS pinned to the PC's certificate.
- ✅ Verify encrypted download and full-file checksum; compare Android package identity and signer before launching the installer.
- ✅ Automatically *check* for newer versions when an unlocked app workspace is opened, with a manual check button.
- ✅ Keep existing Android app data for correctly signed, same-application-ID upgrades, provided future database migrations remain compatible.
- ❌ Cannot silently install code or bypass Android security prompts.
- ❌ Cannot update Android while the Windows PC is offline or on a different network (optional internet distribution is for a later release).
- ❌ Cannot guarantee Android installation until built and tested on an actual phone.
- ❌ Cannot guarantee zero data loss for every future version; test upgrades and migrations, and back up important files.

**Keep your signing key private.** The Windows dashboard accepts an APK and publishes its hash; it cannot prove an APK is properly signed at upload time. Android performs checks before presenting installation. Publish only releases you've built and reviewed yourself. Never publish arbitrary APKs from strangers.
