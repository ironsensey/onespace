# OneSpace v0.9.0 complete setup

This is a development preview. Keep the previous installation and private
backups. The cloud build did not deploy anything to your Pi, Google account,
Windows installation or phone. Do not share tokens or signing keys.

## 1. Get the files and preserve identity

Extract the complete v0.9.0 ZIP, or clone the development branch:

```sh
git clone --branch dev/onespace-independent-connect --single-branch https://github.com/ironsensey/onespace.git
cd onespace
sha256sum -c SHA256SUMS.txt
```

Keep Windows `%AppData%\OneSpace` (or your custom data folder), the stopped Pi's
`/var/lib/onespace-hub`, and the original Android signing key. Use the same
`com.onespace.companion` package. Do not uninstall Android to bypass a signer
error. The Pi is the documented Kali aarch64/ARM64 Pi 3 with 1 GB RAM.
Fix the previously reported undervoltage before relying on camera operation.

## 2. Install the Pi hub

Skip the stop command on a first installation. Commands below run in the
extracted project root on the Pi:

```sh
sudo systemctl stop onespace-hub.service
sudo sh hub/deploy/install-hub.sh bin/onespace-hub-linux-arm64
```

For recording, inspect and run this FIRST-TIME ONLY script while stopped:

```sh
sudo sh hub/deploy/setup-recording-buffer.sh
```

It creates a 128 MiB ext4 image on the SD card, not in RAM. It refuses an
existing image/recordings. Install e2fsprogs/util-linux if tools are missing.
Never delete an existing image containing footage. Keep at least 512 MiB free
outside the buffer and space for the published APK. Outages can fill the buffer
and cause gaps; it is not long-term offline recording storage.

## 3. Choose reachable Pi addresses

Edit `/etc/onespace-hub/hub.env` after installation. For LAN-only testing, use
your Pi LAN address; loopback 127.0.0.1 is unreachable from other devices.
An all-interface example is:

```text
ONESPACE_LISTEN=0.0.0.0:47603
ONESPACE_ALLOW_PUBLIC=false
ONESPACE_CAMERA_UPSTREAM=http://127.0.0.1:8889/camera/whep
```

Restrict the listener with your OS firewall. For remote access without a VPN,
the Pi needs public IPv6 reachability or a suitable public IPv4/router port
forward for TCP 47603. Set `ONESPACE_ALLOW_PUBLIC=true` only for that deliberate
deployment. Ingress has connection caps, but a public deployment still needs
firewall rules, OS updates and security testing. Forwarded headers never grant
access. Do not expose 47601 on the Windows laptop publicly.

**Carrier NAT cannot be fixed by this ZIP.** If your ISP gives no reachable
address, ask about a public address/IPv6, or provide another reachable relay.
No hosted endpoint, router change or DNS name is created automatically.
Tailscale remains an optional compatibility route, not a requirement.

Use a stable DNS name if your public address changes. Both apps support an
optional Pi LAN address with the SAME certificate pin, allowing local access
when the router cannot loop public traffic back into your LAN. Windows still
uses outbound connections to the Pi when it moves to another network.

## 4. Grant device scopes while the hub is stopped

For NEW devices only:

```sh
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub enroll --data /var/lib/onespace-hub --id windows --name 'My PC' --kind windows --scopes clipboard,media,camera,cloud,recording,connect,updates,updates.publish
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub enroll --data /var/lib/onespace-hub --id phone --name 'My phone' --kind android --scopes clipboard,media,camera,cloud,recording,connect,updates
```

For EXISTING IDs, retain tokens and explicitly replace their scope lists:

```sh
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub permissions --data /var/lib/onespace-hub --id windows --scopes clipboard,media,camera,cloud,recording,connect,updates,updates.publish
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub permissions --data /var/lib/onespace-hub --id phone --scopes clipboard,media,camera,cloud,recording,connect,updates
```

