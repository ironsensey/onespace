#!/bin/sh
# Optional, explicit first-time setup on the Pi. Creates a NEW 128 MiB image;
# refuses existing images/files and never formats a disk or partition.
set -eu
if [ "$(id -u)" -ne 0 ]; then echo 'Run with sudo after reading DRIVE-SETUP.md.' >&2; exit 1; fi
for program in fallocate mkfs.ext4 systemd-escape systemctl find; do
  command -v "$program" >/dev/null 2>&1 || { echo "Missing required program: $program" >&2; exit 1; }
done
id onespace-hub >/dev/null 2>&1 || { echo 'Install the hub first.' >&2; exit 1; }
buffer_image=/var/lib/onespace-recording-buffer.img
spool_root=/var/lib/onespace-hub/drive/spool
if systemctl is-active --quiet onespace-hub.service; then echo 'Stop onespace-hub.service before setup.' >&2; exit 1; fi
if [ -e "$buffer_image" ]; then echo 'Buffer image already exists; refusing to overwrite or format it.' >&2; exit 1; fi
if [ -d "$spool_root" ] && [ -n "$(find "$spool_root" -type f -print -quit)" ]; then
  echo 'Existing recordings are present. Preserve/upload them before mounting a new buffer.' >&2; exit 1
fi
if [ -L "$spool_root" ]; then echo 'Spool must not be a symlink.' >&2; exit 1; fi
install -d -m 0700 -o onespace-hub -g onespace-hub "$spool_root"
umask 077
# noclobber also protects against a race after the existence check.
(set -C; : > "$buffer_image")
fallocate -l 128M "$buffer_image"
mkfs.ext4 -q -F -m 0 "$buffer_image"
mount_unit=$(systemd-escape --path --suffix=mount "$spool_root")
if [ -e "/etc/systemd/system/$mount_unit" ]; then echo 'Mount unit already exists; review it manually.' >&2; exit 1; fi
cat > "/etc/systemd/system/$mount_unit" <<EOF
[Unit]
Description=OneSpace bounded recording upload buffer (128 MiB)
Before=onespace-hub.service onespace-mediamtx.service

[Mount]
What=$buffer_image
Where=$spool_root
Type=ext4
Options=loop,nodev,nosuid,noexec

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 "/etc/systemd/system/$mount_unit"
install -d -m 0755 /etc/systemd/system/onespace-hub.service.d
cat > /etc/systemd/system/onespace-hub.service.d/recording-buffer.conf <<EOF
[Unit]
RequiresMountsFor=$spool_root
EOF
systemctl daemon-reload
systemctl enable --now "$mount_unit"
chown onespace-hub:onespace-hub "$spool_root"
chmod 0700 "$spool_root"
install -d -m 0700 -o onespace-hub -g onespace-hub "$spool_root/camera"
echo '128 MiB bounded buffer mounted. Hub and camera services were not started.'
echo 'Continue with the reviewed MediaMTX recording config and Google sign-in in DRIVE-SETUP.md.'
