# v0.8.0 Drive and recording additions

- New authenticated `/hub/v1/cloud/*` routes are separate from the original LAN protocol.
- `cloud` is a new explicit device scope; recording additionally requires `camera` and `recording`.
- Native Windows local routes retain their loopback/Host/X-Local-Key gate and the pinned hub credential. Android uses exact certificate pinning and a non-exported protected activity.
- Google OAuth Desktop flow requests only `drive.file`, uses PKCE and a state-checked loopback callback. Tokens and resumable URLs are owner-only Pi metadata, never UI fields.
- Fixed Google endpoints, disabled redirects, strict resumable-session origin/path validation, upload bounds and stable preallocated Drive IDs prevent arbitrary URL forwarding and duplicate files after ambiguous completion.
- Only OneSpace-marked files in the verified app folder may be managed; unrelated Drive files and folders are rejected.
- Uploaded MP4s are ordinary Google-readable files. TLS protects transport; the Pi and Google are trusted storage endpoints. No end-to-end cloud encryption is claimed.
- The 128 MiB ext4 loop image caps recording disk consumption independently of hub health. Software pauses around 96 MiB; MediaMTX part buffering is capped at 8 MiB. Disk/CPU/RAM behavior on the Pi remains to be measured.
- Checksums verify completed recording bytes before local deletion. MD5 here matches Drive's integrity checksum; it is not used for authentication or encryption.
- Temporary cleanup queries indexed expiry-day properties and catches up from a durable date cursor. Trash is recoverable initially and continues consuming Google quota.
- Large user files are forwarded in bounded chunks; Android uploads are foreground-only, cancellable, and request consent on metered networks.
- Account connection and recording remain opt-in. Existing identities, permissions, installed app ID, and APK signer must be retained.

The preserved v0.7.0 protocol baseline follows:

# Preview protocol and security boundaries

This preview adds a Raspberry Pi hub and live-camera integration to the source available in this workspace, which is OneSpace v0.6.2. The later v0.11/v2 projects described in the handoff were not attached. This package does not certify those later projects or replace their native UI work.

## Communication paths

| Path | Transport and purpose | Availability |
|---|---|---|
| Existing Windows ↔ Android LAN sharing | Existing pinned HTTPS API on TCP 47601 and UDP discovery on 47602 | Independent of Pi and Internet |
| Direct access through an approved private overlay | Existing pinned OneSpace endpoint over a separately installed overlay | Both endpoint devices must be online; explicit overlay admission is required |
| Pi clipboard broker | Pinned HTTPS on TCP 47603, scoped bearer credentials and event-woken long polling | Text messages only in this initial broker protocol |
| Pi media command broker | Same scoped event protocol | Target must consent and be online to execute; commands expire quickly |
| Live camera signaling | Authenticated WHEP SDP through the Pi hub to fixed loopback MediaMTX | Camera viewing permission required |
| Live camera video | WebRTC media to MediaMTX, with ICE connectivity through the LAN/private overlay | HTTP page/signaling reachability alone does not prove media reachability |
| Camera recording, retention and playback archive | Deferred | Not implemented or enabled in this preview |

Tailscale/WireGuard is an optional deployment layer, not an embedded VPN or a replacement for OneSpace authorization. Tailscale may use external coordination and relay infrastructure. A private tunnel does not automatically permit a device to copy a clipboard, view the camera, or control media. Headscale/custom relay infrastructure and automatic universal NAT traversal are outside this preview.

## Hub identity and enrollment

The hub has its own TLS identity, stored separately from every existing Windows identity. Verify its SHA-256 certificate fingerprint through a trusted local channel before saving a client profile. Do not accept a fingerprint supplied only by an untrusted discovery response. Damaged existing identity files must produce an error, not silent key regeneration.

Provision hub devices using the local administrative CLI. Enrollment credentials are sensitive and should be transferred only through a trusted local channel. Hub API credentials are scoped, unique per enrolled device, and must not be shared across Windows and Android. Unspecified new hub scopes are denied. Existing legacy pairing and its storage remain separate from hub enrollment.

The public hub information endpoint establishes neither pairing nor readiness. Device presence is based on recent authenticated activity and can lag an abrupt network loss. The interface must distinguish stored pairing, reachable service, active camera publisher, and actual decoded frames.

## Event delivery

