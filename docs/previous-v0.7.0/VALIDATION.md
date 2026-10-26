# OneSpace Pi Hub Preview validation

## Passed in the cloud

- Windows engine: 32 Go test cases including subtests, race detector, fresh run, no failures/skips. Tests cover original LAN pairing/restart/permissions, pin mismatch rejection before bearer delivery, redirect credential protection, local API authentication/redaction, overlay CIDR limits, scoped receipts, cancellation, stable-id retry, latest-copy queue and expired media.
- Hub/camera: 20 Go test cases including subtests, race detector, no failures/skips. Covers scopes, expiry, idempotency, state persistence, ingress admission, coalesced clipboard ACK, active viewer revocation/ownership and unsafe upstream/session paths.
- Go vet passed for hub/camera and Windows engine. Windows x64 GUI-subsystem executable and statically linked Linux ARM64/AMD64 hub binaries cross-built. The ARM64 binary has not executed on the physical Pi.
- Hub subprocess integration: 10 grouped checks passed with real TLS/CLI/server, including real-time media expiry and durable purge.
- Windows-engine + hub subprocess integration: 4 grouped checks passed for settings authentication/redaction, registry proxy, outbound text queueing and inbound receipt/ACK. This Linux fixture uses clipboard stubs; it is NOT proof of native paste.
- Android exact-source debug and unsigned release APK assembly passed through supplied Gradle wrapper. Version name0.7.0-preview, validation APK code17. Application ID remains com.onespace.companion.
- Android JVM tests: 12 passed, no failures/skips (6 existing inbox-policy tests and 6 hub-policy tests). These are not Android instrumentation tests.
- Android debug/release lint: both passed with0 errors and22 warnings. Intentional pinned trust managers, local-only WebView bridge, older API/UI and photo/backup handling warnings remain for review. No trust-all SSL bypass or WebView SSL-error proceed was added.
- Embedded desktop JavaScript syntax and deployment shell syntax passed.

## Not yet verified

Real Pi power/encoder stability, Pi memory/CPU/temperature under simultaneous video+hub load, native Windows clipboard listener and DPAPI runtime, real phone/WebView WebRTC/ICE, mobile background/metered behavior, actual battery savings, and an in-place upgrade using the user's original key. Native UI/codec/network errors on those devices can still exist; this is a development preview, not a zero-bug or production-ready claim.

The existing camera pipeline is preserved as a reported working baseline, not re-tested here. Requested46FPS is not measuredFPS. Camera recording, motion detection, disk retention and SSD setup are absent. Camera live sessions are limited to2 by default and expire after1 hour.

The Windows event listener replaces constant200ms checks when its native initialization succeeds, with a5-second safety scan and short busy retries; polling remains as fallback. This is an implementation choice, not measured battery evidence.

The hub uses bounded owner-only JSON metadata snapshots rather than the proposed future SQLite schema, TLS1.3 on its new endpoint, scoped hashed tokens and event-woken HTTP polling. Legacy endpoints/pairing remain separate. Hub events are trusted-endpoint plaintext at rest until ACK/expiry; the broker is not an end-to-end-encrypted blind relay. TLS pinning and overlay encryption protect transport.

No private signing keys or prebuilt Android upgrade APKs are shipped. Validation debug signing does not establish compatibility with the installed app. Build locally with the existing key and refresh the current published version-code floor.
