package io.github.filstawski.deployer.notify

import android.content.Context
import androidx.glance.appwidget.updateAll
import io.github.filstawski.deployer.AppContainer
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.OperationStatus
import io.github.filstawski.deployer.widget.DeploymentsWidget
import kotlin.time.Duration.Companion.seconds
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** Checks every saved server, notifies about what changed, and refreshes the widget. */
class Monitor(private val context: Context, private val container: AppContainer) {
    // Periodic and follow-up checks may overlap; each must start from what the
    // one before it remembered, or both would tell the same news.
    private val lock = Mutex()

    suspend fun checkAll() = lock.withLock {
        val connections = container.connections.connections.value
        // The app shows changes itself while it is open.
        val notify = container.settings.notifications.value && !container.inForeground()
        var widgetChanged = false
        for (connection in connections) {
            try {
                val result = container.checker.check(connection.id, container.api(connection), warn = notify)
                if (notify) {
                    container.notifier.post(
                        connection,
                        result.alerts,
                        maxTtl = result.status.deployments.maxTtlSeconds.seconds,
                        canExtend = result.status.token.canWrite,
                        showServer = connections.size > 1,
                    )
                }
                if (connection.id == container.connections.activeId.value) {
                    widgetChanged = container.widget.save(connection.name, result.status, result.deployments)
                }
            } catch (_: ApiException) {
                // Unreachable for now; the next check tries again.
            }
        }
        if (widgetChanged) DeploymentsWidget().updateAll(context)
    }

    /** Whether an operation on a server has finished, or can no longer be followed. */
    suspend fun finished(serverId: String, operationId: String): Boolean {
        val connection = container.connections.connections.value.firstOrNull { it.id == serverId } ?: return true
        return try {
            val status = container.api(connection).operation(operationId).status
            status.done || status == OperationStatus.Unknown
        } catch (e: ApiException) {
            e.code == ApiException.NOT_FOUND
        }
    }
}
