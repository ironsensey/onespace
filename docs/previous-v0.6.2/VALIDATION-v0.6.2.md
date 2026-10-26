# OneSpace v0.6.2 validation

Completed on the cloud Linux development machine. This is a developer package, not a native-device production-release certification.

| Check | Result |
|---|---|
| Go unit/integration/regression tests with race detector, fresh run | 18 passed; 0 failed/skipped |
| Go vet | Passed |
| Windows 10/11 x64 executable cross-build | Passed, using Go 1.27.2 |
| Real local server smoke test | TLS verification, pairing approval, authenticated long poll, download and acknowledgement passed |
| Embedded dashboard JavaScript syntax | Passed |
| Android debug compilation/APK assembly through supplied wrapper | Passed |
| Android release compilation/APK assembly | Passed; unsigned because the existing private release key is not available here |
| Android JVM policy tests | 6 passed; 0 failed/skipped |
| Android debug and release lint | Both passed with 0 errors and 12 warnings |
| Debug APK package/name/code | com.onespace.companion, 0.6.2, code 12 |
| Unsigned release APK package/name/code | com.onespace.companion, 0.6.2, code 13 |
| Debug APK signature verification | APK signature scheme v2 verified; compatibility with the installed signer remains unknown |
| Automatic code management | Consecutive build codes 10, 11, 12 and 13 observed; old explicit code 10 rejected without resetting the counter |

The clean ZIP has a minimum code of 10 and known-published floor 9; its first local build reserves 10 or a higher code after you refresh the floor. The cloud's private build counter and validation APKs are excluded. Keep your own counter thereafter and use the original signing key for upgrades. A higher code alone does not make a differently signed APK compatible.

The original audit failures for queued clipboard permission and acknowledgement persistence now pass as permanent regressions. Added tests cover independent text/file/image lanes, revocation of sleeping polls and content permission enforcement.

Android warnings cover selected-photo access, backup/transfer rules, the intentional custom pinning trust manager, older target/API calls and UI resources. TLS verification remains enabled. The pre-33 receiver branch is protected by a signature-only app permission; its narrowly scoped lint annotation documents that protection.

Unrun: real Windows clipboard runtime, physical Android service/focus/media/network behavior, idle battery consumption, PowerShell/batch helpers on Windows, and a same-key in-place upgrade preserving installed app data. The Android tests are JVM policy tests, not instrumentation or hardware tests. No end-to-end speed or battery improvement is claimed from the local microbenchmarks.

Build tooling: Gradle 8.13 (distribution SHA-256 verified), AGP 8.13.0, Kotlin 2.2.20, SDK 36/build-tools 35.0.0, JDK 21 with Java/Kotlin target 17. App ID, signing checks, pairing data format, provider authority and update endpoints were preserved.
