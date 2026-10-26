# OneSpace v0.5 — Fast Clipboard & Camera Reliability

## Fixed in source

1. **Missed camera events during upload:** A serialized event-driven scanner now remembers changes that arrive during another upload. Previously a new photo could be skipped and wait for a 60-second fallback.
2. **Slow photo finalization:** Bounded camera rescans are scheduled roughly 200 ms, 750 ms, 1.7 s, 3.7 s and 8 s after the most recent MediaStore event. Camera apps can still delay publishing an image until it finishes saving.
3. **Clipboard interference:** The Android screen no longer pulls the Windows clipboard on an 8-second loop; that could overwrite a newly received photo with an older text item. The server-notified inbox remains the main receiving path.
4. **Foreground Android clipboard upload contention:** A dedicated sender queue lets copied text bypass slower general UI requests. Its brief retries are superseded by newer copied text.
5. **Repeated TLS handshakes:** Android shares pinned TLS configuration between requests, reducing unnecessary handshake setup (without disabling certificate-fingerprint verification).
6. **Windows camera decode performance:** Common JPEG/Y'CbCr images are converted directly to a DIB without allocating a second full-sized RGBA image. The original uploaded image is unchanged.
7. **Windows history performance:** The state snapshot is saved as compact JSON; acknowledged outbox records older than one hour are trimmed at cleanup. Unacknowledged jobs remain until their normal lifecycle ends.

## What this does NOT change

- Android 10+ restricts access to the global clipboard while OneSpace is not the focused app. Fully automatic copy monitoring from any foreground app cannot be guaranteed without platform-supported integrations. Inbound clipboard content may be saved in the OneSpace inbox even when Android won't place it on the system clipboard until the app opens.
- A camera image must first be finalized by the phone's camera app and transmitted over Wi-Fi. Speed depends on photo size, Wi-Fi and phone power management.
- A foreground service can still be stopped by Android or device-specific power management.
- This is not an on-device benchmark or a claim of zero bugs.

## Testing

- Windows Go unit and integration tests: PASS.
- Windows Go race detector: PASS.
- Windows amd64 EXE cross-compiled: PASS.
- Windows 1920x1080 YCbCr-to-DIB conversion benchmark (Linux test host): ~18 ms (single isolated conversion; NOT end-to-end latency).
- Android SDK compilation and real-device testing: NOT performed in this environment. Compile via Android Studio or `Build-OneSpace-Debug.bat`.
