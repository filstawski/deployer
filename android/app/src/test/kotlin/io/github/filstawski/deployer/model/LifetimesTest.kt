package io.github.filstawski.deployer.model

import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.State
import java.time.Instant
import kotlin.time.Duration
import kotlin.time.Duration.Companion.days
import kotlin.time.Duration.Companion.hours
import kotlin.time.Duration.Companion.minutes
import kotlin.time.Duration.Companion.seconds
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LifetimesTest {
    private val now = Instant.parse("2026-10-28T16:26:00Z")

    @Test fun formatsDurationsLikeTheCli() {
        // The same cases as domain.FormatDuration in Go.
        val cases = mapOf(
            30.seconds to "30s",
            45.minutes to "45m",
            1.hours to "1h",
            (2.hours + 14.minutes) to "2h 14m",
            1.days to "1d",
            (1.days + 4.hours + 30.minutes) to "1d 4h",
            -(3.hours) to "3h",
        )
        for ((d, want) in cases) assertEquals(want, formatDuration(d))
    }

    @Test fun labelsDurationsForPeople() {
        assertEquals("1 hour", durationLabel(1.hours))
        assertEquals("4 hours", durationLabel(4.hours))
        assertEquals("3 days", durationLabel(3.days))
        assertEquals("1 week", durationLabel(7.days))
        assertEquals("2 weeks", durationLabel(14.days))
        assertEquals("30 days", durationLabel(30.days))
        assertEquals("1h 30m", durationLabel(90.minutes))
    }

    @Test fun limitsExtensionsLikeTheController() {
        val max = 7.days
        val running = deployment(expiresAt = now.plusSeconds(2.hours.inWholeSeconds))
        assertEquals(7.days - 2.hours, running.extensionLimit(max, now))

        val expired = deployment(expiresAt = now.minusSeconds(60), state = State.Expired)
        assertEquals(7.days, expired.extensionLimit(max, now))

        val notReady = deployment(expiresAt = null, ttl = 1.days, state = State.Building)
        assertEquals(6.days, notReady.extensionLimit(max, now))

        val atLimit = deployment(expiresAt = now.plusSeconds(8.days.inWholeSeconds))
        assertEquals(Duration.ZERO, atLimit.extensionLimit(max, now))
    }

    @Test fun offersCommonExtensionsAndTheMaximum() {
        val d = deployment(expiresAt = now.plusSeconds((2.hours + 14.minutes).inWholeSeconds))
        val options = d.extendOptions(7.days, now)
        assertEquals(listOf("+1 hour", "+4 hours", "+1 day", "+3 days", "Max"), options.map { it.label })
        // 7d - 2h14m = 6d 21h 46m, less a spare minute, rounded down to hours.
        assertEquals(6.days + 21.hours, options.last().by)
        assertTrue(options.last().isMax)
    }

    @Test fun offersOnlyWhatFitsNearTheLimit() {
        val d = deployment(expiresAt = now.plusSeconds((7.days - 30.minutes).inWholeSeconds))
        val options = d.extendOptions(7.days, now)
        assertEquals(listOf(29.minutes), options.map { it.by })
        assertTrue(deployment(expiresAt = now.plusSeconds(7.days.inWholeSeconds)).extendOptions(7.days, now).isEmpty())
    }

    @Test fun computesTheNewExpiry() {
        val d = deployment(expiresAt = now.plusSeconds(3600))
        assertEquals(now.plusSeconds(3600 + 86400), d.expiryAfter(1.days, now))
        val expired = deployment(expiresAt = now.minusSeconds(3600))
        assertEquals("an expired lease restarts from now", now.plusSeconds(86400), expired.expiryAfter(1.days, now))
    }

    @Test fun reportsTheShareOfTheLeaseLeft() {
        val ready = now.minusSeconds(18.hours.inWholeSeconds)
        val d = deployment(expiresAt = now.plusSeconds(6.hours.inWholeSeconds), ttl = 1.days, readyAt = ready)
        assertEquals(0.25f, d.leaseRemaining(now)!!, 0.001f)
        assertFalse(d.extended)
        assertTrue(d.copy(expiresAt = now.plusSeconds(30 * 60)).expiresSoon(now))
    }

    @Test fun measuresExtendedLeasesAgainstTheirFullLength() {
        // Ready 18h ago with a 1-day lifetime, then extended by 2 days.
        val ready = now.minusSeconds(18.hours.inWholeSeconds)
        val d = deployment(expiresAt = now.plusSeconds((2.days + 6.hours).inWholeSeconds), ttl = 1.days, readyAt = ready)
        assertTrue(d.extended)
        assertEquals(3.days, d.leaseLength())
        assertEquals(0.75f, d.leaseRemaining(now)!!, 0.001f)
    }
}

fun deployment(
    expiresAt: Instant?,
    ttl: Duration = 1.days,
    state: State = State.Running,
    readyAt: Instant? = null,
) = Deployment(
    id = "dep_1", name = "feat-checkout", projectId = "prj_1", project = "storefront", ref = "feat/checkout",
    state = state, hostname = "feat-checkout.example.com", url = "https://feat-checkout.example.com",
    ttlSeconds = ttl.inWholeSeconds, createdBy = "tok_1", createdAt = Instant.EPOCH, updatedAt = Instant.EPOCH,
    readyAt = readyAt, expiresAt = expiresAt,
)
