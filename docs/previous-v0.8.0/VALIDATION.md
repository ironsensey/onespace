# v0.8.0 Drive Recording Preview validation

Run on the cloud Linux x64 workspace on 26 October 2026. This is a tested
preview package, not a release certified on the user's devices or account.
Evidence is under `validation-drive/`.

## Passed

- **68 Go test cases, including subtests**: 34 hub/camera/cloud and 34 desktop/
  clipboard cases. `go test -race -count=1` passed; no test failures or skipped
  test cases. The hub CLI package has no unit tests and is exercised by the
  separate CLI integration smoke test.
- Cloud tests cover scope checks, upload ownership, stale offsets, an expired
  session, recovery after a lost final response without duplicate files,
  managed-folder boundaries, temporary/permanent retention, indexed expiry-day
  queries, metadata rollback, segment completion, recording pause/resume, and
  preserving local footage when remote checksum verification fails.
- Google sign-in tests check PKCE/offline/minimal-scope URL policy, reject wrong
  callback state, avoid saving cancelled authorization, serialize concurrent
  refresh requests, and enforce owner-only saved credential permissions.
- Permission upgrades preserve existing hub tokens; revoked devices cannot be
  revived by changing scope flags. Desktop proxy tests verify pinned bearer
  authentication, chunk bytes/offset headers, upstream error status, bounded
  requests and route/query restrictions.
- **Actual MediaMTX v1.21.2 Linux x64 recording integration** passed. Its archive
  SHA256 matched the upstream checksum manifest. FFmpeg published a synthetic
  320×240 H.264 stream in real time; the hub enabled recording via the real API;
  MediaMTX finalized two MP4 segments; the native completion hook marked them;
  ffprobe verified H.264 playback; the Drive fixture accepted their contents;
  verified remote checksum/metadata allowed local cleanup. This is not a CSI
  camera, Google account, Pi throughput, or WebRTC media test.
- The full provided MediaMTX recording example was parsed and successfully
  started with temporary loopback test ports using the real upstream binary.
- Hub CLI/TLS/signaling integration passed: local enrollment, credential hashing,
  exact pin rejection, authorization, queue/ACK/idempotency, response bounds,
  restart persistence and revocation. The final static binary also passed this
  smoke test. A run with the real media-event expiry wait passed during this
  change; Go expiry tests also pass. See JSON evidence for exact grouped checks.
- Four desktop/hub integration groups passed on Linux: protected local settings,
  pinned registry proxying, outbound queue delivery, inbound durable receipt/ACK.
  Its clipboard adapter is a stub; this does not prove physical Windows paste.
- **Android debug and release assembly passed**, versionName `0.8.0-preview`,
  validation versionCode **19**, application ID `com.onespace.companion`.
- **12 Android JVM tests passed**. Both lint variants report **0 errors and
  39 warnings**. Warnings include hardcoded/localization strings, deprecated
  platform calls and existing security/API/resource checks. See full reports;
  zero errors is not zero warnings. No Android instrumentation tests were run.
- `go vet ./...` passed for both modules. Bundled dashboard JS syntax, unique DOM
  IDs, and deployment shell syntax passed. Windows x64 GUI and Linux ARM64/x64
  hub binaries were built; hub binaries use no cgo.
- Seven existing identity, profile, update/signing-check and token-protection
  source implementations remain byte-for-byte unchanged from the prior preview.
  The new Android activity is non-exported; no original app ID or signer was
  replaced. Files and pairing data from the old preview were not migrated or
  overwritten in the original source folder.

## Performance evidence and limits

`upload-benchmark.txt` measures a bounded 1 MiB upload through the local Go/
HTTP/JSON/metadata test fixture. It includes fixture allocations and local disk
saves. It is a development regression baseline, **not** measured Google Drive
speed, Pi RAM usage, phone battery improvement, or Internet latency. The hub
smoke JSON contains a local enqueue-to-poll timing; it is not a Windows-to-phone
hardware latency promise.

Uploads and recording buffering have fixed limits, but the chosen Pi process
memory budgets and actual sustained camera/upload throughput still need
measurement. A nominal 5 Mbit/s camera needs upload bandwidth above its bitrate
plus protocol/request overhead. Slow networks cause recording gaps once the
bounded local buffer fills.

## Still requires the user's setup/hardware

- Complete Google OAuth on the user's own project/account and verify actual
  quota, Drive policy, token lifetime, upload/resume, Trash and expiry behavior.
  All Google API tests here use a local fake service; no user files were changed.
- Install and test the optional 128 MiB loop filesystem and systemd dependencies
  on Kali. The setup script was reviewed and syntax-checked; it was **not run**
  against the cloud host or the Pi. Real MediaMTX tests use a disposable test
  directory; hard mount checks are simulated in controller unit tests.
- Test the physical Pi's CSI/OV5647 pipeline, SD endurance, power stability,
  simultaneous live viewing/recording, memory use and network outage recovery.
  The previous `0x50005` power report still requires a physical supply/cable fix.
- Test WebRTC/ICE over the real LAN/overlay; native Windows clipboard listener,
  DPAPI and media keys; and Android WebView/lifecycle/foreground battery behavior.
- Verify the new Android cloud screen on a physical phone, including document
  picker grants, metered consent, pause/resume and device-unlock behavior.
- Build/sign Android with the original release key and a code above the actual
  installed/published maximum; test an in-place upgrade while retaining data.
  The validation debug signer differs from the user's original key; the release
  assembly is unsigned. Neither APK is bundled as a drop-in update.

No zero-bug guarantee or release-ready claim is made. The original upload and
previous packages remain available as rollback material.
