package com.onespace.companion

import org.junit.Assert.*
import org.junit.Test

class InboxPolicyTest {
    @Test fun textNeverWaitsInTheFileLane() {
        assertTrue(InboxPolicy.matchesLane("text", "text"))
        assertFalse(InboxPolicy.matchesLane("text", "files"))
        assertFalse(InboxPolicy.matchesLane("file", "text"))
        assertTrue(InboxPolicy.matchesLane("file", "files"))
    }
    @Test fun manualFetchAcceptsBothKinds() {
        assertTrue(InboxPolicy.matchesLane("text", ""))
        assertTrue(InboxPolicy.matchesLane("file", ""))
    }
    @Test fun aSlowOlderPhotoCannotReplaceNewerText() {
        assertFalse(InboxPolicy.acceptsClipboard(2000L, 1000L))
        assertTrue(InboxPolicy.acceptsClipboard(2000L, 3000L))
    }
    @Test fun retriesOfTheSameTimestampRemainRecoverable() {
        assertTrue(InboxPolicy.acceptsClipboard(2000L, 2000L))
    }
    @Test fun ordinaryDownloadsDoNotBecomeClipboardItems() {
        assertFalse(InboxPolicy.clipboardSupported("file", "private-document.pdf"))
        assertTrue(InboxPolicy.clipboardSupported("file", "camera.JPG"))
        assertTrue(InboxPolicy.clipboardSupported("text", "clipboard.txt"))
    }
    @Test fun offlineBackoffIsBounded() {
        assertEquals(3000L, InboxPolicy.retryDelay(1500L))
        assertEquals(30000L, InboxPolicy.retryDelay(24000L))
        assertEquals(30000L, InboxPolicy.retryDelay(30000L))
    }
}
