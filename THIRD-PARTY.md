# Third-party components

OneSpace owns its application, pairing, routing and permission logic. It uses
established components rather than custom cryptography or video codecs.

| Component | Version | Role | Licence / distribution |
|---|---|---|---|
| Go standard library | Toolchain recorded in validation | TLS, HTTP, networking, OS controls | Go BSD-style, included under licenses/ |
| OkHttp | 4.12.0 | Android HTTP and streaming with per-PC pinning | Apache-2.0, included under licenses/ |
| Okio | 3.6.0 | Bounded Android streaming | Apache-2.0; https://github.com/square/okio/blob/master/LICENSE.txt |
| Kotlin runtime | 2.2.20 | Android application runtime | Apache-2.0; https://github.com/JetBrains/kotlin/blob/v2.2.20/license/LICENSE.txt |
| go-webview2 | 56598839c808 | Windows video surface only | MIT, included under licenses/; local one-callback patch documented in its source |
| go-winloader | c1995be93bd1 | Loads the SDK video-component loader | ISC, included under licenses/ |
| golang.org/x/sys | 0.31.0 | Windows network notifications and OS interfaces | BSD-style, included under licenses/ |
| Microsoft WebView2Loader | Upstream wrapper's pinned SDK binary | Windows video component loader | Redistributable licence included under licenses/ |
| Microsoft WebView2 Runtime | User's maintained OS runtime | H.264/WebRTC playback | Microsoft runtime terms; not bundled or silently installed |
| MediaMTX | 1.21.2 | Pi live video and MP4 segments | MIT, included under licenses/; official archive checksum verified |
| GStreamer / libcamera | Existing Kali packages | Hardware camera capture | Supplied by the OS; not redistributed in this ZIP. Package/plugin licences vary. Existing working capture is preserved. |
| Google Drive API / OAuth | Service | Required large storage | Google service terms and the user's existing account/quota apply; no Gmail-reading permission |

Tailscale is optional compatibility support, not bundled or required by
OneSpace Connect. No third-party hosted messaging platform, cloud AI service
or VPN account is required for the built-in PC relay. Remote access still
requires a reachable Pi or another separately provisioned reachable endpoint.

Keep these notices with redistributed binaries. This inventory describes the
components in this build; it is not a guarantee that a provider or dependency
will remain available forever. Runtime and OS security updates remain necessary.
