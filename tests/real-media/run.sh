#!/bin/sh
set -eu
cd "$(dirname "$0")"
trap 'status=$?; if [ "$status" -ne 0 ]; then docker compose logs bridge coordinator mediamtx publisher receiver || true; fi; docker compose down -v 2>/dev/null || true; rm -f test.crt test.key; exit "$status"' EXIT
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=onespace-real-media-test' \
  -addext 'subjectAltName=DNS:coordinator,IP:127.0.0.1' \
  -keyout test.key -out test.crt
docker compose build
docker compose run --rm receiver 2>&1 | tee receiver-output.txt
echo "=== RECEIVER OUTPUT END ==="