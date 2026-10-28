#!/bin/sh
set -eu

[ "$(id -u)" -eq 0 ] || { echo "Run with sudo." >&2; exit 1; }
[ "$#" -eq 1 ] || { echo "Usage: sudo sh install-turn.sh <public-ipv6-or-dns-name>" >&2; exit 1; }

public_host=$1
case "$public_host" in
  *[!A-Za-z0-9:._-]*) echo "Invalid public IPv6 or DNS name." >&2; exit 1 ;;
esac
turn_host=$public_host
case "$public_host" in
  *:*) turn_host="[$public_host]" ;;
esac

apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y coturn openssl
if [ -f /etc/default/coturn ]; then
  sed -i 's/^TURNSERVER_ENABLED=.*/TURNSERVER_ENABLED=1/' /etc/default/coturn
  grep -q '^TURNSERVER_ENABLED=' /etc/default/coturn || echo 'TURNSERVER_ENABLED=1' >> /etc/default/coturn
fi

install -d -m 0750 -o root -g onespace-hub /etc/onespace-hub
secret=$(openssl rand -hex 32)
realm="onespace"

cat > /etc/turnserver.conf <<EOF
listening-port=3478
tls-listening-port=5349
listening-ip=::
relay-ip=::
min-port=49152
max-port=49252
fingerprint
lt-cred-mech
realm=$realm
server-name=$public_host
no-cli
no-loopback-peers
no-multicast-peers
stale-nonce=600
log-file=stdout
simple-log
user=onespace:$secret
EOF
chmod 0640 /etc/turnserver.conf

ice_json=$(printf '[{"urls":["turn:%s:3478?transport=udp","turn:%s:3478?transport=tcp"],"username":"onespace","credential":"%s"}]' "$turn_host" "$turn_host" "$secret")
if grep -q '^ONESPACE_LISTEN=127.0.0.1:47603$' /etc/onespace-hub/hub.env 2>/dev/null; then
  sed -i 's|^ONESPACE_LISTEN=.*|ONESPACE_LISTEN=[::]:47603|' /etc/onespace-hub/hub.env
fi
if grep -q '^ONESPACE_ALLOW_PUBLIC=' /etc/onespace-hub/hub.env 2>/dev/null; then
  sed -i 's|^ONESPACE_ALLOW_PUBLIC=.*|ONESPACE_ALLOW_PUBLIC=true|' /etc/onespace-hub/hub.env
else
  printf 'ONESPACE_ALLOW_PUBLIC=true\n' >> /etc/onespace-hub/hub.env
fi
if grep -q '^ONESPACE_CAMERA_ICE_SERVERS=' /etc/onespace-hub/hub.env 2>/dev/null; then
  sed -i "s|^ONESPACE_CAMERA_ICE_SERVERS=.*|ONESPACE_CAMERA_ICE_SERVERS='$ice_json'|" /etc/onespace-hub/hub.env
else
  printf '\nONESPACE_CAMERA_ICE_SERVERS=%s\n' "$ice_json" >> /etc/onespace-hub/hub.env
fi
chmod 0640 /etc/onespace-hub/hub.env

systemctl enable coturn
systemctl restart coturn
systemctl restart onespace-hub.service
echo "coturn installed with UDP/TCP 3478 and relay ports 49152-49252."
echo "Forward TCP/UDP 3478 and UDP 49152-49252 to this Pi before remote testing."
