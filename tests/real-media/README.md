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

These credentials are disposable fixture credentials, not a production
credential scheme. Production clients must use short-lived coturn REST
credentials. The receiver asserts `candidateType=relay` in forced-relay mode.
