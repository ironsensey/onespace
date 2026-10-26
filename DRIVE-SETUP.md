# Google Drive storage and camera recording · v0.8.0 preview

This package adds Google Drive integration to the existing OneSpace preview.
It does not sign into, upload to, or modify your actual account until you
configure and run it. Your Gmail address identifies your Google account;
OneSpace does not request permission to read Gmail.

## What you get

- Windows: **Cloud & recordings** in the dashboard.
- Android: **Pi Hub → Cloud storage & recordings**, behind device unlock.
- Upload large files, create text files, view the app's Drive files, open them
  in Google Drive, change Temporary/Permanent policy, and move files to Trash.
- Resume a file by selecting the same original filename, size **and contents**.
  The source file remains on the sending device. Pending sessions belong to
  the device token that started them; each device sees only its own file jobs.
- Record the existing Pi camera stream into short H.264 MP4 segments and
  upload completed segments to Drive. Live view remains available.
- A seven-day temporary default, adjustable from 1–365 days. The lifetime starts after upload verification. Existing files
  retain their policy until explicitly changed. Permanent means keep until
  you delete; it does not mean immutable or immune to Google account policies.

## 1. Preserve your installation

- Keep the previous package as a rollback copy.
- Back up the stopped hub's `/var/lib/onespace-hub` securely, including its
  certificate, device records, and any pending clips. Do not share this backup.
- Keep Windows `%AppData%\OneSpace`, its identities and pairing data.
- Build Android with your **original release signing key**, same application ID
  `com.onespace.companion`, and a code above your actual installed/published
  maximum. This source starts at code 18; refresh the published floor if your
  real app is newer. Cloud-built debug signing is not a compatible update.
- This branch extends the source available here; later unprovided v0.11/v2
  source is not included or overwritten.

## 2. Install the hub and bounded recording buffer

Use the ARM64 binary for the documented aarch64 Kali installation. Check the
Pi's power supply first: previously reported `get_throttled=0x50005` indicates
an active power/throttling problem. Recording cannot repair undervoltage.

```sh
sudo systemctl stop onespace-hub.service
sudo ./hub/deploy/install-hub.sh ./bin/onespace-hub-linux-arm64
sudo ./hub/deploy/setup-recording-buffer.sh
```

The buffer setup is a **first-time, opt-in root script**. Read it first. It
creates a new 128 MiB image file on the SD card, formats that file as ext4, and
mounts it at `/var/lib/onespace-hub/drive/spool`. It never formats a disk or
partition, refuses an existing image, and refuses to hide existing footage.
It also makes hub startup depend on the mount. Install `e2fsprogs`/`util-linux`
if their tools are missing. Keep at least 512 MiB free outside this image.

This is a hard filesystem capacity limit, not 128 MiB of Pi RAM. It bounds SD
usage even if the hub crashes or recording control fails. The hub normally
pauses near 96 MiB buffered or below 8 MiB available in the buffer. It requires
a distinct 64–256 MiB recording filesystem before enabling recording; you can
provision an equivalent filesystem yourself instead of running the script.

Do not put backups, Google credentials, or other user files inside this mount.
Do not run the setup script on every update. A failed setup is reported and
does not start recording. Review any partial installation before retrying;
never delete an existing image that may hold footage.

## 3. Configure MediaMTX without changing capture

- Keep your existing OV5647/libcamera hardware capture command unchanged; it
  still publishes `rtsp://127.0.0.1:8554/camera`.
- Review `hub/deploy/mediamtx-drive-recording.example.yml` against your actual
  MediaMTX v1.21.2 configuration. Save a backup before installing it.
- The example starts with `record: false`. It enables the management API only
  on `127.0.0.1:9997`, and grants API/publish/read only to loopback callers.
- Preserve your reachable WebRTC LAN/Tailscale ICE host configuration.
- Install the reviewed example as `/etc/onespace-hub/mediamtx.yml` only after
  checking your existing settings. Recording output and the completion hook
  must match `/var/lib/onespace-hub/drive/spool/camera`.
- If using our systemd examples, review `onespace-mediamtx-drive.service` and
  install it **as the existing `onespace-mediamtx.service`**, not as a second
  competing MediaMTX service. Back up that unit first.
- This optional transport unit runs as `onespace-hub` so it can write the
  owner-only buffer. The camera capture service remains separate. Do not run
  two MediaMTX processes on the same ports.
- The recording transport depends on the hub: stopping/restarting the hub
  also stops it. Start MediaMTX again after hub maintenance. This safeguards
  recording lifecycle; the hard filesystem cap provides independent protection.

Do not expose RTSP, the management API, or raw WHEP signaling on public
interfaces. Keep HTTPS 47603/WebRTC media limited to trusted LAN/overlay peers.
The existing live-only example remains available if you disable recording.

## 4. Create your own Google OAuth client

