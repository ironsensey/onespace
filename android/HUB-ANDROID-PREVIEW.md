# v0.8.0 addition: Google Drive storage & recordings

Open **Pi Hub → Cloud storage & recordings** after hub setup. This protected
native screen lists/manages OneSpace files, uploads in bounded chunks, creates
text files, and controls Pi recording. Google OAuth credentials stay on the Pi.
Foreground file uploads pause on leaving the screen; choose the same source
again to resume. See **../DRIVE-SETUP.md**. Preserve application ID and original
release key; set the version floor from your actual latest published APK.

The preserved v0.7.0 baseline notes follow:

# Android Pi Hub Preview

This adds to the supplied v0.6.2 Kotlin project. It is not a reconstruction of the later v0.11/v2 source mentioned in the handoff. Existing `com.onespace.companion` identity, PC preference store, Keystore alias and update-provider authority remain unchanged. Build upgrades using your existing signing key and a version code higher than your actual installed/published code.

## Setup

1. Open OneSpace → **Set up Raspberry Pi hub instead**, or unlock the app and choose **Pi Hub & Live Camera**.
2. Enter the Pi host/overlay DNS name (default hub port 47603), fingerprint copied from the trusted Pi, the device's enrollment token and optional recipient device ID. Verify and save. The token is stored with a separate Android Keystore key; it never replaces PC credentials or enters viewer JavaScript.
3. The phone can use Pi-only features without pairing a Windows PC. The Android screen lock is still required for protected app contents.
4. Enroll only necessary hub scopes. `camera` allows live viewing; `clipboard` allows ephemeral text relay; `media` allows sending Connect-style controls to an opted-in Windows recipient. This is not a Spotify account integration.

## Clipboard and direct transfers

- LAN remains the default copied-text route. **Use hub for copied text** explicitly selects the saved hub/recipient for focused-app clipboard sending. An uncertain LAN POST is not automatically replayed through the hub.
- **Receive hub clipboard text** is independent from the PC inbox setting and opts into a visible foreground connection, including mobile-data use. Only short text up to 16 KiB is relayed in this preview. Hub text expires quickly; expired messages are discarded. Files and photos use the existing direct PC protocol.
- Copied text is read only while the app is unlocked and focused. Received text is stored durably before notification/acknowledgement and becomes pasteable when the unlocked main screen is focused. No Android background clipboard restriction is bypassed.
- When a PC is already paired, the Hub page offers an optional **Direct PC over private overlay** address. Verify it using the existing PC certificate, then enable it. The original LAN address is retained. Direct HTTPS still handles image clipboard, files, Android camera photos and APK updates; the PC must explicitly allow its private overlay subnet.
- Background PC transfers normally remain on Wi-Fi/Ethernet. Mobile or VPN-only transport whose underlying metering is unknown requires **Allow PC background sync on mobile data** as well as the explicit overlay route. This can use billable data for large photos/files. Manual foreground transfers are user initiated and can also use data. The independent hub-text opt-in does not silently authorize large PC transfers.
- Disconnecting the hub removes only its profile and hub-specific send/receive flags. It does not erase the PC pairing, files or tasks.

## Live viewing

- The bundled local WebView document is receive-only WebRTC; phone camera/microphone permissions are not requested. Only Kotlin performs SDP requests, using exact certificate pinning and the enrolled token.
- External navigation and document subresource requests are blocked. DELETE session URLs must remain on the same pinned hub and match the camera-session route.
- Leaving the viewer closes the peer connection and attempts authenticated session deletion. Resuming a paused viewer requires screen-lock confirmation. The hub also needs bounded server-side session expiry for interrupted clients.
- Media/ICE may still fail even when HTTPS works. Test the actual Android WebView, H.264 decoding, overlay ICE candidates and router/mobile conditions. No video is recorded by OneSpace in this preview.

## Build/test status

Use the included Gradle wrapper with a full JDK 17/21 and SDK 36/build-tools 35.0.0. The native Kotlin UI is retained and receives a cyan default accent plus Hub/live/control pages; no unverified Compose rewrite is introduced.

JVM tests exercise address/pin validation, hostile camera session redirects, UTF-8 text bounds, lane policies and device ID rules. They do not replace physical Android lifecycle, metering, background, camera playback or same-key upgrade tests. Debug and unsigned release build/test/lint results are recorded by the parent package's validation report; do not install the cloud debug APK over an existing app with a different signer.
