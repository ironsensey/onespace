#!/bin/sh
set -eu
cd "$(dirname "$0")"
trap 'status=$?; if [ "$status" -ne 0 ]; then docker compose --profile turn logs bridge coordinator mediamtx publisher coturn receiver || true; fi; docker compose --profile turn down -v 2>/dev/null || true; rm -f test.crt test.key; exit "$status"' EXIT
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=onespace-real-media-test' \
  -addext 'subjectAltName=DNS:coordinator,IP:127.0.0.1' \
  -keyout test.key -out test.crt
docker compose --profile turn build
docker compose --profile turn up -d mediamtx publisher coordinator bridge coturn
sleep 5
TURN_URL=turn:coturn:3478?transport=udp \
TURN_USERNAME=testuser \
TURN_CREDENTIAL=testpassword \
FORCE_RELAY=1 docker compose --profile turn run --rm receiver