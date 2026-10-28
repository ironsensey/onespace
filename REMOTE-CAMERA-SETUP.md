# OneSpace remote camera setup

This adds a self-hosted TURN fallback without making Tailscale or another VPN
mandatory. Direct LAN WebRTC remains preferred. The installer now uses coturn's
REST authentication: the hub derives short-lived viewer credentials from a
root-owned secret and never sends that permanent secret to Windows or Android.
This does not make a Pi behind CGNAT publicly reachable.

See `REMOTE-NETWORK-REQUIREMENTS.txt` for the complete reachability boundary,
port-forwarding list and validation checklist.

## Proposed no-VPN remote architecture (not deployed)

The current Pi hub does not yet open an outbound camera-signaling connection.
The existing Connect relay is PC API traffic only and must not be treated as a
video relay. The prepared target architecture is:

1. The Pi maintains an outbound, TLS-authenticated coordinator WebSocket using
   its existing device identity. It sends only bounded, authorized session
   signaling; it never exposes RTSP or MediaMTX administration.
2. Windows and Android open their own outbound authenticated coordinator
   connections. The coordinator authorizes a viewer/Pi pair and forwards only
   bounded SDP/ICE messages for that session.
3. The viewer and Pi-side MediaMTX attempt ICE candidates with STUN first.
   WebRTC DTLS-SRTP carries media directly when the selected candidate pair is
   host/server-reflexive.
4. If direct ICE fails, the clients use a separately provisioned TURN service
   over UDP, TCP, then TLS. The coordinator issues short-lived TURN
   credentials; it never stores a permanent TURN credential in a client.
5. Session IDs, device IDs, permissions, expiry, revocation, message size,
   attempt count, viewer count and bandwidth are bounded. Clients report the
   selected candidate pair, decoded frames, bitrate, jitter, packet loss and
   relay/direct state.

The repository now contains the bounded protocol implementation and Pi bridge,
but no public coordinator or TURN service is deployed.

### Signaling contract

OneSpace uses **non-trickle ICE** for the first implementation. Windows and
Android wait for browser ICE gathering to reach `complete`, bounded by six
seconds, then send one complete SDP offer through the authenticated route.
The Pi bridge sends that offer to MediaMTX WHEP and returns the SDP answer.
MediaMTX v1.21.2 is compatible with this offer/answer flow. Candidate
messages remain reserved for a future negotiated protocol version; they are
not silently assumed to work.

Coordinator requests are retryable: Pi polling does not consume a request.
The Pi acknowledges only after a MediaMTX answer has been returned through
the coordinator. Requests have bounded delivery attempts and expire with the
session. Signaling messages carry deterministic IDs and duplicate IDs are
ignored. A MediaMTX `Location` is retained and accepted only if it resolves
under the configured numeric loopback WHEP URL; invalid or unsafe locations
are rejected, and the retained session is deleted when answer delivery,
acknowledgement or authorization fails.

The coordinator device file must contain an explicit camera permission map;
being enrolled is not sufficient:

```json
{"devices":{"pi":"<sha256>","android":"<sha256>"}, "camera":{"pi":["android"]}}
```

## Hosting comparison and review gate

No external infrastructure has been installed. The following is the concrete
proposal for review:

| Component | Candidate | Current documented limits/constraints | Decision |
|---|---|---|---|
| Coordination | Cloudflare Workers + SQLite Durable Objects | Workers Free: 100,000 requests/day and 10 ms CPU/request. Durable Objects Free: SQLite only, 5 GB account storage, 100 classes, 32 MiB received WebSocket messages. WebSocket connections are control/signaling only, not video. | Feasible for a small control plane, subject to account/signup and WebSocket lifecycle testing. |
| TURN | Cloudflare Realtime TURN | UDP 3478/443, TCP 3478/80, TLS 5349/443. Metered at **$0.05 per real-time GB outbound** when used without Cloudflare's SFU. Per-allocation documented limits include 50–100 Mbps and 5–10 kpps. | Technically suitable, but not zero-cost. Requires explicit approval and credential/API provisioning. |
| TURN alternative | Oracle Cloud Always Free VM + coturn | Up to 2 Ampere OCPUs/12 GB RAM equivalent and 200 GB block storage allocation described; public capacity is not guaranteed, idle instances may be reclaimed, and the official page requires card verification for account setup. Egress allowance was not stated on the current official Always Free page. | Possible self-hosted test option, but not a guaranteed free production dependency. |
| Coordination alternative | Render Free Web Service | Spins down after 15 minutes without HTTP/WebSocket traffic, wakes in about one minute, 750 free instance-hours/month, ephemeral filesystem, outbound bandwidth quota and possible suspension/billing. | Not suitable for the always-on coordinator. |
| Coordination/compute alternative | Fly.io | Current pricing documentation states there is no free tier/monthly free usage allowance; new accounts have only a short free trial. | Not suitable for the user's no-paid dependency requirement. |

