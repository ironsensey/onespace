# OneSpace v0.6 — Reliability, Updates, and Battery

## What changed

- **Version labels:** Android reads installed `versionName` and `longVersionCode` from Android PackageManager, instead of showing hard-coded v0.4. Windows uses the build's version in its UI and `/v1/info` response.
- **APK publish metadata:** Windows extracts the Android app ID, versionCode and versionName from the compiled APK manifest automatically, validates the app ID, and stores SHA-256. You no longer type a release version into the publisher. Android still checks APK signing identity before offering installation, and the Android OS verifies the final install.
- **Repeat update prompts:** Android distinguishes downloaded versus installed versions. Automatic prompts are limited to once per version; manually opening Updates still allows checking and installing a downloaded update. Old cached APKs are removed once already installed.
- **Changing Windows IP:** Windows replies to local UDP broadcast discovery on port 47602 with a public certificate fingerprint; Android verifies the expected identity using certificate-pinned TLS before saving the new address. A user can still enter an IP manually if router/firewall blocks broadcast.
- **Battery:** Reduces camera fallback scans from every 60 seconds to every 10 minutes (MediaStore change events remain active), reduces the incoming queue wakeup frequency via longer server waits, and reacts to network state changes. On a reboot, user-enabled sync may restart via a visible foreground service if Android allows it.
- **Clipboard:** Windows retries transient clipboard read failures instead of permanently dropping a change after a busy clipboard. Clipboard echo prevention remains. Android clipboard restrictions have NOT been bypassed.
- **Photo watcher:** Event-driven 4-stage finalization checks with deduplication and a verified reconnection path for camera-only operation.

## Test status

- Windows `go test ./...` and `go test -race ./...`: passed.
- Windows `GOOS=windows GOARCH=amd64 go build`: succeeded.
- Windows embedded dashboard JavaScript syntax: passed `node --check`.
- APK metadata reader: tested against a synthetic binary Android manifest; needs validation against an actual signed APK.
- Android manifest: XML parsed. Kotlin syntax scanned with kotlinc; no syntax errors found, but Android libraries/SDK are unavailable here so **Android v0.6 has not compiled in this environment**.
- Actual battery usage, connection recovery and transfer speed: **not measured on real Windows/Android hardware**.

## Known platform limits

- Android 10+ generally restricts background clipboard reads from other apps. Camera MediaStore sync and inbound files are different from clipboard reading and may work while the foreground sync service remains allowed.
- Android and OEM battery settings can stop background processes; no ordinary app can promise uninterrupted background execution.
- Some routers block UDP broadcast; Windows firewall might require allowing OneSpace UDP 47602 on private networks.
- Replaying a file upload when the connection breaks mid-response can create duplicates; OneSpace avoids automatically replaying uncertain writes.
- A live Android app may have a different signing certificate from another user's build. In-place update requires the **same signing identity**.