1. Open [Google Cloud Console](https://console.cloud.google.com/), create or
   select a project, and enable **Google Drive API**.
2. Configure Google Auth Platform consent for your personal application.
   During Testing, add your own Google email as a test user.
3. Create an OAuth client of type **Desktop app**, then download its JSON.
4. Copy that JSON privately onto the Pi, readable by `onespace-hub`, with
   owner-only permissions. For example, store it at
   `/var/lib/onespace-hub/google-desktop-client.json`, owner `onespace-hub`,
   mode `0600`. Do not paste it or tokens into chat or commit them.

Only `https://www.googleapis.com/auth/drive.file` is requested. This permits
OneSpace-created files; the app does not list or delete unrelated Drive files.
Your ordinary browser/Google app can still access your account normally.
Google organizational restrictions may block consent. Refresh tokens for an
external app left in **Testing** generally expire after seven days for this
scope; use the appropriate production consent configuration for durable use.
Google may still require reauthentication or revoke credentials later.

## 5. Connect your account locally

Stop the hub first: CLI operations use its exclusive metadata lock. If you
have a browser on the Pi, run:

```sh
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub drive-connect \
  --data /var/lib/onespace-hub \
  --oauth-client /var/lib/onespace-hub/google-desktop-client.json
```

Open the sign-in URL printed in your **local terminal** and complete Google
consent. The callback listens only on Pi loopback port 53682 for five minutes.
Do not share the URL. The flow uses state verification and PKCE.

For a headless Pi, first open an SSH session **from the PC browser's machine**:

```sh
ssh -L 53682:127.0.0.1:53682 YOUR_PI_USER@YOUR_PI_ADDRESS
```

Run the same `drive-connect` command inside that session, then open its URL
in the PC's browser. The loopback callback travels through SSH. Do not open
53682 on your router or bind the callback publicly. If the port is occupied,
choose a different `--oauth-port` and match both SSH tunnel ports.

The refresh credential is saved on the Pi as an owner-only file; it is never
returned to Windows/Android. A protected filesystem is still necessary because
anyone who compromises the Pi account can read its files. This preview does
not provide end-to-end encryption against Google or a compromised Pi.

## 6. Grant cloud permissions explicitly, retaining your current tokens

While the hub is stopped, replace `phone` / `windows` with your existing IDs:

```sh
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub permissions \
  --data /var/lib/onespace-hub --id phone \
  --scopes clipboard,media,camera,cloud,recording
sudo -u onespace-hub /opt/onespace-hub/bin/onespace-hub permissions \
  --data /var/lib/onespace-hub --id windows \
  --scopes clipboard,media,camera,cloud,recording
```

These commands replace the selected device's scope list, preserve its existing
token, and do not revive revoked devices. Include every old scope you wish to
keep. `cloud` permits managing the shared OneSpace Drive files; recording
control additionally requires both `camera` and `recording`. Grant the minimum
permissions each device needs. Existing devices gain no new scopes silently.

```sh
sudo systemctl daemon-reload
sudo systemctl start onespace-hub.service
sudo systemctl start onespace-mediamtx.service
```

Check `journalctl -u onespace-hub -u onespace-mediamtx` locally if startup fails.
The hub's source address/certificate/device-token checks still apply. Pair it
through your LAN or private overlay as described in START-HERE.md.

## 7. Use it in OneSpace

- Windows: launch `bin/OneSpace-Windows-v0.8.0-Drive-Preview.exe`, keep your
  existing profile, configure/verify the hub, then open **Cloud & recordings**.
- Android: build/install a correctly signed compatible update in Android
  Studio; open **Pi Hub → Cloud storage & recordings** and unlock.
- Press Refresh to verify your connected Google email, quota and file access.
  Merely having a saved credential is not proof current Google access works.
- Upload a test file or create a note; it should appear in the app-created
  `OneSpace` folder and in the file list. Open it in Drive to check it.
- Choose Temporary/Permanent and temporary days. Start recording, then refresh
  status. An enabled MediaMTX setting is not proof of frames: check new playable
  MP4 clips arriving in Drive. Stop recording explicitly when finished.
- You can keep a temporary file permanently or set a permanent file to expire.
  Moving to Trash requires a user confirmation. Expiry is automatic while the
  hub and Google access work, with periodic cleanup and retries after outages.
- File upload is foreground-only in this preview. Android pauses/cancels on
  leaving the protected screen and asks before large metered-data uploads.
  Select the same source again to resume. Temporary sessions can expire at
  Google; the app then restarts safely with the same allocated remote file ID.

## Storage, speed and limits

- 15-second MP4 target segments, cut on keyframes. Rotation is approximate.
  The current segment is uploaded after completion, not byte-for-byte live.
- File upload uses 1 MiB chunks, with a single recording uploader. Large user
  files are never staged completely on the Pi. Video uses the bounded buffer.
- At 5 Mbit/s, continuous footage is about **54 GB/day** before overhead. It
  needs sustained upload bandwidth above that bitrate. 5 TB is about 92 days
  at that rate **before other usage and Trash**, not unlimited recording.
- Temporary cleanup moves files to Drive Trash. It does not immediately free
  quota. Google normally removes Trash after 30 days; empty it yourself when
  needed. OneSpace does not permanently purge your Drive Trash automatically.
- Uploads use HTTPS; hub clients use pinned TLS. Recording checksums are checked
  against Drive before local deletion. Google can read ordinary uploaded MP4s;
  storage is not an opaque end-to-end encrypted vault.
- Internet loss/expired sign-in/full Drive pauses progress. Completed clips
  remain locally until verified, then are removed. The buffer cannot retain an
  indefinite outage; capture pauses and gaps are visible/expected.
- Power loss may leave the current segment unfinalized. It is not automatically
  uploaded or silently discarded. Inspect/repair it locally with media tools;
  do not mark arbitrary partial files complete. Completed clips remain queued.
- No Drive cloud relay for instant clipboard/live video: existing direct/hub
  text and WebRTC paths remain separate.
- Test on your Pi and account before relying on this for CCTV. This has not
  been deployed to your hardware or signed with your Android release key.

To disconnect, stop the hub and run `onespace-hub drive-disconnect --data
/var/lib/onespace-hub` as `onespace-hub`; this revokes the Google grant and
removes the saved credential. It does not delete your Drive files. If Google
cannot be reached, revoke OneSpace in your Google Account and secure the local
credential file until you can complete cleanup. Reconnecting a different
account is not an automatic migration of this account's root folder/jobs.