Grant only what you need. `connect` is required on BOTH relay devices. It does
not grant PC pairing or override PC permissions. `updates` downloads APKs;
`updates.publish` publishes APKs/reserves version codes. Do not grant publishing
to ordinary receiving devices. Revoked devices cannot be revived by a scope change.
Inspect the Pi's identity through your trusted local/SSH console:

```sh
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub fingerprint --data /var/lib/onespace-hub
```

## 5. Configure Google Drive and camera services

Follow **DRIVE-SETUP.md sections 4–5** for your own Desktop OAuth client,
Google sign-in and SSH loopback callback. Use only the downloaded JSON privately;
Google credentials remain on the Pi. Testing-mode consent may expire in seven
days. OneSpace requests Drive access, not Gmail-reading access.

Copy the recording example to a writable file, REVIEW it against your working
MediaMTX v1.21.2 configuration, and preserve your actual network candidates:

```sh
cp hub/deploy/mediamtx-drive-recording.example.yml mediamtx-reviewed.yml
```

Set `webrtcAdditionalHosts` to your actual reachable LAN/public addresses or
names as needed. Live media uses UDP 8189. RTSP 8554, WHEP HTTP 8889 and API 9997
must stay loopback-only. HTTPS working does not prove WebRTC media is reachable.
Keep your proven libcamera/GStreamer pipeline; requested FPS is not measured FPS.

The full ZIP includes a checksum-verified MediaMTX ARM64 binary. Install it and
your REVIEWED config with the managed service installer:

```sh
sudo sh hub/deploy/install-camera-services.sh bin/mediamtx-linux-arm64 ./mediamtx-reviewed.yml
```

The installer preserves an existing installed publisher by default. You can
explicitly supply a proven custom publisher script as its third argument. It
checks required GStreamer plugins/groups, makes backups and does not edit
firmware. Stop old manual MediaMTX/camera processes before enabling services;
do not run two publishers/transports on the same ports.

```sh
sudo systemctl enable --now onespace-hub onespace-mediamtx onespace-camera
sudo systemctl status onespace-hub onespace-mediamtx onespace-camera --no-pager
```

Stopping/restarting the hub also stops its dependent media services. After
maintenance start all three again. Check journals if a driver/group/sandbox
setting differs on your actual Kali installation.

## 6. Windows native app

1. Fully exit the old engine and back up its profile.
2. Run `bin/OneSpace-Windows-v0.9.0-Preview.exe`. It is unsigned developer code.
   The main UI uses Windows controls. No external dashboard browser is opened.
3. Home shows the PC fingerprint and addresses. Set a 12+ character ecosystem
   password if not already configured. Devices approves/rejects pairing.
4. Pi connection: enter the remote reachable HTTPS address, optional LAN HTTPS
   address, verified Pi fingerprint, Windows token, and phone hub ID.
5. Enable hub and **built-in OneSpace Connect**, then save. Keep copied-text
   route `lan` for the existing PC inbox carried inside end-to-end TLS. The
   separate `hub` short-text route trusts the Pi with queued plaintext.
6. Settings controls automatic clipboard sharing, startup and power policy.
   Per-device permissions remain under Devices. Reapply startup if you move or
   rename the executable; an old startup entry can still point to your old build.
7. Files, tasks, notes, cloud storage and recording are native pages. Choose file
   opens the OS picker on path-based pages. Refresh lists IDs used in the forms.
8. Live camera is an isolated in-app Microsoft WebView2 video surface, with
   browser permissions denied, external resources blocked and scoped RPC.
   Install the official maintained WebView2 Runtime if absent:
   https://developer.microsoft.com/microsoft-edge/webview2/
9. Minimize the dashboard to keep PC sharing running. Closing it stops the PC
   engine. Pi camera/storage remain independent. `-legacy-ui` opens the old
   browser UI only when explicitly requested for troubleshooting.

## 7. Android compatible update

