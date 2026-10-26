# OneSpace v0.4 — Fast local delivery

## Improvements

- **Immediate inbox notification instead of waiting up to 60 seconds.** Windows' `/v1/outbox?wait=22` supports authenticated, bounded long-poll requests. Each new queued item wakes connected receivers. Existing regular requests (`/v1/outbox`) still work.
- **Dedicated Android receive worker.** Incoming delivery no longer waits for the camera upload worker, so simultaneous camera and inbox activity is less likely to stall each other.
- **Faster camera observation.** Android checks newly saved photos around 400 ms after a MediaStore change instead of 1800 ms, with a retry around 2800 ms for files that have not finished saving. Existing periodic recovery checks remain.
- **Faster Windows clipboard observation.** The sequence-number check interval is 200 ms instead of 850 ms. Actual transfer speeds still depend on connection, formats and device capabilities.
- **Immediate in-app paste preparation.** When OneSpace is foregrounded on Android, its receiving service notifies the Activity as soon as a queued text/photo item is downloaded. An Android system notification is still used for background receipt; Android forbids ordinary background clipboard manipulation on most recent versions.
- **Echo prevention.** The Android clipboard listener ignores OneSpace-labeled inbound clipboard data so a received item is not continuously uploaded again.
- **Power management.** Long-polling replaces repeated fast network checks; offline reconnection backs off from 1.5 to 30 seconds; automatic temporary-storage cleanup runs at most hourly rather than every minute. Media observer is event-driven. No measured battery or RAM gains are claimed.
- **Compatibility.** Unchanged Android application ID, `versionCode=4`, `versionName=0.4.0`, compatible network endpoint and unchanged pairing storage format.

## Explicit limitations and safety

- Clipboard content is sensitive. Automatic Windows clipboard monitoring must still be enabled by the user, and per-device clipboard permissions are enforced server-side.
- Android does not allow normal apps to read another app's clipboard in the background. A text copy from another Android app is therefore **not** guaranteed to arrive automatically on Windows; use Android Share → OneSpace where needed.
- Android may also prevent placing new content on the system clipboard while OneSpace is backgrounded. Incoming data is saved and can be made pasteable when the app opens.
- Automatic Camera Sync requires full photo permission, background service enabled, and active local network. Phone camera apps, Xiaomi battery policy and photo size can affect delivery times. It is not a promise of zero latency or 24/7 operation.
- Windows source compiles and its Go test suite (including `-race`) passes. Android source has not been compiled or exercised on a phone in this environment. Build on your Windows Android Studio installation and validate before relying on it.