The hub event API uses stable caller-supplied operation IDs for idempotency, a bounded shared queue with recipient-specific delivery, expiration and explicit recipient acknowledgements. A successful enqueue means queued; it does not mean the recipient pasted the text or executed the command. An acknowledgement records recipient processing, not proof that a third-party app accepted a paste. The initial queue limit is 256 events total, 128 enrolled devices, and 64 KiB per server text event; the client UI/sender policy is tighter at 16 KiB. New pending clipboard text from the same sender to the same recipient replaces older pending text. A single device can exhaust the shared queue with other permitted work, so quotas per sender remain a future improvement.

Clipboard and media commands have different lifetimes. A stale clipboard must not unexpectedly replace current user content, and a delayed media command must not execute minutes later. Receiver-side feature switches and platform permissions remain authoritative even if hub authorization succeeds. Clients must not automatically replay an uncertain legacy POST through a different transport and claim exactly-once delivery.

The default copied-text route remains the existing LAN route. The preview exposes an explicit hub route rather than replaying uncertain legacy writes through both routes. Media dispatch is best effort with at-most-once receipt-before-action handling: a crash between that receipt and an OS action can lose a command. The protocol does not promise exactly-once operating-system actions. Durable message queues are not a distributed consistency service for tasks/notes in this preview. File relay, resumable brokered image/file transfer, conflict-free notes and distributed release-code allocation remain future work.

## Encryption and storage

Each client-to-hub connection is encrypted and authenticated using the pinned hub certificate and a scoped credential. WebRTC encrypts the media transport. These protections are not a claim that the hub is blind to content: it terminates TLS, processes brokered clipboard text, and runs MediaMTX as the camera endpoint.

This initial hub uses bounded local metadata storage rather than a heavy service framework. Clipboard event payloads may exist as plaintext in that protected state file until acknowledged/expired and purged. Clipboard events expire after 60 seconds, media commands after 15 seconds; idle expiry is swept every five seconds. Digest-only idempotency receipts remain briefly after delivery. Filesystem permissions, short retention and bounded queues reduce exposure; they do not replace disk encryption. Do not describe the broker as end-to-end encrypted between Windows and Android or zero knowledge. Host compromise can expose clipboard text and camera video.

Android hub credentials use a separate Android Keystore-backed profile. The existing PC profile, Keystore alias and application ID remain compatible. Windows hub credentials use per-user DPAPI protection and must never appear in a dashboard response or logs. The non-Windows development adapter is base64 in an owner-only file, not an OS vault; it is not intended as protected Linux desktop credential storage. Certificate fingerprints are public identity checks; bearer tokens and private keys are secrets. Changing the Windows hub address or fingerprint requires supplying its new scoped credential rather than forwarding the old one to a different endpoint.

## Camera boundary

Only the configured loopback MediaMTX WHEP endpoint may receive forwarded signaling. A client cannot choose an arbitrary upstream URL. The gateway must reject redirects, oversized SDP, cross-origin session locations and deletion of another device's session. It should bind a session to the same enrolled device and require current camera scope for further operations.

Authorization on initial WHEP creation alone does not revoke an already established video session. The gateway revalidates sessions every 15 seconds and requests upstream deletion for revoked/expired sessions; network failure can delay actual upstream teardown. The default session lifetime is one hour and the Pi-oriented default permits two viewers. Graceful shutdown deletes known sessions. An abrupt crash can prevent this cleanup, so restarting the upstream media service may be necessary. Do not assume closing the OneSpace signaling page instantly stops an independent WebRTC connection.

Keep RTSP publishing and MediaMTX management private. Permit only required media ports on a trusted LAN/overlay, disable unused listeners, and preserve the user-tested single-capture/single-encoder pipeline. No SD-card recording is configured in this preview. “Recording disabled” does not imply that no control metadata or bounded logs are written.

## Platform and verification limits

Android 10+ background clipboard restrictions, service lifecycle limits, battery policies and user force-stop remain in force. Opening a remote tunnel does not grant background clipboard reading or silent power control. Live playback should stop when its screen closes and must not silently acquire microphone or camera permissions.

The supplied automated checks exercise local server protocols, negative authorization cases and pure policy logic. Linux clipboard stubs cannot validate Windows clipboard behavior. A fake WHEP upstream can validate signaling routing and ownership checks, not real camera encoding, ICE connectivity, delivered FPS or Android WebView decoding. Pi ARM64 compilation does not demonstrate execution on the physical Pi.

Before treating the build as a stable deployment, test the actual Pi power health, camera pipeline, overlays on different networks, native Windows clipboard/media execution, Android background behavior, same-key APK upgrades and disconnect/revocation recovery. No zero-bug, zero-latency or measured battery-improvement claim is made.
