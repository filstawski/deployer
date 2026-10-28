package io.github.filstawski.deployer.model

import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.State
import java.time.Instant
import kotlin.time.Duration
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit

/** One item's result in a batch: [result], or the [error] that stopped it. */
data class Outcome<T, R>(val item: T, val result: R?, val error: ApiException?)

/**
 * Runs [action] for every item, [parallel] at a time. One failure doesn't
 * stop the rest; every item gets an outcome, in the order given.
 */
suspend fun <T, R> runEach(items: List<T>, parallel: Int = 4, action: suspend (T) -> R): List<Outcome<T, R>> = coroutineScope {
    val permits = Semaphore(parallel)
    items.map { item ->
        async {
            permits.withPermit {
                try {
                    Outcome<T, R>(item, action(item), null)
                } catch (e: ApiException) {
                    Outcome<T, R>(item, null, e)
                }
            }
        }
    }.awaitAll()
}

/** What can be done to several deployments at once. */
enum class BulkAction(val verb: String, val doing: String, val done: String) {
    Start("Start", "Starting", "Started"),
    Stop("Stop", "Stopping", "Stopped"),
    Update("Update", "Updating", "Updated"),
    Extend("Extend", "Extending", "Extended"),
    Destroy("Destroy", "Destroying", "Destroyed");

    /** Whether the action makes sense for [d] now; the server has the final word. */
    fun appliesTo(d: Deployment): Boolean = when (this) {
        Start -> d.canStart
        Stop -> d.canStop
        Update -> d.canUpdate
        Extend, Destroy -> d.state != State.Destroyed
    }
}

/**
 * How much to extend one of several deployments by: the [choice], or its
 * maximum when that is less or when the choice is null ("as long as
 * allowed"). Null when the deployment is already at the server's limit.
 */
fun Deployment.extensionFor(choice: Duration?, maxTtl: Duration, now: Instant): Duration? {
    val most = extendOptions(maxTtl, now).lastOrNull()?.by ?: return null
    return if (choice == null) most else minOf(choice, most)
}

/** "3 deployments", "1 deployment". */
fun deployments(count: Int) = if (count == 1) "1 deployment" else "$count deployments"

/** "a, b, and c", or the first few and how many more. */
fun names(names: List<String>, show: Int = 4): String = when {
    names.size <= 1 -> names.joinToString()
    names.size <= show -> names.dropLast(1).joinToString(", ") + " and " + names.last()
    else -> names.take(show).joinToString(", ") + " and ${names.size - show} more"
}
