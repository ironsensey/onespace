# OneSpace Pi Hub — 0.8.0 preview

This Linux ARM64 service adds a lightweight trusted control hub beside the existing Windows/Android LAN protocol. It does not replace TCP 47601, UDP 47602, existing pairing identities or APK signing. Optional Google Drive storage and recording are described in ../DRIVE-SETUP.md. Recording stays disabled until explicit setup and permission grants.

## Deployment and provisioning

Use [the Pi setup guide](../PI-SETUP.md) for system users, permissions, MediaMTX, the preserved camera publisher and private networking. Provision devices locally while the service is stopped. The CLI holds an exclusive lifetime state lock so an offline administrative process cannot overwrite newer running state.

```sh
sudo systemctl stop onespace-hub
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub enroll \
  --data /var/lib/onespace-hub --id windows --name "My PC" \
  --kind windows --scopes clipboard,media,camera
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub enroll \
  --data /var/lib/onespace-hub --id android --name "My phone" \
  --kind android --scopes clipboard,media,camera
sudo systemctl start onespace-hub
```

Each successful enrollment prints **one local JSON credential** containing `id`, `name`, `token`, `fingerprint` and `version`. The token is a 64-character secret; do not post the output in chat, screenshots, logs or shared files. Provision each client with its own token through your trusted local/SSH session. Compare the certificate fingerprint independently. The service stores only SHA-256 token hashes. An existing device ID cannot be silently replaced.

Use only required scopes: `clipboard` for short-lived copied text, `media` for approved media commands, and `camera` for live video. Both sender and destination need the event's scope. Client-side receive/media toggles remain an additional consent boundary. Camera permission does not grant clipboard or media permissions.

To revoke a device:

```sh
sudo systemctl stop onespace-hub
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub revoke \
  --data /var/lib/onespace-hub --id android
sudo systemctl start onespace-hub
```

Stopping the service closes active viewer sessions. Revocation removes queued traffic to/from that device; restart does not reactivate its token. There is no public enrollment/admin API in this first preview. A replacement device needs a new ID and explicit enrollment.

Local fingerprint inspection, also while stopped:

```sh
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub fingerprint --data /var/lib/onespace-hub
```

Default standalone invocation binds **loopback only**:

```sh
onespace-hub serve --data /private/owner-only-directory
```

For selected LAN/overlay interfaces, configure an explicit listener and firewall rules as documented in the Pi guide. The CLI defaults to `127.0.0.1:47603`. Ingress accepts loopback, RFC1918/ULA and Tailscale's `100.64.0.0/10` source addresses before TLS; public source addresses are rejected. Forwarding headers never grant access. Do not publicly forward hub, RTSP, camera administration or SSH ports.

```sh
onespace-hub serve --data /var/lib/onespace-hub \
  --listen 0.0.0.0:47603 \
  --camera-upstream http://127.0.0.1:8889/camera/whep
```

The last example deliberately binds all interfaces; it requires private-interface firewall restrictions. Camera upstream is a fixed, validated loopback WHEP endpoint, not a client-supplied URL.

## Wire protocol

All routes require TLS. The supplied server negotiates TLS 1.3, and clients must validate the exact enrolled certificate pin and validity dates. Except for public service metadata, use `Authorization: Bearer TOKEN`. Tokens must never be embedded in query strings.

| Method/path | Behavior |
|---|---|
| `GET /hub/v1/info` | Public service name/version, protocol version, capabilities, the current recording-control flag and text-size limit |
| `GET /hub/v1/devices` | Authenticated device list: `id`, `name`, `kind`, `scopes`, `online`, `lastSeen`; no credential material |
| `GET /hub/v1/events?wait=50` | Authenticated event-woken long poll; wait clamped to 55 seconds; recipient-specific scope filtering |
| `POST /hub/v1/events` | Authenticated, permission-checked bounded enqueue |
| `POST /hub/v1/events/ID/ack` | Durable recipient-only acknowledgement |
| `GET /hub/v1/health` | Timestamped Go heap, goroutines, uptime, pending count and storage health; no invented camera FPS/power readings |
| `/hub/v1/camera/status`, `/whep`, `/session/...` | Camera-scoped WHEP signaling and opaque sessions; see the Pi guide |

