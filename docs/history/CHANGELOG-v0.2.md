# OneSpace v0.2 proposed update

## Android

- Reorganized native Android UI with dedicated Home, Clipboard, Camera, Files, Workspace, Devices, Controls, Updates, Settings pages.
- Four local color themes: Midnight, Forest, Plum, Graphite.
- Android active-app clipboard listener and 12-second check for Windows text (opt-in, OS focus-restricted).
- Opt-in visible connected-device foreground service for new camera images and 60-second inbox checks (best effort; Android may stop).
- Default permanent and 24-hour temporary download paths displayed in Files.
- Reuses original keystore-compatible v0.2 full-APK update flow; app ID unchanged; version code incremented to 2.
- Basic Android widget retained from v0.1; additional widgets still to be built.

## Windows

- Server-enforced per-device permissions: files, clipboard, camera, tasks, notes, volume, power and updates.
- Queues Android-originated shared text and camera photos to other paired Android devices via device-specific inboxes, respecting files permission.
- Run-at-login toggle for the current user via Windows registry.
- Launch in Edge app-style window where available; Windows network service continues to run independently.
- Shows active app-data directory under Windows Settings.

## Engineering status

- Go tests pass, including new permission-denial and cross-device fan-out tests.
- Windows amd64 EXE cross-compiled with Go 1.23.2.
- Dashboard JavaScript passes `node --check`.
- Android Kotlin code NOT compiled or device-tested in this environment. Treat as an experimental update until you run a debug build and hardware validation.
