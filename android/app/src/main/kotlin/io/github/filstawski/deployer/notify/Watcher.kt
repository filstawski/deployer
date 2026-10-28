package io.github.filstawski.deployer.notify

import io.github.filstawski.deployer.api.ApiError
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.Operation
import io.github.filstawski.deployer.api.OperationStatus
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.api.UpdateResult
import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.model.timeLeft
import java.time.Instant
import kotlin.time.Duration
import kotlin.time.Duration.Companion.hours
import kotlinx.serialization.Serializable

/** What the last check saw of a deployment, to tell what changed since. */
@Serializable
data class Seen(
    val state: State,
    val commit: String = "",
    val operationId: String? = null,
    /** The operation had finished, so it needn't be looked up again. */
    val operationDone: Boolean = true,
    /** The expiry already warned about, so each lease is warned about once. */
    val warnedExpiry: String? = null,
)

/** Something worth a notification. */
sealed interface Alert {
    val deployment: Deployment

    data class Ready(override val deployment: Deployment) : Alert
    data class Failed(override val deployment: Deployment, val error: ApiError?) : Alert
    data class Updated(override val deployment: Deployment, val commit: String) : Alert
    data class UpdateFailed(override val deployment: Deployment, val error: ApiError?) : Alert
    data class Expiring(override val deployment: Deployment, val left: Duration) : Alert
}

/** How long before expiry to warn. */
val WARN_BEFORE_EXPIRY = 1.hours

/**
 * The operations a check needs: those that are new, or weren't finished last
 * time, since an update keeps the deployment running and only its operation
 * tells how it ended.
 */
fun operationsToCheck(previous: Map<String, Seen>?, deployments: List<Deployment>): List<String> =
    deployments.mapNotNull { d ->
        val id = d.operationId ?: return@mapNotNull null
        val seen = previous?.get(d.id)
        id.takeIf { seen == null || seen.operationId != id || !seen.operationDone }
    }

/**
 * Compares deployments with what the last check saw and returns what to tell,
 * with what to remember for next time. The first check of a server only
 * remembers, except for warnings about expiry, which are still useful.
 *
 * Without [warn], as while the app is open and shows changes itself, expiry
 * warnings wait: they come once the app is closed, if they still apply.
 */
fun watch(
    previous: Map<String, Seen>?,
    deployments: List<Deployment>,
    operations: Map<String, Operation>,
    now: Instant,
    warn: Boolean = true,
): Pair<List<Alert>, Map<String, Seen>> {
    val alerts = mutableListOf<Alert>()
    val next = mutableMapOf<String, Seen>()
    for (d in deployments) {
        val seen = previous?.get(d.id)
        val operation = d.operationId?.let { operations[it] }
        val operationDone = when {
            d.operationId == null -> true
            operation != null -> operation.finished
            seen?.operationId == d.operationId -> seen.operationDone
            // A new operation that couldn't be looked up: look again next time.
            else -> false
        }

        if (previous != null) {
            val update = updateAlert(d, seen, operation)
            when {
                // An update's outcome says more than the state it left behind.
                update != null -> alerts += update
                // Unseen means started since the last check, which it may have finished already.
                d.state == State.Running && (seen == null || seen.state.inProgress) -> alerts += Alert.Ready(d)
                d.state == State.Failed && seen?.state != State.Failed -> alerts += Alert.Failed(d, d.error)
            }
        }

        var warned = seen?.warnedExpiry
        val left = d.timeLeft(now)
        val expires = d.expiresAt?.toString()
        if (warn && d.state == State.Running && left != null && left > Duration.ZERO && left <= WARN_BEFORE_EXPIRY && warned != expires) {
            alerts += Alert.Expiring(d, left)
            warned = expires
        }
        next[d.id] = Seen(d.state, d.commit, d.operationId, operationDone, warned)
    }
    return alerts to next
}

/** A status this app doesn't know won't change into one it does; stop waiting for it. */
private val Operation.finished: Boolean get() = status.done || status == OperationStatus.Unknown

/** How an update ended, when it finished since the last check, started here or anywhere else. */
private fun updateAlert(d: Deployment, seen: Seen?, operation: Operation?): Alert? {
    if (operation == null || operation.kind != "deployment.update" || !operation.finished) return null
    if (seen != null && seen.operationId == operation.id && seen.operationDone) return null
    return when (operation.status) {
        OperationStatus.Succeeded -> {
            val result = operation.result?.let {
                runCatching { DeployerApi.json.decodeFromJsonElement(UpdateResult.serializer(), it) }.getOrNull()
            }
            // Nothing new was pushed: nothing to tell.
            if (result?.changed == false) null else Alert.Updated(d, result?.commit ?: d.commit)
        }
        OperationStatus.Failed, OperationStatus.Cancelled -> Alert.UpdateFailed(d, operation.error)
        else -> null
    }
}
