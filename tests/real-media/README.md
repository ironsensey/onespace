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
./run.sh
```

The test is non-trickle ICE. The receiver waits for ICE gathering to complete,
posts the complete SDP offer through the coordinator, and waits for decoded
frames. A successful SDP answer without a decoded frame is a failure.

The default fixture verifies coordinator-to-bridge MediaMTX media flow and
records the selected candidate pair. It does not prove Internet reachability.
The TURN profile requires a separately supplied coturn test endpoint:

```sh
TURN_URL=turn:turn.example:3478?transport=udp \
TURN_USERNAME=short-lived-user \
TURN_CREDENTIAL=short-lived-credential \
docker compose --profile turn run --rm receiver
```

The TURN profile must be run twice by the CI job: UDP and TCP/TLS fallback.
Credentials are injected at runtime and are never committed. The receiver
must assert `candidateType=relay` in forced-relay mode.
