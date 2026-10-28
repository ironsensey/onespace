#!/bin/sh
set -eu
cd "$(dirname "$0")"
trap 'docker compose down -v 2>/dev/null || true; rm -f test.crt test.key' EXIT
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=onespace-real-media-test' \
  -addext 'subjectAltName=DNS:coordinator,IP:127.0.0.1' \
  -keyout test.key -out test.crt
docker compose build
docker compose run --rm receiver
