package com.onespace.companion

import org.junit.Assert.*
import org.junit.Test

class RouteCandidatesTest {
    @Test fun verifiedLocalWinsWithoutRemoteProbe() {
        val probes = mutableListOf<String>()
        assertEquals("lan", RouteCandidates.choose("lan", "remote") { probes.add(it); true })
        assertEquals(listOf("lan"), probes)
    }
    @Test fun unreachableOrWrongIdentityLocalFallsBackOnlyAfterVerification() {
        val probes = mutableListOf<String>()
        assertEquals("remote", RouteCandidates.choose("lan", "remote") { probes.add(it); it == "remote" })
        assertEquals(listOf("lan", "remote"), probes)
        assertNull(RouteCandidates.choose("lan", "remote") { false })
    }
    @Test fun duplicateAndEmptyRoutesAreNotProbedTwice() {
        var count = 0
        assertNull(RouteCandidates.choose("same", "same") { count++; false })
        assertEquals(1, count)
        assertNull(RouteCandidates.choose("", "") { throw AssertionError("empty route") })
    }
}