Cloudflare Workers/Durable Objects plus Cloudflare Realtime TURN is therefore the
most operationally simple proposal, but it is **not free for relayed video**.
No deployment, account creation, payment method, API token, DNS change or client
configuration will occur until the user approves this proposal.

The proposal must be validated with a real Pi at home and a phone on mobile
data. A coordinator health response or SDP answer alone is not success: the
viewer must display frames and record a verified direct or TURN-selected ICE
candidate pair.

## Pi

Copy the new ARM64 hub binary and TURN installer to the Pi, then run:

```sh
sudo sh ./hub/deploy/install-turn.sh YOUR_PUBLIC_IPV6_OR_DNS_NAME
```

The installer installs the widely used coturn service, creates a restricted
REST-auth secret, configures the hub's authenticated ICE-server response, and
restarts the hub. It does not expose RTSP, MediaMTX WHEP, or the MediaMTX
management API. The secret stays in `/etc/onespace-hub/hub.env` and
`/etc/turnserver.conf`; do not copy either file to a client.

Forward only:

| Protocol | Ports | Purpose |
|---|---:|---|
| TCP | 47603 | OneSpace HTTPS signaling and built-in PC relay |
| UDP/TCP | 3478 | TURN allocation |
| UDP | 49152-49252 | TURN media relay range |

Set the hub listener in `/etc/onespace-hub/hub.env` to an IPv6-capable address:

```text
ONESPACE_LISTEN=[::]:47603
ONESPACE_ALLOW_PUBLIC=true
ONESPACE_CAMERA_UPSTREAM=http://127.0.0.1:8889/camera/whep
```

Keep the OS firewall enabled and restrict administrative ports. Never forward
TCP `47601`, RTSP `8554`, MediaMTX HTTP/WHEP `8889`, MediaMTX management
`9997`, or SSH unless separately secured for maintenance.

## MediaMTX

Keep the existing hardware pipeline and local MediaMTX endpoints:

```text
RTSP: 127.0.0.1:8554
WHEP: 127.0.0.1:8889
Management: disabled or loopback-only
```

The authenticated hub proxies WHEP signaling. The clients receive the TURN
configuration only after hub authentication and camera-scope authorization.

## Windows and Android

Use the Pi's public IPv6 address in bracketed URL form:

```text
https://[YOUR_PUBLIC_IPV6]:47603
```

Keep the existing hub certificate fingerprint and device-specific tokens.
Windows and Android now request the authorized ICE configuration from
`/hub/v1/camera/status`; the browser/WebView camera viewer passes it to
`RTCPeerConnection`. Direct candidates are still attempted before TURN.

## Validation

Test from a network that is not the Pi's LAN:

```powershell
Test-NetConnection '[YOUR_PUBLIC_IPV6]' -Port 47603
```

Then open Live Camera. A successful HTTPS connection proves signaling only;
video is proven only when the viewer reaches `connected` and displays frames.
Check the Pi services and coturn logs if signaling succeeds but video does not:

```sh
sudo systemctl status onespace-hub coturn --no-pager
sudo journalctl -u onespace-hub -u coturn -n 100 --no-pager
```

This path still cannot overcome an ISP that blocks inbound IPv6 or supplies
CGNAT without a reachable IPv6/public endpoint. In that case an externally
hosted TURN service and an outbound signaling coordinator are required. The
repository now includes a media-blind coordinator broker and standalone
command, but the Pi/client outbound adapters and deployment are not yet
enabled. No Internet-reachable Pi/TURN deployment was available for this
handover; successful HTTPS signaling or a local test endpoint must not be
reported as remote camera success.
