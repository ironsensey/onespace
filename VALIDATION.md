# v0.9.0 Independent Preview validation

Validated in the Linux cloud workspace on 27 October 2026. This is a tested
preview, not a release certified on the user's physical devices or Google account.
Current evidence is in validation-independent/; older evidence is archived.

## Passed here

- **82 Go test cases including subtests**: 42 hub/camera/cloud/update and 40
  desktop/clipboard cases, with the race detector; no failed or skipped cases.
  Desktop clipboard integration uses Linux stubs, not physical Windows paste.
- Relay coverage: missing scope/offline/self target, bounded connection slots,
  real nested TLS with an independent PC certificate, revocation, malformed
  upgrade rejection and shutdown. Shared code allocation covers concurrent
  reservations, restart persistence and rejection of corrupted counters.
- Update store covers publisher-vs-reader authorization, malformed APKs,
  monotonic publication, actual bytes/hash/manifest, restart and corrupt metadata.
- Native backend covers note/task/file lifecycle, permissions/validation through
  the original handlers, credential masking, retention of pagination cursors,
  and cancellation without partial-file persistence or source-file deletion.
- **Actual MediaMTX v1.21.2 Linux x64 recording integration** passed, with a
  synthetic FFmpeg H.264 publisher, completed playable MP4 segments, the actual
  completion hook, a fake Drive service and checksum-verified local cleanup.
  This does not test the Pi's physical CSI camera or Google account.
- **19 Android JVM tests passed**, including real OkHttp/nested TLS fixtures:
  original PC identity and bearer authentication survive the relay; wrong Pi
  and PC identities are rejected; an unavailable LAN Pi address falls back.
  This is JVM TLS, not Android Conscrypt/lifecycle/instrumentation testing.
- Android **debug and unsigned release assembly passed**, versionName
  `0.9.0-preview`, APK versionCode **28**, package `com.onespace.companion`.
  Both lint variants have **0 errors and 41 warnings**. Full reports are included.
- Source version metadata is advanced conservatively to floor 28/minimum 29
  for future builds, preventing fresh source copies from building below the
  bundled candidate. Update the floor again if the user's actual release is higher.
- Actual hub + Linux desktop processes passed eight integration groups: inner
  PC pin verification, denial without PC pairing, pairing approval, workspace/
  item operations, PC permission enforcement, APK publication and duplicate-code
  rejection, APK download with Windows stopped, and shared code reservation.
- Actual Gradle packaging configuration (dry-run) and the Python helper reserved
  increasing codes against a temporary pinned hub. No release key was used.
- Packaged static hub CLI/TLS/signaling smoke passed ten groups, including a real
  media-event expiry wait and durable purge. The timing in that fixture is not
  measured PC-to-phone latency.
- Embedded video JavaScript passed mocked connection/stop and Go-RPC arity/proof
  checks. This is not real WebRTC playback or a native Windows UI test.
- Windows GUI x64 and Linux ARM64/x64 binaries cross-compiled. Windows-targeted
  and hub go vet passed. Windows clipboard memory copying uses RtlMoveMemory,
  avoiding the former foreign-address-to-Go-pointer vet warnings.
- Camera/install shell syntax and Python test/helper syntax passed. Both MediaMTX
  ARM64 and amd64 archives matched the official upstream checksum manifest;
  the ARM64 binary and redistribution licence are packaged.

## Blocked or still requires real setup

- **Dependency vulnerability scanning did not complete**: this environment's
  Go vulnerability database request returned Forbidden. No clean-security-scan
  claim is made. The branch-limited GitHub Actions job provides a permitted-network
  check, but its actual results must be inspected separately.
- Real Windows native UI, file picker, OS clipboard, power/network/resume events,
  WebView2 video playback and same-profile upgrade remain unrun here. A disposable
  Windows clipboard test and UI Automation smoke script/workflow are included.
- Real Android Conscrypt, WebView/WebRTC, document picker, background service,
  metered consent, battery behavior and same-original-key in-place upgrade.
- Public Pi endpoint/DNS/firewall/router setup. No public address or hosted relay
  was created. Carrier NAT without another reachable endpoint remains a boundary.
  No general-purpose ICE/STUN/TURN data connection engine is supplied.
- Actual Google OAuth, quota, upload/resume, retention and Trash behavior. Google
  API tests use local fixtures; no user's account or files were changed.
- Actual Pi installation of the loop filesystem/systemd camera services,
  permissions, OV5647 hardware, SD endurance, power, RSS/CPU/temperature,
  simultaneous viewing/recording, network outages and reboot recovery.
- The earlier undervoltage report still needs a physical supply/cable fix.

The Windows executable is unsigned developer code. The bundled Android release
APK is UNSIGNED and needs the user's original key before installation. Debug
validation APKs are not bundled as upgrades. No signing key, user credential or
pairing profile was distributed. Original test branch/package remain separate;
Android rollback needs the same signer and a higher code, not a simple downgrade.
