# OneSpace v0.6 — upgrade instructions

**v0.6 Android build:** application ID `com.onespace.companion`, versionCode `6`, versionName `0.6.0`.

## 1. Windows

1. Exit the *old* OneSpace Windows process completely. Closing its browser-like window may leave the service running; check Task Manager if port 47600 is still held.
2. Back up the entire `%AppData%\OneSpace` directory, unless you've specified a different `-data` path. It includes stored pairing identities and workspace data; **do not delete it**.
3. Run `OneSpace-Windows-v0.6.exe` from this package. Check that the left sidebar says v0.6.0. If it still says v0.4, an older server is still running.
4. Keep automatic clipboard sharing on if you want PC copies sent to Android. Review each device's Clipboard and Camera permissions under Devices.
5. To allow *automatic IP recovery*, allow OneSpace on **private networks** in the Windows firewall. The discovery service uses UDP **47602**, while encrypted pairing/transfers use TCP **47601**. Never turn off your firewall entirely or open these services to the public internet. If discovery is blocked, manually update the PC IP on Android (or pin the PC DHCP lease on your router).

## 2. Android — building the updated APK

1. Extract the `OneSpace-v0.6-Android-Project.zip` and open the `android` directory in Android Studio, or open PowerShell inside that folder.
2. Run `./Build-OneSpace-Debug.bat` **as ` .\Build-OneSpace-Debug.bat` in PowerShell**. This uses Gradle 8.13; do **not** regenerate with Gradle 9.6.
3. After `BUILD SUCCESSFUL`, the debug APK is `android\app\build\outputs\apk\debug\app-debug.apk`.
4. You can update an installed **debug** version only if it was signed with the same debug certificate. If your current app is release-signed, use **Build → Generate Signed Bundle / APK → APK** in Android Studio and **your existing release .jks key**. Never share your key or passwords.
5. With the Windows v0.6 app running, go to Updates → select your built APK → Publish. **Version code and name are now read automatically from the APK.** The publisher does not accept unrelated application IDs. In the installed Android OneSpace, go to Updates → Check → Download (if not already downloaded) → Install and approve Android's install prompt.
6. Do not uninstall the existing app to update unless you consciously accept the loss of app data. First back up important content.

## 3. Turn on automatic functions

- Android → Settings: opt in to Background connection and Automatic camera sync; allow full photo access and notification permissions as appropriate. A visible foreground notification should indicate active sync.
- On Android 15 and heavily customized phones, check manufacturer power management if syncing stops. Android may still suspend/restrict background services.
- Camera-to-PC images should become pasteable on Windows after file finalization and transfer. This isn't guaranteed instantaneous for large photos.
- Android's system clipboard may not be readable while OneSpace is in the background. This is an Android restriction, not an app toggle.

## 4. Test after updating

1. Confirm *Windows* sidebar says `v0.6.0` and *Android* Updates says `0.6.0 (code 6)`.
2. Copy text from Windows and receive it on Android. While OneSpace is foreground on Android, copy text and paste on Windows.
3. Take three photos with the regular Camera app. Test Ctrl+V in Windows Paint, measuring shutter-to-paste time. Check for duplicates and skipped photos.
4. Check for updates again—when v0.6 is already installed, it should say there is no newer update, even if Windows still hosts the v0.6 APK.
5. To test PC IP renewal: restart your router, check Windows IP with `ipconfig`, then try to sync again. UDP discovery might be blocked by your router or firewall; if so use manual IP or a DHCP reservation.
6. Compare Android battery use after a few hours of ordinary use in Settings → Battery → OneSpace, against a comparable period before upgrading. No lab battery claim is provided.

## If something fails

- **Kotlin / Gradle compilation:** Copy the final `BUILD FAILED` section, not the full log. Confirm `gradle/wrapper/gradle-wrapper.properties` uses Gradle 8.13.
- **Updating still prompts Download:** Ensure the installed app is actually v0.6 code 6 and you did not only download the APK without approving Android's install.
- **IP rediscovery unavailable:** Check Windows service is v0.6, Android and PC are on a routable local network, and the UDP port is not blocked on private networks.
- **Foreground service stops:** OEM/OS background execution restrictions cannot be bypassed; open OneSpace, re-enable sync and review its allowed battery/background behavior.

