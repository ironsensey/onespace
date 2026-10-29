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

The receiver uses headless Chromium (Playwright) for forced-TURN testing, which is required because aiortc 1.9.0 cannot prove relay (no candidate-pair/local-candidate/remote-candidate stats and no relay-only ICE policy). The browser harness supports `iceTransportPolicy:"relay"` (relay-only selection) and `RTCIceTransport.getSelectedCandidatePair()` (selected candidate types), which is the trustworthy evidence forced-TURN requires.

Build (run in `tests/real-media`):
```sh
# Build the Node.js image with Playwright + Chromium
docker build -t onespace-test-receiver .

# Run with a running coordinator and bridge (see docker-compose.yml)
# Environment variables for the browser receiver (same as aiortc version):
# COORDINATOR=https://coordinator:47604
# VIEWER_TOKEN=windows-test-token
# VIEWER_ID=windows
# TARGET_ID=pi
# TURN_URL=          # optional
# TURN_USERNAME=     # optional
# TURN_CREDENTIAL=   # optional
# FORCE_RELAY=0      # set 1 to force relay-only candidate selection

docker run --rm \
  -e COORDINATOR=$COORDINATOR \
  -e VIEWER_TOKEN=$VIEWER_TOKEN \
  -e VIEWER_ID=$VIEWER_ID \
  -e TARGET_ID=$TARGET_ID \
  -e TURN_URL=$TURN_URL \
  -e TURN_USERNAME=$TURN_USERNAME \
  -e TURN_CREDENTIAL=$TURN_CREDENTIAL \
  -e FORCE_RELAY=$FORCE_RELAY \
  onespace-test-receiver
```

The Playwright Dockerfile uses `npm ci` with pinned Playwright@1.64.0 (the version that exists on npm). The image includes Chromium with `npx playwright install chromium --with-deps`, which resolves the previous `"idealTree" already exists` error.

The forced-TURN test still requires decoded frames and, in `FORCE_RELAY=1` mode, a selected viewer-local candidate type == `relay`. MediaMTX is not relay-configured, so only viewer-side relay is expected.

For Compose integration, replace the `entrypoint` line in `docker-compose.yml`:
```yaml
receiver:
  entrypoint: ["node", "/app/receiver.js"]
```