The ZIP includes source and an UNSIGNED release APK (versionCode 28).
The source uses a conservative floor of 28 and minimum 29 for the next build. It cannot update your
installed app until signed with the original signing key.

Open `android/` in Android Studio. Use SDK 36, Build Tools 35.0.0, the supplied
Gradle 8.13 wrapper and a full JDK 17/21. Set the known published version floor
from your ACTUAL highest release, then Generate Signed App Bundle / APK → APK
with your existing keystore/alias. Never replace the key or application ID.
Alternatively sign the bundled unsigned APK using your SDK's apksigner and
original key, with passwords entered privately. Check its code exceeds the
installed version before publishing. Do not use the cloud debug signer.

In OneSpace, configure Pi Hub with the address WITHOUT `https://`, optional
LAN address, pin, phone token and Windows hub target ID. Verify/save. Keep your
original PC pairing. Enable **built-in Pi fallback** for PC features.

For a new remote PC pairing: first configure the hub, then open Pair PC. Enter
the PC certificate pin and password and select **Pair through configured Pi**.
Approve locally in the Windows native app. Hub enrollment alone cannot pair a PC.

Enable Receive Windows clipboard and camera/photo access only as needed.
Mobile background sharing needs its own consent. Android still restricts
background clipboard access; open/unlock OneSpace to activate received paste.
A force-stopped app or vendor battery policy can interrupt background work.

## 8. Updates while the laptop is off

In Windows Updates choose the original-key-signed APK and **Publish to Pi**.
Your Windows hub token needs updates.publish; use Pi published release to check
its actual manifest. Android Hub settings → **Get Android updates from Pi**
requires the phone's updates scope. Check/download/install through Android's
normal user-approved installer. Package, version, hash and signer are checked.
Google Drive and the Pi relay are not used to bypass Android installation rules.

## 9. Automatic shared version codes

Local builds retain a file-locked increasing counter. For builds on several
machines, use the Pi as the single authority. Privately set these environment
variables on the builder (not in source control):

```text
ONESPACE_SHARED_VERSION_CODES=true
ONESPACE_HUB_URL=https://YOUR_PI:47603
ONESPACE_HUB_FINGERPRINT=YOUR_VERIFIED_64_HEX_PIN
ONESPACE_HUB_TOKEN=YOUR_PUBLISHER_SCOPED_TOKEN
```

Explicit assemble/bundle/install builds reserve a durable code before packaging.
IDE sync does not reserve. Failed builds leave gaps; an unavailable authority
fails the build instead of silently reusing a code. Update the published floor
first if your existing release is higher. Keep the local .onespace counter.
The native Windows Updates page can reserve/show a code; the Python helper in
tools can also reserve and build. For an already reserved code use
`-PonespaceVersionCode=N` to avoid a second reservation. Use one authority for
all intended publishers; independent authorities cannot coordinate codes.

## 10. Verify before relying on it

Test local text/image/photo/file sharing; move the laptop and phone to different
networks; verify the built-in connection and PC permissions. Check reconnects,
large transfers, screen lock/unlock and metered consent. Verify moving live
frames, one then two viewers, short recording, playable Drive clips, retention,
Trash quota, reboot and an internet interruption. Turn Windows off and verify
Pi camera/cloud/update download still work. PC actions naturally need PC online.

Inspect errors locally:

```sh
sudo journalctl -u onespace-hub -u onespace-mediamtx -u onespace-camera -n 80 --no-pager
```

Windows logs remain under the profile's onespace.log. Read VALIDATION.md for
what passed here versus real Windows/Android/Pi and Google checks still unrun.
The branch-limited GitHub Actions workflow supplies Windows-native and security
checks without release signing keys. Check its actual run results on GitHub;
those Windows jobs cannot be run inside this Linux workspace.

For an Android rollback without deleting data, build the older source with the
same signing key and a NEW higher version code. Android normally rejects a
straight installation of an older APK over the new version.
