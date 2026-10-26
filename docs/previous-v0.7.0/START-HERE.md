# Start here — OneSpace Pi Hub Preview

## 1. What this build does

Pi runs the lightweight headless hub, scoped device registry, short-lived text/media queues and live-camera authorization. Your existing GStreamer hardware H.264 encoder and MediaMTX provide video. Windows/Android gain separate Hub settings and authenticated live viewing. No recording, storage retention or motion AI is enabled.

This is an additive preview from the source available here (v0.6.2), not a merge into the unattached v0.11/v2 projects. Keep backups and validate on test devices before replacing a relied-on installation.

## 2. Install on your Pi

Fix the previously reported undervoltage first. Extract the ZIP onto Kali ARM64, enter its root folder, and verify:

```sh
sha256sum -c SHA256SUMS.txt
sudo sh ./hub/deploy/install-hub.sh ./bin/onespace-hub-linux-arm64
```

The installer preserves existing identity/data, does not start services or change the camera pipeline, and leaves the hub bound to loopback. Follow **PI-SETUP.md** to enroll separate Windows/phone tokens while stopped, select a trusted LAN/overlay listener, restrict firewall access and enable the service. Give each device only its own token. Verify the Pi certificate fingerprint through your trusted SSH/local console. Never share enrollment output publicly.

For remote operation, install Tailscale using its official instructions on Pi, PC and Android, authenticate your own devices and restrict ACLs. The app neither creates a VPN/account silently nor opens public router ports. Android permits only one active VPN app at a time. Private-network configuration is still necessary; the ZIP alone cannot connect an inaccessible Pi.

## 3. Connect the existing live camera

Keep the proven 1344x1008/requested46FPS/5Mbps/two-buffer/TCP publisher. Compare and carefully merge `hub/deploy/mediamtx-live-only.example.yml` with the existing MediaMTX v1.21.2 configuration; do not overwrite it blindly. RTSP8554 and HTTP8889 must be loopback-only so raw viewers cannot bypass hub permissions. Enable reachable private WebRTC media on UDP8189 and advertise the correct LAN/overlay candidates. No recording paths are created.

Start with one viewer. The gateway defaults to two simultaneous viewers and expires sessions after one hour; use Reconnect if necessary. Permission revocation is checked every15 seconds plus bounded network cleanup time. Loading a page or passing a signaling test does not establish real video playback.

## 4. Windows

1. Fully stop the old OneSpace process; closing its dashboard alone may leave the engine running. Back up the entire existing OneSpace data directory.
2. Run `bin/OneSpace-Windows-Hub-Preview.exe` from a stable location. Keep your existing data directory and pairing certificates. This unsigned x64 developer build uses the available Edge/app-window dashboard, not the unattached WinUI3 project.
3. Open **Pi Hub**, enter the Pi HTTPS address, verified fingerprint and the Windows token. Choose the target phone ID. Blank token retains the credential only when address and identity are unchanged.
4. Enable the hub connection. To send copied text through the Pi, explicitly select **Pi hub** as the copied-text route. LAN direct remains the default. Enable **Receive hub text** only if desired. Text sending is limited to16KiB and expires after60 seconds. The trusted hub can read its text queue; this is not a zero-knowledge relay.
5. Open **Live Camera → Watch live**. Native Go handles pinned SDP signaling; hub credentials are not passed to browser JavaScript. Leaving/hiding the page stops video.
6. **Media Connect** sends Play/Pause/Next/Previous/Stop OS media keys to a permitted Windows target. The receiving PC must opt in locally and its Pi token needs the media scope. This controls an active player; it is not Spotify account integration and does not invent track/playing metadata.
7. To admit the existing PC API over Tailscale, explicitly allow the required overlay peer CIDRs (prefer individual /32 addresses) in Pi Hub settings. Only subsets of100.64.0.0/10 or fd7a:115c:a1e0::/48 are accepted. Existing pinned PC TLS and device permissions remain required.

If the GUI cannot start, inspect `%AppData%\OneSpace\onespace.log` (or your custom profile root), and check that ports47600/47601 are free. Logs rotate at1MiB with one previous file.

## 5. Android Studio and upgrade

1. Open the package's `android` folder in Android Studio. Install SDK36 and Build Tools35.0.0. Use the supplied Gradle8.13 wrapper and a full JDK17/21; do not upgrade tool versions blindly.
2. Before releasing, run `./Prepare-Release.ps1` on Windows, or supply `-PublishedVersionCode N` with your verified highest installed/published code. The project minimum is14; known floor13. Each explicit APK/bundle/install build reserves the next local code. Later v0.11/v2 apps may use higher codes: refresh the floor, do not assume14 is enough.
3. Choose **Build → Generate Signed App Bundle/APK → APK**, select the **original existing keystore and alias**, and build the release variant. Your application ID remains `com.onespace.companion`. Do not create a replacement key or uninstall to bypass signature errors.
4. Publish your signed APK through the existing Windows update service or install it in place using Android's normal installer. No cloud-built debug APK is included because it would have an unverified signing identity.
5. Open **Pi Hub & Live Camera**, enter the Pi host:47603 (Android field takes host, not full URL), verify the fingerprint and provide the phone's own token. Choose the Windows target ID. Save verifies both the pin and token.
6. Enable hub copied-text send/receive explicitly. Reception uses the visible opted-in background service. Paste activation happens only while OneSpace is unlocked/focused. Android background clipboard and service limits still apply.
7. For remote direct files/photos/workspace/updates, verify the PC overlay address against the existing PC certificate on the Hub page, then enable **Use private overlay for PC**. Original LAN host/pairing are retained. Background transfers on mobile/VPN-only networks require separate metered consent. Manual transfers/live video may use mobile data when you request them.
8. Open Live Camera; leaving the screen closes playback. Real WebView/WebRTC ICE and phone lifecycle behavior still require on-device tests.

## 6. Verify before relying on it

Check local sharing with Pi/internet off; remote text on different Wi-Fi/mobile; explicit permissions/revocation; Play/Pause with the target enabled; one then two camera viewers; camera/network restart; and native Windows paste. Confirm no recording files appear. Monitor Pi power, temperature, RSS and actual decoded FPS. Do not infer measured performance from requested46FPS or from desktop-loopback tests. See VALIDATION.md for actual checks and unrun hardware gates.
