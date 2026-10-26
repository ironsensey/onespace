**New preview feature:** Android APK Update Center. Windows locally hosts full APK releases; paired Android can check, download, verify and request installation with user approval. See `UPDATE-GUIDE.md`. This update system is not yet tested on Android hardware.

# OneSpace features and roadmap

Status keys: **Prototype** = implemented in source, with varying degrees of runtime verification; **Partial** = limited or requires user interaction; **Planned** = not implemented.

| Area | Feature | Status |
| --- | --- | --- |
| Device connections | Windows host and Android companion on LAN | Prototype |
| Secure enrollment | Password + PC approval + pinned TLS certificate + device revocation | Prototype |
| Peer-to-peer file transfer | Android→Windows HTTPS upload and Windows→Android queued downloads | Prototype |
| Shared folders | Permanent and Temporary (24-hour expiry) | Prototype |
| Windows clipboard | Android→Windows text and JPEG/PNG image paste formats | Prototype (Windows device test needed) |
| Windows→Android clipboard | Foreground request to pull Windows text clipboard | Prototype |
| Camera integration | Capture in companion and paste image on Windows | Prototype (device test needed) |
| Camera integration | Send newest camera photo from Android | Prototype (permission-dependent) |
| Always-on camera monitoring | Auto-transfer pictures taken in any Android camera app while companion is closed | Planned — OS background limits |
| Notes and tasks | Shared CRUD with manual refresh; local disk persistence | Prototype |
| Windows dashboard | Devices, files, activity, tasks, notes, controls, settings | Prototype |
| Android widget | One basic home-screen quick-launch widget | Prototype |
| Configurable widgets | Multiple specialized widgets and all-in-one layouts | Planned |
| Storage dashboard | Files list, manual cleanup via 24h expiry, saved folders | Partial (no category analytics) |
| Windows power actions | Lock, sleep, restart, shutdown after PC confirmation | Prototype (Windows device test needed) |
| Remote power control | Android→Windows requests with local approval and opt-in | Prototype (Windows device test needed) |
| Android media volume | On-device adjustment | Prototype |
| Windows media volume | Windows Core Audio via PowerShell COM bridge, controllable from Android | Prototype (Windows device test needed) |
| Windows→Android volume | Remote Android volume management from PC | Planned |
| Android power-off/reboot | Ordinary-app silent shutdown/reboot | Not supported on normal consumer Android devices |
| Android lock | App screen protected by biometric/device screen lock | Prototype |
| File/search/history | Basic received-file history, activity timeline | Partial |
| Clipboard history | Rich searchable clipboard timeline | Planned |
| Bluetooth handoff | Discover paired peripherals and switch Windows↔Android | Planned — accessory/OS-dependent |
| Unified notifications | Shared notification hub | Planned |
| Mouse/keyboard | Cross-device input | Planned |
| Discovery | Automatic mDNS/Bonjour discovery | Planned (manual local IP for now) |
| Remote networking | Different Wi-Fi networks with optional relay | Planned |
| Automation engine | Cross-device workflows, profiles, triggers | Planned |
| Cross-platform expansion | macOS, Linux, iOS clients | Planned |
| Battery/performance | Small event-driven server, no Android always-on background service | Partially implemented; no real-device benchmarks |

## Suggested milestones

**0.2 — Robust connectivity:** finish Android build, device testing, local IP auto-discovery, reconnect diagnostics, QA around network changes, large file transfers, better clipboard formats and clear pairing UI.

**0.3 — Signature experience:** user-opt-in photo monitoring with OS-compliant behavior, rate limits, clipboard conflict handling, searchable clipboard/photo history, retention preferences.

**0.4 — Productivity:** realtime task/note synchronization and conflict resolution; attachments, unified inbox, widgets and notifications.

**0.5 — Device controls:** verified Windows/Android volume and media, optional peripheral handoff via supported Bluetooth APIs, and capability-specific remote actions.

**0.6+ — Advanced ecosystem:** automation and profiles, activity continuity, input sharing, optional internet relay and more desktop/phone operating systems.
