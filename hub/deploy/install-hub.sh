#!/bin/sh
# Install the already-built hub only. Does NOT start services, enroll devices,
# modify firewall/firmware, overwrite MediaMTX config, or change camera capture.
set -eu
if [ "$(id -u)" -ne 0 ]; then
  echo 'Run with sudo after reviewing this script and verifying package SHA256SUMS.' >&2
  exit 1
fi
if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
  echo 'Usage: sudo ./hub/deploy/install-hub.sh ./bin/onespace-hub-linux-arm64' >&2
  exit 1
fi
case "$(uname -m)" in
  aarch64|arm64) ;;
  *) echo 'This installer expects the documented ARM64 Pi OS and ARM64 binary.' >&2; exit 1 ;;
esac
deploy_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if ! id onespace-hub >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/onespace-hub --no-create-home --shell /usr/sbin/nologin onespace-hub
fi
install -d -m 0755 /opt/onespace-hub/bin
install -d -m 0750 -o root -g onespace-hub /etc/onespace-hub
install -d -m 0700 -o onespace-hub -g onespace-hub /var/lib/onespace-hub
install -d -m 0700 -o onespace-hub -g onespace-hub /var/lib/onespace-hub/drive /var/lib/onespace-hub/drive/spool /var/lib/onespace-hub/drive/spool/camera
stamp=$(date -u +%Y%m%dT%H%M%SZ)
if [ -f /opt/onespace-hub/bin/onespace-hub ]; then
  cp -p /opt/onespace-hub/bin/onespace-hub "/opt/onespace-hub/bin/onespace-hub.backup-$stamp"
fi
if [ -f /etc/systemd/system/onespace-hub.service ]; then
  cp -p /etc/systemd/system/onespace-hub.service "/etc/systemd/system/onespace-hub.service.backup-$stamp"
fi
install -m 0755 "$1" /opt/onespace-hub/bin/onespace-hub.new
mv /opt/onespace-hub/bin/onespace-hub.new /opt/onespace-hub/bin/onespace-hub
install -m 0644 "$deploy_dir/onespace-hub.service" /etc/systemd/system/onespace-hub.service
if [ ! -e /etc/onespace-hub/hub.env ]; then
  printf '%s\n' 'ONESPACE_LISTEN=127.0.0.1:47603' 'ONESPACE_CAMERA_UPSTREAM=http://127.0.0.1:8889/camera/whep' > /etc/onespace-hub/hub.env
  chown root:onespace-hub /etc/onespace-hub/hub.env
  chmod 0640 /etc/onespace-hub/hub.env
fi
systemctl daemon-reload
echo 'Installed; not started. Read PI-SETUP.md, enroll devices while stopped, then choose trusted bind/firewall settings.'
echo 'Existing hub identities/data and existing MediaMTX/camera files were preserved.'
