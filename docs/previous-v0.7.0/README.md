# OneSpace Pi Hub Preview — 0.7.0-preview

Start with [START-HERE.md](START-HERE.md), then [PI-SETUP.md](PI-SETUP.md). This is a separate development preview based on the v0.6.2 source available in this chat. Later v0.11/v2 sources mentioned in the handoff were not supplied; this package does not replace or certify them.

- `bin/onespace-hub-linux-arm64`: headless Kali ARM64 / Raspberry Pi 3 hub.
- `bin/OneSpace-Windows-Hub-Preview.exe`: Windows x64 preview with Hub, Live Camera and Media Connect pages. GUI subsystem avoids a console window; diagnostic logs live in the existing OneSpace profile.
- `android/`: complete Android Studio project, unchanged application ID, original signing identity required for upgrades.
- `hub/`: Go hub, authenticated camera WHEP gateway, live-only MediaMTX example, systemd units and installer.
- `tests/`: reproducible protocol/integration checks with no physical-camera/paste claims.
- `docs/PROTOCOL-AND-SECURITY.md`: implemented boundaries and deferred features.

Recording is OFF. No video is saved, and no SSD is needed for live viewing. Small bounded hub metadata is stored on the existing OS storage. Direct LAN sharing remains independent of the Pi/internet. Private-overlay connectivity requires separately provisioning Tailscale or an equivalent supported private network.

The Pi broker handles short text and media commands. Files/photos/legacy workspace and updates use the existing direct PC endpoint, optionally over an explicitly verified overlay route. Shared hub notes/tasks, automatic multi-route selection, brokered file relay, recording, AI and full native WinUI/Compose rewrites are not implemented. The available UI is extended without inventing missing later source.

No signing keystores, device tokens, camera footage, pairing state or prebuilt Android APK are included. Do not uninstall the installed Android app or reset existing Windows data to install this preview. Hardware performance, native clipboard, real WebRTC playback, battery and signed in-place upgrade still need device validation.
