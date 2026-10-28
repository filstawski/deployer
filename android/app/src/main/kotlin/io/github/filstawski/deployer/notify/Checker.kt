package io.github.filstawski.deployer.notify

import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.data.KeyValueStore
import java.time.Instant
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json

data class CheckResult(
    val status: ServerStatus,
    val deployments: List<Deployment>,
    val alerts: List<Alert>,
)

/** Checks one server: reads its deployments, compares them with the last check, and remembers them. */
class Checker(private val store: KeyValueStore) {

    /** Checks a server; [warn] is false when expiry warnings can't be shown yet, so they wait. */
    suspend fun check(serverId: String, api: DeployerApi, warn: Boolean = true, now: Instant = Instant.now()): CheckResult {
        val status = api.server()
        val deployments = api.deployments()
        val previous = load(serverId)
        val operations = operationsToCheck(previous, deployments).mapNotNull { id ->
            try {
                id to api.operation(id)
            } catch (_: ApiException) {
                null
            }
        }.toMap()
        val (alerts, seen) = watch(previous, deployments, operations, now, warn)
        store.put(key(serverId), json.encodeToString(serializer, seen))
        return CheckResult(status, deployments, alerts)
    }

    /** Forgets a server that was removed from the phone. */
    fun forget(serverId: String) = store.put(key(serverId), null)

    private fun load(serverId: String): Map<String, Seen>? =
        store.get(key(serverId))?.let { runCatching { json.decodeFromString(serializer, it) }.getOrNull() }

    private fun key(serverId: String) = "seen:$serverId"

    private companion object {
        val json = Json { ignoreUnknownKeys = true }
        val serializer = MapSerializer(String.serializer(), Seen.serializer())
    }
}
