# OneSpace Android v0.6.1 — publishing version fix

The Windows updater already contains a published APK with version code 8.
Android APK updates and OneSpace's server require a strictly higher APK version code.
This corrected Android build changes ONLY versionCode from 6 to 9 and versionName from 0.6.0 to 0.6.1.
It retains applicationId com.onespace.companion, the app features, the update channel, and all other source.

## Build on your Windows PC

1. Extract this archive. Open PowerShell in its `android` directory.
2. Run `./Build-OneSpace-Debug.bat` (PowerShell accepts `.\Build-OneSpace-Debug.bat`).
3. Confirm BUILD SUCCESSFUL and use android/app/build/outputs/apk/debug/app-debug.apk.
4. In Windows OneSpace > Updates, upload the APK. The server should detect versionCode 9 / versionName 0.6.1 automatically.
5. Open Android OneSpace > Updates, download and approve installation.

**IMPORTANT:** Debug APKs only update apps signed with the SAME debug certificate. If your currently installed app is release-signed, build this updated source with your EXISTING release keystore instead. Do not uninstall the app merely to bypass the version check: this can destroy app-managed data.

The Windows server's rejection is correct safety behavior. If the published version later increases beyond 8, the Android APK versionCode must increase beyond that newer published value before a build.

This archive contains source, not a compiled APK; the Android build has not been compiled or tested in this environment.
