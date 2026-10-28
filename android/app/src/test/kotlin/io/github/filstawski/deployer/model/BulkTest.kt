package io.github.filstawski.deployer.model

import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.State
import java.time.Instant
import java.util.concurrent.atomic.AtomicInteger
import kotlin.time.Duration.Companion.days
import kotlin.time.Duration.Companion.hours
import kotlin.time.Duration.Companion.minutes
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class BulkTest {
    private val now = Instant.parse("2026-10-28T15:00:00Z")

    @Test fun runsEveryItemAndKeepsFailuresApart() = runBlocking {
        val running = AtomicInteger()
        val most = AtomicInteger()
        val outcomes = runEach((1..10).toList(), parallel = 3) { n ->
            most.accumulateAndGet(running.incrementAndGet(), ::maxOf)
            delay(20)
            running.decrementAndGet()
            if (n % 4 == 0) throw ApiException("INVALID_STATE", "no $n")
            n * 10
        }
        assertEquals((1..10).toList(), outcomes.map { it.item })
        assertEquals(listOf(4, 8), outcomes.filter { it.error != null }.map { it.item })
        assertEquals(30, outcomes.first { it.item == 3 }.result)
        assertTrue("at most 3 at a time, but ${most.get()}", most.get() <= 3)
    }

    @Test fun actionsApplyWhereTheServerWouldAllowThem() {
        val running = deployment(now.plusSeconds(3_600)).copy(image = "sha256:x")
        val stopped = deployment(now.plusSeconds(3_600), state = State.Stopped).copy(image = "sha256:x")
        val neverBuilt = deployment(null, state = State.Failed)
        assertEquals(listOf(BulkAction.Stop, BulkAction.Update, BulkAction.Extend, BulkAction.Destroy), BulkAction.entries.filter { it.appliesTo(running) })
        assertEquals(listOf(BulkAction.Start, BulkAction.Update, BulkAction.Extend, BulkAction.Destroy), BulkAction.entries.filter { it.appliesTo(stopped) })
        assertTrue(BulkAction.Start.appliesTo(neverBuilt).not())
    }

    @Test fun extendsEachAsFarAsItMay() {
        val roomy = deployment(now.plusSeconds(1.days.inWholeSeconds))
        val nearLimit = deployment(now.plusSeconds((7.days - 2.hours).inWholeSeconds))
        val atLimit = deployment(now.plusSeconds(7.days.inWholeSeconds))
        assertEquals(1.days, roomy.extensionFor(1.days, 7.days, now))
        // 2h left before the limit, less a spare minute.
        assertEquals(1.hours + 59.minutes, nearLimit.extensionFor(1.days, 7.days, now))
        assertEquals(1.hours + 59.minutes, nearLimit.extensionFor(null, 7.days, now))
        assertEquals(6.days - 1.hours, roomy.extensionFor(null, 7.days, now))
        assertNull(atLimit.extensionFor(1.days, 7.days, now))
    }

    @Test fun namesReadNaturally() {
        assertEquals("a", names(listOf("a")))
        assertEquals("a and b", names(listOf("a", "b")))
        assertEquals("a, b and c", names(listOf("a", "b", "c")))
        assertEquals("a, b, c, d and 2 more", names(listOf("a", "b", "c", "d", "e", "f")))
        assertEquals("1 deployment", deployments(1))
        assertEquals("3 deployments", deployments(3))
    }
}
