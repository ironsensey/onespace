# Real-media camera integration test

This fixture is intentionally separate from production deployment. It starts
MediaMTX, a synthetic H.264 publisher, the OneSpace coordinator, the Pi-side
bridge, and a WebRTC receiver in temporary containers. The receiver must
decode at least one video frame and reports the selected ICE candidate pair.

Requirements on a supported Linux host:

- Docker Engine with Compose v2.
- Internet access to pull the pinned test images on first use.
- Linux networking (the fixture does not use the user's Pi, router, or TURN
  configuration).

Run:

```sh
cd tests/real-media
sh run.sh
```

The test is non-trickle ICE. The receiver waits for ICE gathering to complete,
posts the complete SDP offer through the coordinator, and waits for decoded
frames. A successful SDP answer without a decoded frame is a failure.

The receiver is a headless Chromium (Playwright) test harness. This is required
because aiortc 1.9.0 does not expose `candidate-pair`/`local-candidate`/
`remote-candidate` stats and does not support a relay-only ICE policy, so it
cannot prove that the selected media path is relayed. Chromium supports
`iceTransportPolicy: "relay"` (forces relay-only selection) and
`RTCIceTransport.getSelectedCandidatePair()` (reports the selected pair's
candidate types), which is the trustworthy evidence the fixture needs.

The default fixture verifies coordinator-to-bridge MediaMTX media flow and
records the selected candidate pair. It does not prove Internet reachability.
The Compose TURN profile starts temporary coturn with test-only credentials:

```sh
docker compose --profile turn up -d
TURN_URL=turn:coturn:3478?transport=udp \
TURN_USERNAME=testuser \
TURN_CREDENTIAL=testpassword \
FORCE_RELAY=1 docker compose --profile turn run --rm receiver
```

The receiver uses Playwright's request context for coordinator HTTP (the page is
`about:blank`, so page-side `fetch` would be blocked by CORS) and runs WebRTC in
headless Chromium. A bounded readiness check retries coordinator session creation
until a deadline instead of a fixed sleep.

With `FORCE_RELAY=1` the receiver uses `iceTransportPolicy: "relay"` and fails
unless at least one video frame is decoded AND the viewer's LOCAL candidate in
the selected pair is `relay`. MediaMTX is not configured for relay, so the
remote side is host/srflx; requiring `relay`/`relay` would incorrectly reject a
viewer-only TURN path. Merely configuring a TURN URL or observing a coturn
allocation is insufficient; the viewer's selected media path must use TURN.

These credentials are disposable fixture credentials, not a production
credential scheme. Production clients must use short-lived coturn REST
credentials.
