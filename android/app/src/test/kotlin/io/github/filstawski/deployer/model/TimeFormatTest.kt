package io.github.filstawski.deployer.model

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import org.junit.Assert.assertEquals
import org.junit.Test

class TimeFormatTest {
    private val zone = ZoneId.of("Europe/Berlin")
    private val format = TimeFormat(zone, Locale.US, DateTimeFormatter.ofPattern("HH:mm"))
    // Wednesday, 28 October 2026, 16:26 in Berlin.
    private val now = Instant.parse("2026-10-28T15:26:00Z")

    @Test fun namesDaysRelativeToToday() {
        assertEquals("today at 18:40", format.at(Instant.parse("2026-10-28T17:40:00Z"), now))
        assertEquals("tomorrow at 18:40", format.at(Instant.parse("2026-10-29T17:40:00Z"), now))
        assertEquals("yesterday at 09:00", format.at(Instant.parse("2026-10-27T08:00:00Z"), now))
        assertEquals("Saturday at 18:40", format.at(Instant.parse("2026-10-31T17:40:00Z"), now))
        assertEquals("Nov 12 at 18:40", format.at(Instant.parse("2026-11-12T17:40:00Z"), now))
    }

    @Test fun usesTheLocalCalendarDay() {
        // 23:30 UTC is already tomorrow in Berlin.
        assertEquals("tomorrow at 00:30", format.at(Instant.parse("2026-10-28T23:30:00Z"), now))
    }

    @Test fun shortTimesFitSmallSpaces() {
        assertEquals("18:40", format.short(Instant.parse("2026-10-28T17:40:00Z"), now))
        assertEquals("tomorrow", format.short(Instant.parse("2026-10-29T17:40:00Z"), now))
        assertEquals("Sat", format.short(Instant.parse("2026-10-31T17:40:00Z"), now))
        assertEquals("Nov 12", format.short(Instant.parse("2026-11-12T17:40:00Z"), now))
    }

    @Test fun saysHowLongAgo() {
        assertEquals("just now", format.ago(now.minusSeconds(20), now))
        assertEquals("3h 2m ago", format.ago(now.minusSeconds(3 * 3600 + 120), now))
        assertEquals("2d 4h ago", format.ago(now.minusSeconds(2 * 86400 + 4 * 3600), now))
    }
}
