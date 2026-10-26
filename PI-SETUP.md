> v0.8.0 adds optional Drive recording. For that mode, follow **DRIVE-SETUP.md**.
> The instructions below describe the preserved live-only baseline.

# OneSpace Pi Hub Preview — Kali ARM64 / Pi 3 Model B 1.2

This package adds a small Go hub and live-camera integration. **Recording is disabled; an SSD is not required for this live-only preview.** The Pi still needs its existing OS storage for small bounded hub metadata and credentials. It does not save camera footage.

The reported camera working baseline is preserved. Nothing has been deployed to your actual Pi by the cloud build. The documented Pi has Kali ARM64, an OV5647 CSI camera and manually started MediaMTX v1.21.2. Verify paths and installed versions before applying service examples.

## 1. Power and existing camera

Your handoff reports `vcgencmd get_throttled` returning `0x50005` (current undervoltage/throttling plus historical flags). Fix the supply/cable before trusting unattended operation. A suitable quality Pi 3 supply is normally about 5.1 V/2.5 A, accounting for peripherals. Recheck health under sustained load; a working browser picture is not a power-stability test.

Keep your existing manually working camera running while first testing the hub. `hub/deploy/camera-publisher.sh` contains the exact selected pipeline: 1344×1008 NV12, requested 46/1 FPS, two-buffer leaky queue, hardware H.264 5 Mbps, repeated sequence header, Baseline Level 4 and RTSP TCP to localhost 8554. This does not assert measured 46 FPS.

## 2. Install the hub binary

Extract the complete preview ZIP on the Pi. Verify the supplied checksums from the extracted package root before installation. Then use the Linux ARM64 hub binary supplied in the package (adjust its path to the binary location in that package):

```sh
sudo sh ./hub/deploy/install-hub.sh ./bin/onespace-hub-linux-arm64
```

The installer creates a restricted `onespace-hub` account, installs the hub and systemd unit, and preserves existing identities/data/configuration. It does not start or enable the service, change networking/firewall rules, edit firmware, change the MediaMTX installation, or touch Windows/Android pairings.

The hub defaults to `127.0.0.1:47603`. Choose the actual trusted LAN or Tailscale address in `/etc/onespace-hub/hub.env` before remote clients connect. Binding a specific address can require waiting for that interface after boot. An all-interface bind requires firewall rules that restrict access to the intended private interfaces; never open this service publicly.

## 3. Enroll your devices and pin the hub identity

Hub CLI administration takes the same state lock as the server. Stop the hub while enrolling/revoking; restarting afterwards will disconnect existing viewers and sessions.

```sh
sudo systemctl stop onespace-hub
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub fingerprint --data /var/lib/onespace-hub
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub enroll --data /var/lib/onespace-hub --id windows --name 'My PC' --kind windows --scopes clipboard,media,camera
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub enroll --data /var/lib/onespace-hub --id phone --name 'My phone' --kind android --scopes clipboard,media,camera
```

Enrollment deliberately prints a newly generated device token to this trusted local terminal. Give each device only its own token and needed scopes. Do not post tokens or identity files in chat, screenshots, logs or source control. Enter the hub address, independently verified SHA-256 certificate fingerprint and that device's token into its OneSpace Hub settings. The hub connection is separate from your existing PC pairing; do not replace that PC fingerprint/token with the Pi's identity.

Scopes are explicit; absent permission is denied. Use `camera` only for devices allowed to view live video; `clipboard` for hub clipboard sharing; `media` for media commands. Enabling a media scope is not permission to override the target device's local opt-in. An existing device ID is not silently overwritten. To revoke:

```sh
sudo systemctl stop onespace-hub
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub revoke --data /var/lib/onespace-hub --id phone
sudo systemctl start onespace-hub
```

Read the CLI `--help` for the exact available options. Preserve `/var/lib/onespace-hub` securely for upgrades. Deleting/recreating `identity.key` or `identity.crt` breaks the pin previously verified by clients.

## 4. Start the hub

After configuring the trusted listener and enrolling clients:

```sh
sudo systemctl enable --now onespace-hub
sudo systemctl status onespace-hub --no-pager
sudo journalctl -u onespace-hub -n 40 --no-pager
```

Do not run a second server with the same data directory. The unit uses bounded restart attempts, owner-only data, a restricted account and memory/task limits. These limits are starting limits, not measured Pi resource results; check actual RSS, temperature, CPU and power flags under load. No Docker, Python inference or video transcoder is required by the hub.

## 5. Connect live MediaMTX securely

The hub's WHEP gateway talks only to a fixed loopback URL, default `http://127.0.0.1:8889/camera/whep`. The enrolled client negotiates SDP over pinned HTTPS to the hub. Video travels directly between MediaMTX and the viewer using WebRTC DTLS-SRTP.