Event request:

```json
{"id":"client-generated-uuid-or-32hex-id","to":"android","kind":"clipboard.text","text":"sample text","action":""}
```

For media use `kind:"media"` with `action:"play_pause"`, `"next"`, `"previous"` or `"stop"` and empty text. Arbitrary commands, scripts and power actions are rejected. A receiver still needs its own media-control opt-in. Device IDs match `[A-Za-z0-9_-]{1,64}`. Optional client idempotency IDs match `[A-Za-z0-9_-]{8,80}`.

New messages return HTTP 201 and an event object; idempotent retries return HTTP 200. Reusing a client ID for different content returns 409. The server event `id` is a separate random 32-hex ID used for ACK. Events include `from`, `to`, `kind`, optional `text`/`action`, `createdAt` and `expiresAt` (UTC RFC3339).

Clipboard events expire after **60 seconds**; media commands after **15 seconds**. Retry deduplication receipts last five minutes. Clients must not replay old copied content after its TTL. Pending copied text from the same sender to recipient supersedes older unsent copies; this is a latest-value channel, not clipboard history. An ACK for an already fetched superseded event still succeeds for the correct recipient while its receipt remains.

Pollers sleep on event notification rather than fast timers. Policy is rechecked after wakeup; revocation wakes waiters. Only actual authenticated requests update `lastSeen`; background camera-policy checks cannot fabricate online status. `online` means an authenticated request within 120 seconds, not proof that every feature is ready.

## Resource and persistence bounds

- Standard-library-only Go service; no container stack, database daemon, video encoding or recording work.
- At most 128 enrolled device records, 256 pending events and 4,096 short-lived receipts.
- Text up to 64 KiB UTF-8; shipped clients may use a smaller low-latency limit. Strict bounded JSON input and unknown-field rejection.
- Poll batches at most 16 events and 512 KiB of serialized event payload; total held polls capped at 64 and two per device.
- Metadata snapshot capped at 8 MiB. Atomic temporary write, file sync and rename commit; failed writes do not become successful enqueue/ACK responses.
- File-backed JSON metadata is intentional for this bounded first preview. It is **not** a deployed SQLite implementation or an unlimited shared-workspace database.
- Expired events are hidden immediately; a five-second sweeper removes expired plaintext from the active on-disk snapshot even with no incoming traffic. Storage failures are reported through `storageHealthy:false` and retried. This is not secure forensic erasure of old disk blocks/backups.
- Durable acknowledgements remove delivered payloads. Receipt cleanup never deletes user files or camera recordings.
- Configure `GOMEMLIMIT=64MiB` as a Go soft heap target; it is not a process RSS hard limit. Camera/MediaMTX have their own resource budgets.

The hub is a **trusted endpoint**: queued copied text is readable by the hub process and stored in its owner-only data directory until ACK/expiry. TLS and a private overlay protect transport; this is not a blind end-to-end-encrypted clipboard relay. Do not grant automatic clipboard forwarding to an untrusted hub. No copied content or tokens are logged by the daemon.

`hub.json`, `identity.key`, `identity.crt` and `state.lock` live in the owner-only data directory. Snapshot/key files are mode 0600. Back up identity and metadata privately while stopped; never distribute live credentials or keys in the project ZIP. Damaged/partial identities are not regenerated. Certificate expiry or an incorrect clock requires deliberate repair/renewal, not bypassing validation.

## Build and test

```sh
cd hub
go test -race -count=1 ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath -o onespace-hub-linux-arm64 ./cmd/onespace-hub
```

Cross-compilation and mocked camera signaling do not prove physical Pi camera playback, real FPS, cross-network ICE, battery consumption or native-device clipboard behavior. Refer to the release validation matrix for what actually ran.
