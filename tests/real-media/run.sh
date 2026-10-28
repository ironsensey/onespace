#!/bin/sh
set -eu
trap 'rm -f test.crt test.key' EXIT
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=onespace-real-media-test' -keyout test.key -out test.crt
docker compose build
docker compose run --rm receiver
