package io.github.filstawski.deployer.notify

import io.github.filstawski.deployer.api.ApiError
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.Operation
import io.github.filstawski.deployer.api.OperationStatus
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.model.deployment
import java.time.Instant
import kotlin.time.Duration.Companion.minutes
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class WatcherTest {
    private val now = Instant.parse("2026-10-28T15:00:00Z")
    private fun d(state: State, op: String? = "op_1", expiresIn: Long? = 86_400, commit: String = "aaa") =
        deployment(expiresAt = expiresIn?.let { now.plusSeconds(it) }, state = state).copy(operationId = op, commit = commit)

    private fun op(kind: String, status: OperationStatus, result: String? = null, error: ApiError? = null, id: String = "op_2") =
        Operation(id, kind, status, error = error, result = result?.let { Json.parseToJsonElement(it) }, createdAt = now)

    private fun seen(d: Deployment, done: Boolean = true) = mapOf(d.id to Seen(d.state, d.commit, d.operationId, done))

    @Test fun theFirstCheckOnlyRemembers() {
        val (alerts, next) = watch(null, listOf(d(State.Failed)), emptyMap(), now)
        assertTrue(alerts.isEmpty())
        assertEquals(State.Failed, next.values.single().state)
    }

    @Test fun tellsWhenABuildBecomesReady() {
        val before = d(State.Building, expiresIn = null)
        val (alerts, _) = watch(seen(before), listOf(d(State.Running)), emptyMap(), now)
        assertEquals(listOf(Alert.Ready::class), alerts.map { it::class })
    }

    @Test fun tellsAboutDeploymentsStartedAndFinishedBetweenChecks() {
        // Deployed from the CLI after one check, ready or failed by the next.
        val known = d(State.Running).copy(id = "dep_known")
        val ready = d(State.Running).copy(id = "dep_new", name = "new")
        val failed = d(State.Failed).copy(id = "dep_broken", name = "broken")
        val building = d(State.Building, expiresIn = null).copy(id = "dep_slow", name = "slow")
        val (alerts, _) = watch(seen(known), listOf(known, ready, failed, building), emptyMap(), now)
        assertEquals(listOf("new" to Alert.Ready::class, "broken" to Alert.Failed::class), alerts.map { it.deployment.name to it::class })
    }

    @Test fun anOperationThatCouldNotBeLookedUpIsLookedUpAgain() {
        val updating = d(State.Running, op = "op_2")
        val (alerts, next) = watch(seen(d(State.Running)), listOf(updating), emptyMap(), now)
        assertTrue(alerts.isEmpty())
        assertEquals(listOf("op_2"), operationsToCheck(next, listOf(updating)))
    }

    @Test fun anUpdateTellsOnceHowItEnded() {
        // Updating a stopped deployment that then failed: one alert, about the update.
        val stopped = d(State.Stopped)
        val failedAfter = d(State.Failed, op = "op_2").copy(error = ApiError("HEALTHCHECK_FAILED", "no answer"))
        val failure = op("deployment.update", OperationStatus.Failed, error = ApiError("HEALTHCHECK_FAILED", "no answer"))
        val (alerts, _) = watch(seen(stopped), listOf(failedAfter), mapOf("op_2" to failure), now)
        assertEquals(listOf(Alert.UpdateFailed::class), alerts.map { it::class })

        // Updating a stopped deployment that then started: told as an update, not also as ready.
        val startedAfter = d(State.Running, op = "op_2")
        val success = op("deployment.update", OperationStatus.Succeeded, """{"previous_commit":"aaa","commit":"bbb","changed":true}""")
        val (updated, _) = watch(seen(d(State.Starting)), listOf(startedAfter), mapOf("op_2" to success), now)
        assertEquals(listOf(Alert.Updated::class), updated.map { it::class })
    }

    @Test fun tellsWhenADeploymentFails() {
        val failed = d(State.Failed).copy(error = ApiError("HEALTHCHECK_FAILED", "no answer"))
        val (alerts, _) = watch(seen(d(State.Starting)), listOf(failed), emptyMap(), now)
        assertEquals("no answer", (alerts.single() as Alert.Failed).error?.message)
        // Only once.
        val (again, _) = watch(seen(failed), listOf(failed), emptyMap(), now)
        assertTrue(again.isEmpty())
    }

    @Test fun tellsHowAnUpdateEndedWhileTheDeploymentKeptRunning() {
        val running = d(State.Running)
        val updating = running.copy(operationId = "op_2")
        val succeeded = op("deployment.update", OperationStatus.Succeeded, """{"previous_commit":"aaa","commit":"bbbbbbbbb","changed":true}""")
        val (alerts, next) = watch(seen(running), listOf(updating), mapOf("op_2" to succeeded), now)
        assertEquals("bbbbbbbbb", (alerts.single() as Alert.Updated).commit)
        assertTrue(next.values.single().operationDone)

        val failed = op("deployment.update", OperationStatus.Failed, error = ApiError("BUILD_FAILED", "npm ci failed"))
        val (failure, _) = watch(seen(running), listOf(updating), mapOf("op_2" to failed), now)
        assertEquals("npm ci failed", (failure.single() as Alert.UpdateFailed).error?.message)
    }

    @Test fun followsAnUpdateAcrossChecks() {
        val updating = d(State.Running, op = "op_2")
        val running = op("deployment.update", OperationStatus.Running)
        val (first, afterFirst) = watch(seen(d(State.Running)), listOf(updating), mapOf("op_2" to running), now)
        assertTrue(first.isEmpty())
        assertEquals(listOf("op_2"), operationsToCheck(afterFirst, listOf(updating)))
        val unchanged = op("deployment.update", OperationStatus.Succeeded, """{"previous_commit":"aaa","commit":"aaa","changed":false}""")
        val (second, afterSecond) = watch(afterFirst, listOf(updating), mapOf("op_2" to unchanged), now)
        assertTrue("nothing changed, nothing to tell", second.isEmpty())
        assertTrue(operationsToCheck(afterSecond, listOf(updating)).isEmpty())
    }

    @Test fun warnsOnceBeforeALeaseEnds() {
        val soon = d(State.Running, expiresIn = 40.minutes.inWholeSeconds)
        val (alerts, next) = watch(seen(soon), listOf(soon), emptyMap(), now)
        assertEquals(40.minutes, (alerts.single() as Alert.Expiring).left)
        val (again, _) = watch(next, listOf(soon), emptyMap(), now.plusSeconds(600))
        assertTrue(again.isEmpty())
        // Extended, then close to the end again: a new warning.
        val extended = soon.copy(expiresAt = now.plusSeconds(7_200))
        val (later, _) = watch(next, listOf(extended), emptyMap(), now.plusSeconds(6_000))
        assertEquals(1, later.size)
    }

    @Test fun aWarningHeldBackWhileTheAppIsOpenComesLater() {
        val soon = d(State.Running, expiresIn = 40.minutes.inWholeSeconds)
        val (held, next) = watch(seen(soon), listOf(soon), emptyMap(), now, warn = false)
        assertTrue(held.isEmpty())
        val (later, _) = watch(next, listOf(soon), emptyMap(), now.plusSeconds(300))
        assertEquals(1, later.size)
    }

    @Test fun doesNotWarnAboutStoppedOrDistantDeployments() {
        val stopped = d(State.Stopped, expiresIn = 600)
        val distant = d(State.Running, expiresIn = 7_200).copy(id = "dep_2")
        val (alerts, _) = watch(seen(stopped) + seen(distant), listOf(stopped, distant), emptyMap(), now)
        assertTrue(alerts.isEmpty())
    }
}
