# Independent OneSpace development

The first native-screen increment has been expanded into v0.9.0 Independent
Preview. See README.md, SETUP-v0.9.0.md and VALIDATION.md for the current build.

Implemented: native Windows control pages, isolated video component, built-in
end-to-end PC TLS relay, LAN preference and optional Pi LAN/remote addresses,
Windows OS network/resume notifications, Pi-hosted APK publication/download,
shared version codes, and managed camera service installation.

Still external setup/hardware gates: actual Pi reachability and firewall,
Google consent, original Android signing key, physical Windows native UI and
WebView2/Android lifecycle tests, Pi camera/power/memory/SD tests. General-purpose
STUN/TURN peer traversal and a hosted public relay are not provided. Native UI
uses Win32 controls rather than a WinUI 3 redesign. No security or performance
percentage claims are inferred from builds and fixture tests.
