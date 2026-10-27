# OneSpace v0.9.0 · Independent Preview

Start with [SETUP-v0.9.0.md](SETUP-v0.9.0.md). This development build adds
built-in PC connectivity through the Pi and a native Windows dashboard.
It is not a release certified on your physical devices or Google account.
It extends the supplied source; later unprovided branches are not merged.

- Native Win32 Windows controls: pairing, permissions, files, clipboard,
  tasks, notes, hub configuration, media keys, Google Drive, recording and APKs.
- Live camera opens an isolated in-app WebView2 video component. The dashboard
  does not launch Edge or another external browser. WebView2 Runtime is needed
  only for video; most Windows 10/11 installations already have it.
- OneSpace Connect: Windows opens outbound pinned TLS connections to the Pi.
  Android prefers a verified LAN PC connection, then the Pi relay. The original
  PC TLS connection, certificate pin, pairing token and permissions stay intact
  inside that relay. Large files are streamed, not stored on the Pi.
- Optional local and remote Pi addresses avoid reliance on router hairpin NAT.
  Windows network-change/resume notifications reconnect without polling.
- Pi-hosted APK publication and downloads work with the laptop off. Android
  keeps its package, hash, version and signing-certificate checks. Publishing
  permission is separate from downloading permission.
- Optional shared release-code reservation on the Pi, including direct Gradle
  integration. Local file-locked version codes remain available for offline work.
- Google Drive remains the large-storage destination. Resumable file uploads,
  notes, temporary/permanent retention, Trash and short camera recordings remain.
- A camera-service installer manages the proven publisher and MediaMTX without
  changing firmware or silently replacing an existing installed capture command.

## Network boundary

OneSpace Connect removes the need for a separate Tailscale application. It
cannot create a public address through carrier NAT. The Pi must be reachable
from both devices. LAN works locally; remote access requires public IPv6 or a
suitable public IPv4/port-forward/DNS setup. No public relay was provisioned.
The remote path uses the Pi; general-purpose ICE/STUN/TURN hole punching is
not implemented. Video also needs reachable WebRTC media candidates/ports.

## Build contents

- `windows/`: Go engine and native Windows UI; `-legacy-ui` keeps the previous
  browser interface available for troubleshooting.
- `android/`: same `com.onespace.companion` Android Studio project. Use your
  original signing key and a version code above your actual installed maximum.
- `hub/`: Kali ARM64 Pi hub, bounded encrypted relay, Drive and update store.
- `bin/`: current packaged native Windows and Pi builds plus pinned MediaMTX.
- `android-builds/`: unsigned release APK, if included. It is not installable
  as a compatible update until signed with your original key.
- `VALIDATION.md`: current checks, blocked security scan and unrun hardware gates.
- `THIRD-PARTY.md`, `licenses/`: components and redistribution notices.

No user token, Google credential, signing key or pairing profile is distributed.
Google-readable Drive files are not an end-to-end encrypted cloud vault.
Android background restrictions remain; no zero-delay/zero-bug claim is made.
The original v0.8.0 package and test branch remain separate rollback material.
