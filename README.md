# OneSpace v0.8.0 · Google Drive Recording Preview

**Start with [DRIVE-SETUP.md](DRIVE-SETUP.md).** This source and binary package
adds optional Google Drive storage and Pi camera recording to the existing
OneSpace Windows/Android/Kali hub preview. It has not been deployed to your Pi
or connected to your Google account.

- Windows **Cloud & recordings**: upload files, create text files, list/open
  OneSpace's Drive files, choose temporary/permanent retention, and Trash files.
- Android **Pi Hub → Cloud storage & recordings**: protected native controls
  with bounded foreground upload and explicit metered-data consent.
- Pi: existing hardware H.264 stream, approximately 15-second MP4 segments,
  resumable Google Drive uploads, and remote checksum verification before local
  footage cleanup. No second camera encoder or transcoding.
- Recording needs the explicitly installed **128 MiB bounded SD-card buffer**.
  It does not need an SSD or allocate 128 MiB of Pi RAM. Buffer pressure pauses
  recording; internet outages can cause footage gaps.
- Google OAuth uses `drive.file`, PKCE and a local callback. Google credentials
  stay on the Pi. Scope grants are explicit and existing hub tokens are kept.
- Temporary defaults to seven days after verified upload, editable to 1–365
  days. Permanent stays until deleted. Expiry/deletion moves files to Drive
  Trash; Trash still consumes quota until removed by Google or the owner.
- Existing direct LAN/overlay sharing, clipboard, media keys, pairing identities,
  Android application ID and update protocol are preserved.

Files uploaded to Drive are ordinary Google-readable files, protected in
transit by HTTPS. This is not an end-to-end encrypted cloud vault. Clipboard
and live camera do not use Google Drive as their real-time transport.

| Folder / file | Purpose |
| --- | --- |
| `bin/OneSpace-Windows-v0.8.0-Drive-Preview.exe` | Windows x64 GUI build |
| `bin/onespace-hub-linux-arm64` | Pi aarch64 Kali hub |
| `bin/onespace-hub-linux-amd64` | Linux x64 hub / integration fixture |
| `windows/` | Windows Go backend and dashboard source |
| `android/` | Android Studio project; sign with your original release key |
| `hub/` | Lightweight Go hub, Drive integration, camera gateway, deployment examples |
| `DRIVE-SETUP.md` | Google sign-in, recording buffer, MediaMTX and usage guide |
| `START-HERE.md` | Preserved client/pairing/build setup |
| `VALIDATION.md` | Current checks and remaining device/account testing |
| `validation-drive/` | Machine-generated current validation evidence |
| `SHA256SUMS.txt` | Packaged file checksums |

No Google credential, keystore, user pairing profile, or cloud-signed debug APK
is distributed. For an in-place Android update, use your original signing key
and a version code above your actual installed/published maximum. Later
unprovided v0.11/v2 code is not part of this branch. Previous preview notes are
archived under `docs/previous-v0.7.0` and `docs/previous-v0.6.2`.
