# OneSpace remote camera setup

This adds a self-hosted TURN fallback without making Tailscale or another VPN
mandatory. Direct LAN WebRTC remains preferred. The Pi must have public IPv6
or a forwarded public IPv4 address, and the router/firewall must permit the
ports below.

See `REMOTE-NETWORK-REQUIREMENTS.txt` for the complete reachability boundary,
port-forwarding list and validation checklist.

## Pi

Copy the new ARM64 hub binary and TURN installer to the Pi, then run:

```sh
sudo sh ./hub/deploy/install-turn.sh YOUR_PUBLIC_IPV6_OR_DNS_NAME
```

The installer installs the widely used coturn service, creates a restricted
credential, configures the hub's authenticated ICE-server response, and
restarts the hub. It does not expose RTSP, MediaMTX WHEP, or the MediaMTX
management API.

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
CGNAT without a reachable IPv6/public endpoint. In that case, an optional
overlay or externally hosted TURN endpoint is required.