For a secure OneSpace deployment, bind **MediaMTX RTSP 8554 and its HTTP signaling 8889 to loopback**, disabling public/browser bypass of OneSpace camera permissions. `hub/deploy/mediamtx-live-only.example.yml` uses property names checked against the upstream v1.21.2 configuration. It disables recording, HLS, RTMP, SRT, MoQ, playback and management listeners, limits the local publisher/read path to `camera`, and keeps UDP 8189 for encrypted WebRTC media.

Do not blindly overwrite your working `~/camera-stream/mediamtx/mediamtx.yml`. Back it up, compare the example with the installed version and merge only the intended changes. Verify the updated config in a planned stop/restart test before switching permanently. Existing direct `http://PI:8889/camera` browser access will intentionally stop being available when HTTP signaling becomes loopback-only; use OneSpace's authenticated Live Camera viewer.

Restrict TCP 47603 to enrolled LAN/overlay devices and UDP 8189 to the intended viewer networks. MediaMTX may advertise LAN/overlay candidates; verify `webrtcAdditionalHosts` and interface settings for the actual Pi. Do not hardcode the old `192.168.0.106` address. Loading the hub UI does not prove WebRTC media connectivity. No TURN server or universal NAT traversal is bundled in this preview.

The gateway defaults to two viewers, caps configuration at 32, limits SDP to 256 KiB and keeps sessions for at most one hour by default. Pi 3 may support fewer viewers at 5 Mbps; begin with one. Each additional viewer increases upload bandwidth. Device revocation/session expiry is rechecked every 15 seconds, then the upstream session is deleted; network timeout can add delay. Graceful hub shutdown deletes known upstream sessions. A process kill or power failure cannot run cleanup; MediaMTX's disconnected-session/ICE timeouts must also be tested. Camera status reports signaling reachability, not actual camera frames or measured FPS.

## 6. Optional camera systemd conversion — after preserving the baseline

`onespace-mediamtx.service` and `onespace-camera.service` are optional unit examples, not installed/enabled by the hub installer. Before using them:

1. Stop the manual publisher and MediaMTX once, preserving their files/configuration.
2. Create a dedicated `onespace-camera` account and `/var/lib/onespace-camera`. Check that the Pi actually has `video` and `render` groups, and grant only needed device access. Do not add the hub account to those groups: it does not capture video.
3. Install your existing trusted MediaMTX binary as `/opt/onespace-hub/bin/mediamtx`, the reviewed config as `/etc/onespace-hub/mediamtx.yml`, and the supplied script as `/opt/onespace-hub/bin/camera-publisher.sh` with execute permission. No MediaMTX binary is downloaded by these scripts.
4. Make the config readable by the camera service account; keep any future credentials private. Install the reviewed units under `/etc/systemd/system`, run `systemctl daemon-reload`, then start MediaMTX followed by the camera publisher.
5. Test a reboot, encoder failure, camera reconnect and normal shutdown. Review journal output, libcamera permissions and `get_throttled`. Do not assume `network-online.target` proves the camera driver is ready; bounded restart/backoff handles transient startup failures.

Do not use `PrivateDevices=yes` for the camera publisher; it needs real CSI/media/encoder devices. The example intentionally leaves the existing hardware/firmware configuration unchanged. Some Kali/libcamera installations may need service sandbox/device-access adjustments; this is a hardware verification gate.

## 7. Different-network access

Install/configure Tailscale on the Pi, Windows and Android through the official supported workflow. Enroll only your devices and restrict overlay ACLs. The application does not install a VPN or create an account silently. Android supports one active VPN service at a time, which can conflict with another VPN app.

Use the Pi's stable overlay address for the separate hub profile. OneSpace's fingerprint/token checks still apply inside the overlay. Test from mobile data and a different Wi-Fi network, including UDP media. Tailscale may require external coordination/relay; direct routing is preferred when possible. A home Pi behind CGNAT alone does not guarantee internet reachability. LAN direct PC sharing stays independent when Pi/Internet/overlay is down.

## Current boundaries

- No recording, footage timeline, motion detection, disk retention or SSD setup in this preview.
- Existing Pi camera pipeline is reused; requested FPS is not a measured performance claim.
- Native Pi hardware, real Windows/Android WebRTC playback, overlay ICE, battery and same-key upgrade tests remain necessary.
- Live camera access and hub state remain available without Windows once the Pi services and networking are actually configured. PC-specific clipboard/media actions still require that PC online.
- Hub clipboard mode and direct legacy PC mode are explicit; avoid sending the same copy through both routes. Ordinary Android background clipboard restrictions remain.
