# OneSpace v0.6.2 — implemented changes

This is a targeted clipboard-receive developer update, not implementation of every item in the earlier architecture roadmap.

- Enabling Android Receive Clipboard now activates its visible background connection. Receiving copied text/photos no longer also requires enabling the separate generic-file Inbox toggle. Restarting/unlocking the app reconciles an already enabled connection.
- Windows supports additive text/file/image inbox lanes. Android receives text independently of larger images/files, while clipboard-only users do not repeatedly poll for unrelated PDFs/files. Legacy clients can still request the original unfiltered inbox.
- Manual and background reception share per-kind synchronization and durable delivery receipts, preventing duplicate downloads during concurrent fetching or lost acknowledgements. Confirmed receipts expire after 24 hours; unacknowledged receipts remain available for retry.
- An older, slow image cannot replace a newer received text/photo according to the PC item's creation timestamp. Notification updates use a single, quiet existing notification rather than re-alerting on each update.
- Receiver registration is private on modern Android and signature-permission protected on older supported Android versions. Certificate pinning, encrypted transport, approval-based pairing, device tokens and update signer checks remain in place.
- Inbox settings changes interrupt obsolete requests, and service shutdown cancels active inbox network operations. Network-loss callbacks are handled. Camera event bursts retain their earliest scheduled scan rather than continually moving it later.
- Windows rechecks permissions for queued delivery and content access. Revocation wakes and invalidates existing long polls. Acknowledgements report persistence failure and roll back their in-memory received flag.
- Pending queue lookup builds a temporary item index only when relevant work exists, replacing repeated nested history scans. The measured 10,000-job synthetic lookup improved from about 146 ms to about 3.8 ms, at the cost of extra temporary index memory. This is not a phone/battery benchmark.
- Windows clipboard writes use a real window handle on the same OS thread. Bitmap-to-PNG work runs after releasing the clipboard lock. Image dimensions are checked before expensive decoding. Clipboard-request responses distinguish stored data from successfully applied clipboard data; Android manual photo sharing reports that distinction.
- The Windows APK publish button disables the actual button while publishing. Android update dialogs cannot stack; automatic prompt history is written when an offer is actually shown.
- Both platform version labels are 0.6.2. Android's application ID remains `com.onespace.companion`. Explicit APK/bundle/install builds reserve increasing local version codes; minimum 10, with a configurable known-published floor and a script to read the existing PC publisher. The signed APK update endpoint and manifest format are unchanged.
- Added permanent Go regressions and six Android JVM policy tests. Included the Gradle 8.13 wrapper with checksum verification and a complete Android Studio guide.

## Preserved compatibility

Keep the existing Android signing key, Windows data root and TLS identity. Android Keystore alias, app preference names, update-provider authority, package ID, existing Windows state format, pairing flow and update URLs are retained. New Android delivery receipts use a separate local preference store; existing delivery IDs are recognized. No user files are bulk deleted or migrated by this update.

## Still outstanding

Native Windows clipboard runtime tests, real Android lifecycle/network/notification tests, battery measurements and a same-key in-place upgrade were not possible on this cloud machine. Android lint retains 12 warnings. PowerShell/batch helpers have not been executed on Windows. The Windows executable is unsigned and cross-built for Windows 10/11 x64.

Further work from the audit remains, including a fully durable/idempotent camera upload queue, broader discovery/network binding support, distributed release-code allocation, DPAPI token protection, richer native Windows UI, and broader Android instrumentation coverage. Large photos, camera finalization, OS/OEM restrictions, unavailable networks, and unsupported clipboard formats still impose limits. No zero latency or permanent Android background execution is promised.
