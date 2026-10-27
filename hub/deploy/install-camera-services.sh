#!/bin/sh
# Explicit deployment of the existing camera pipeline and reviewed MediaMTX config.
# Does not install firmware, download executables, or change sensor/encoder settings.
set -eu
[ "$(id -u)" -eq 0 ] || { echo 'Run with sudo.' >&2; exit 1; }
[ "$#" -ge 2 ] && [ "$#" -le 3 ] || { echo 'Usage: sudo sh install-camera-services.sh /path/to/trusted/mediamtx /path/to/reviewed/mediamtx.yml [proven-publisher.sh]' >&2; exit 1; }
[ -f "$1" ] && [ -f "$2" ] || { echo 'Both source files must exist.' >&2; exit 1; }
if [ "$#" -eq 3 ] && [ ! -f "$3" ]; then echo 'Publisher script not found.' >&2; exit 1; fi
id onespace-hub >/dev/null 2>&1 || { echo 'Install the hub first.' >&2; exit 1; }
command -v gst-inspect-1.0 >/dev/null 2>&1 || { echo 'Install the working GStreamer/libcamera environment first.' >&2; exit 1; }
for plugin in libcamerasrc v4l2h264enc h264parse rtspclientsink; do
 gst-inspect-1.0 "$plugin" >/dev/null 2>&1 || { echo "Missing GStreamer plugin: $plugin" >&2; exit 1; }
done
for group in video render; do getent group "$group" >/dev/null || { echo "Missing device group: $group; review camera access on this OS." >&2; exit 1; }; done
if ! id onespace-camera >/dev/null 2>&1; then
 useradd --system --home-dir /var/lib/onespace-camera --no-create-home --shell /usr/sbin/nologin onespace-camera
fi
usermod -a -G video,render onespace-camera
install -d -m 0700 -o onespace-camera -g onespace-camera /var/lib/onespace-camera
install -d -m 0755 /opt/onespace-hub/bin
install -d -m 0750 -o root -g onespace-hub /etc/onespace-hub
deploy_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
stamp=$(date -u +%Y%m%dT%H%M%SZ)
for path in /opt/onespace-hub/bin/mediamtx /etc/onespace-hub/mediamtx.yml /etc/systemd/system/onespace-mediamtx.service /etc/systemd/system/onespace-camera.service /opt/onespace-hub/bin/camera-publisher.sh; do
 if [ -f "$path" ]; then cp -p "$path" "$path.backup-$stamp"; fi
done
systemctl stop onespace-camera.service onespace-mediamtx.service 2>/dev/null || true
# Source may already be the destination on an upgrade. Stage before replacing.
install -m 0755 "$1" /opt/onespace-hub/bin/mediamtx.new
mv /opt/onespace-hub/bin/mediamtx.new /opt/onespace-hub/bin/mediamtx
install -m 0640 -o root -g onespace-hub "$2" /etc/onespace-hub/mediamtx.yml.new
mv /etc/onespace-hub/mediamtx.yml.new /etc/onespace-hub/mediamtx.yml
if [ "$#" -eq 3 ]; then
 [ -f "$3" ] || { echo 'Publisher script not found.' >&2; exit 1; }
 install -m 0755 "$3" /opt/onespace-hub/bin/camera-publisher.sh.new
 mv /opt/onespace-hub/bin/camera-publisher.sh.new /opt/onespace-hub/bin/camera-publisher.sh
elif [ ! -f /opt/onespace-hub/bin/camera-publisher.sh ]; then
 install -m 0755 "$deploy_dir/camera-publisher.sh" /opt/onespace-hub/bin/camera-publisher.sh
else
 echo 'Existing installed capture command preserved.'
fi
install -m 0644 "$deploy_dir/onespace-mediamtx-drive.service" /etc/systemd/system/onespace-mediamtx.service
install -m 0644 "$deploy_dir/onespace-camera.service" /etc/systemd/system/onespace-camera.service
systemctl daemon-reload
echo 'Installed; not started. Stop any old manual publisher/MediaMTX before enabling these services.'
echo 'Then: sudo systemctl enable --now onespace-hub onespace-mediamtx onespace-camera'
echo 'Verify journals, moving live frames, Drive clips, power and reboot behavior.'
