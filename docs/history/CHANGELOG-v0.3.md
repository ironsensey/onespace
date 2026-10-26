# OneSpace v0.3 — Clipboard and Camera Reliability

## Fixed / improved

- **Windows automatic clipboard monitor**: detects *changes* in Windows clipboard text and common uncompressed bitmap formats. Copies are transferred via queued, authenticated local HTTPS to other trusted Android devices. Settings → Automatic Windows clipboard sharing is **OFF by default**, because clipboard data can be sensitive.
- **Android automatic photo sending**: MediaStore ContentObserver + 60-second recovery scan; avoids reuploading camera images after successful delivery, while looking back for photos that finish saving late. Shares images to Windows clipboard, and Windows queues them for other authorized Android devices.
- **Android incoming items**: background service stores queued images and text under Downloads/OneSpace/{Temporary,Permanent}; optional notification lets you open OneSpace to make the latest supported item pasteable on Android. Ordinary Android apps CANNOT silently read the clipboard of a different foreground app on Android 10+.
- **Multi-device permission enforcement**: Windows skips queuing clipboard/camera content to devices for which admin disabled the corresponding permission.
- **Battery/network**: checks for media events, reuses a 60-second inbox fallback rather than busy-looping, skips LAN requests when Android is on cellular, and avoids decoding Windows clipboard images unless the system clipboard sequence changed. No background CPU/battery measurements have been made yet.
- **Durability**: duplicate inbound item downloads are avoided after successful receive even when acknowledgments fail; v0.3 upgrades preserve installed application ID and device-pairing state if using the same signing key and Windows data folder.

## Important limits

- Camera auto-sync needs an opted-in foreground service, Full Photos permission, Wi-Fi on the same LAN and a running Windows app. Xiaomi/other OEM battery controls may still pause/terminate the service. Enable Camera & Inbox/Background in Android Settings.
- Android 10+ restricts clipboard access to focused apps and privileged keyboards. OneSpace cannot promise truly automatic Android-other-app → Windows clipboard or silently overwrite the Android clipboard while the app is backgrounded. Instead use Android Share → OneSpace for outgoing items and open OneSpace's incoming notification for paste.
- Windows image capture currently supports ordinary 24- and 32-bit **BI_RGB DIB** clipboard images. Some applications place other pixel formats on the clipboard; these are skipped rather than crashing.
- APK updates require the **same application ID, signing certificate and increasing versionCode**. OneSpace v0.3 is versionCode 3; updating a release-signed installation using a debug-signed APK will fail.
- Android code has NOT been built or tested in this environment; use Android Studio/SDK on Windows to compile and test the APK. Windows cross-compilation, Go tests, race checks and dashboard syntax checks pass. A full no-bugs guarantee would be misleading.
