package com.onespace.companion

import org.junit.Assert.*
import org.junit.Test

class HubPolicyTest {
    @Test fun hostDefaultsToTheHubPort() {
        assertEquals("https://pi.tailnet.ts.net:47603", HubPolicy.baseUrl("pi.tailnet.ts.net"))
        assertEquals("https://100.101.102.103:47603", HubPolicy.baseUrl("100.101.102.103:47603"))
    }
    @Test fun credentialAndPathInjectionAreRejected() {
        for (bad in listOf("user:password@host", "pi/other", "pi?token=secret", "pi#fragment", "pi:0", "https://pi")) {
            try { HubPolicy.baseUrl(bad); fail("accepted invalid address") } catch (_: IllegalArgumentException) {} catch (_: java.net.URISyntaxException) {}
        }
    }
    @Test fun cameraDeleteCannotEscapeThePinnedHub() {
        val base = "https://pi:47603"
        assertEquals("/hub/v1/camera/session/abc-123", HubPolicy.sessionPath(base, "/hub/v1/camera/session/abc-123"))
        for (bad in listOf("https://evil:47603/hub/v1/camera/session/a", "http://pi:47603/hub/v1/camera/session/a", "/hub/v1/devices", "/hub/v1/camera/session/a?extra=1", "//evil/hub/v1/camera/session/a")) {
            try { HubPolicy.sessionPath(base, bad); fail("accepted foreign session") } catch (_: IllegalArgumentException) {}
        }
    }
    @Test fun textLimitsCountUtf8Bytes() {
        assertTrue(HubPolicy.validText("a".repeat(16384)))
        assertFalse(HubPolicy.validText("a".repeat(16385)))
        assertFalse(HubPolicy.validText("界".repeat(6000)))
        assertFalse(HubPolicy.validText("  "))
    }
    @Test fun deviceIdsMatchHubEnrollmentRules() {
        assertTrue(HubPolicy.validTarget("windows_1"))
        assertFalse(HubPolicy.validTarget("windows/other"))
        assertFalse(HubPolicy.validTarget("pc.other"))
        assertFalse(HubPolicy.validTarget("a".repeat(65)))
    }
    @Test fun fingerprintFormattingDoesNotAcceptArbitraryCharacters() {
        assertEquals("a".repeat(64), HubPolicy.normalizedPin(("AA:".repeat(31)+"AA")))
        try { HubPolicy.normalizedPin("z"+"a".repeat(64)); fail("accepted invalid pin") } catch (_: IllegalArgumentException) {}
    }
}
