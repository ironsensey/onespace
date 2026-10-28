#!/bin/sh
set -eu
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=onespace-real-media-test' \
  -keyout test.key -out test.crt

