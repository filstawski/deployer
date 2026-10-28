package io.github.filstawski.deployer.model

import org.junit.Assert.assertEquals
import org.junit.Test

class LogBufferTest {
    @Test fun joinsPiecesThatEndMidLine() {
        val b = LogBuffer()
        b.append("#1 load Dock")
        assertEquals(listOf("#1 load Dock"), b.snapshot().map { it.text })
        b.append("erfile\n#2 RUN npm ci\n#3 ")
        assertEquals(listOf("#1 load Dockerfile", "#2 RUN npm ci", "#3 "), b.snapshot().map { it.text })
        // Line numbers stay stable as lines complete, so lists can key on them.
        assertEquals(listOf(0L, 1L, 2L), b.snapshot().map { it.number })
    }

    @Test fun keepsTheNewestLines() {
        val b = LogBuffer(limit = 2)
        b.replace("a\nb\nc\n")
        assertEquals(listOf("b", "c"), b.snapshot().map { it.text })
    }

    @Test fun replaceStartsOver() {
        val b = LogBuffer()
        b.append("old\n")
        b.replace("new\r\n")
        assertEquals(listOf("new"), b.snapshot().map { it.text })
    }

    @Test fun colorsErrorsAndWarnings() {
        assertEquals(Tone.Bad, classify("Error: card_declined"))
        assertEquals(Tone.Bad, classify("#8 ERROR: process did not complete"))
        assertEquals(Tone.Busy, classify("warn payments: using a test mode key"))
        assertEquals(null, classify("GET /checkout 200 in 48ms"))
        assertEquals("words inside others don't count", null, classify("GET /errors-page 200"))
    }
}
