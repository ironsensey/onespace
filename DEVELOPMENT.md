# Independent OneSpace development

This branch is an incomplete development increment, not a new release. The
v0.8.0 binaries in bin/, SHA256SUMS.txt and VALIDATION.md describe the previous
packaged preview, not the changed source here. Do not treat its validation as
verification of these new changes.

## Implemented in this increment

- Optional native Windows desktop window, built with Windows system controls
  and the existing Go engine. No WebView/browser or additional runtime.
- Native status, certificate fingerprint, paired/pending device listing,
  initial password setup, pairing approval/rejection and clipboard toggle.
- Native actions retain the original handlers, validation and state persistence;
  no new local key export or credential bootstrap file.
- Android prefers a certificate-verified LAN connection before the explicitly
  configured remote PC fallback. Preflight has 800 ms connect/read timeouts,
  bounded identity responses and a 30-second cache keyed to network/profile.
- Network/profile changes invalidate the cache; failed reads can reselect a
  verified route. Transfers that may have committed are not replayed on another
  route. Existing remote-network consent and certificate identity stay intact.

## Try the native Windows window

Build from windows/ with Go 1.23 or newer:

```
go build -buildvcs=false -trimpath -o ../OneSpace-Native-Preview.exe ./cmd/onespace
```

Fully exit the prior engine, back up the profile, then run:

```
OneSpace-Native-Preview.exe -native-ui
```

This is a standard Win32 controls prototype, not a completed WinUI 3 redesign.
Cloud, live camera, file management and detailed permissions still use the old
interface; native equivalents are pending. The browser interface remains the
default to avoid losing existing functionality. Closing the native window
leaves the engine running, as closing the old dashboard does.

## Still required before the requested architecture is complete

- OneSpace-owned coordination/signaling, ICE/STUN/TURN connectivity and relay
  deployment, using reviewed established networking libraries.
- An actually reachable coordination/relay endpoint. A Pi behind carrier NAT
  cannot guarantee arbitrary remote connectivity by itself. No public endpoint
  or infrastructure was provisioned by this change.
- Cross-route transfer IDs, duplicate-safe replay and full Pi file/workspace
  forwarding; current hub relay remains text/media only.
- Full native Windows pages and native playback, with Windows UI tests.
- Managed MediaMTX/libcamera installation and migration on the actual Pi.
- Pi-hosted authenticated signed APK distribution and compatible Android
  discovery/download support while the laptop is off.
- Physical Windows/Android/Pi tests, upgrade/signing preservation checks,
  actual power/memory/latency measurements and licensing/dependency audit.

Tailscale remains a supported compatibility route. It has not been removed or
replaced in this increment. Google Drive remains the large-storage provider.
No credentials, Android release signing key or Google account were changed.

## Checks for this increment

- Windows Go tests passed with the race detector, including native-action
  validation and restart-persistence coverage.
- Windows amd64 cross-compilation and Windows-targeted go vet passed.
- Android debug assembly, 15 JVM tests and debug lint passed (versionCode 20
  in this validation build). See the current build reports for warnings.
- Tests cover route ordering, rejecting unverified routes, duplicate candidate
  elimination, and native handler validation/persistence. They do not verify
  native Windows UI execution or actual phone TLS/network transitions.
- Android's validation APK is debug-signed and is not a compatible release
  update for an existing differently signed app.
