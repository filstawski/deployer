package io.github.filstawski.deployer.ui.deployment

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.github.filstawski.deployer.api.ApiError
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.Operation
import io.github.filstawski.deployer.api.OperationStatus
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.api.UpdateResult
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

data class DeploymentState(
    val deployment: Deployment? = null,
    val status: ServerStatus? = null,
    /** The last refresh failed; earlier data stays on screen. */
    val error: ApiException? = null,
    /** The action in progress, such as "Stopping", from the tap until its operation finishes. */
    val pending: String? = null,
    /** Why the last action failed. */
    val actionError: ApiException? = null,
    /** A short confirmation to show once, such as "Stopped feat-checkout". */
    val message: String? = null,
    /** An update is building a new version while the current one keeps serving. */
    val updating: Boolean = false,
    /** The end of the build log, while something is being built. */
    val buildLog: List<String> = emptyList(),
    /** The deployment was destroyed from this screen; leave it. */
    val destroyed: Boolean = false,
) {
    val canWrite: Boolean get() = status?.token?.canWrite == true

    /** A build is running: the deployment's first, or an update's. */
    val building: Boolean get() = deployment?.state?.inProgress == true || updating
}

/**
 * One deployment and its actions. Actions return an operation, which is
 * followed until it finishes, the way the CLI waits for it, but only while
 * the screen is visible; the operation itself continues on the server.
 */
class DeploymentViewModel(
    private val api: DeployerApi,
    private val ref: String,
    private val pollMillis: Long = 1_000,
    /** Called with the operation once the server accepted an action, to follow it after the app is left. */
    private val onSubmitted: (operationId: String) -> Unit = {},
) : ViewModel() {
    private val _state = MutableStateFlow(DeploymentState())
    val state: StateFlow<DeploymentState> = _state.asStateFlow()

    private val visible = MutableStateFlow(false)

    /** Cuts the wait between refreshes short, so an action's effect shows at once. */
    private val wake = Channel<Unit>(Channel.CONFLATED)

    // Loads overlap: polling, pull to refresh, and the reload after an action.
    // Only a response newer than the one on screen is shown.
    private val started = AtomicLong()
    private val shown = AtomicLong()

    /** The deployment's most recent operation, to tell when an update is building. */
    private var latest: Operation? = null

    /** Refreshes while the screen is visible: often while something changes. */
    suspend fun poll() {
        visible.value = true
        try {
            while (true) {
                if (_state.value.status == null) loadStatus()
                load()
                val busy = _state.value.pending != null || _state.value.building
                withTimeoutOrNull(if (busy) 2_000L else 10_000L) { wake.receive() }
            }
        } finally {
            visible.value = false
        }
    }

    fun refresh() {
        viewModelScope.launch { load() }
    }

    fun showMessage(text: String) = _state.update { it.copy(message = text) }

    fun messageShown() = _state.update { it.copy(message = null) }

    fun dismissActionError() = _state.update { it.copy(actionError = null) }

    fun stop() = run("Stopping") { api.stop(ref) }

    fun start() = run("Starting") { api.start(ref) }

    fun update() = run("Updating") { api.update(ref) }

    fun destroy() = run("Destroying") { api.destroy(ref) }

    suspend fun extend(seconds: Long): Deployment {
        val updated = api.extend(ref, seconds)
        _state.update { it.copy(deployment = updated) }
        return updated
    }

    private fun run(label: String, submit: suspend () -> Operation) {
        // Pending from the tap on, so a second tap doesn't submit twice.
        if (_state.value.pending != null) return
        _state.update { it.copy(pending = label, actionError = null) }
        viewModelScope.launch {
            val submitted = try {
                submit()
            } catch (e: ApiException) {
                _state.update { it.copy(pending = null, actionError = e) }
                return@launch
            }
            wake.trySend(Unit)
            onSubmitted(submitted.id)
            val finished = follow(submitted)
            val name = _state.value.deployment?.name ?: ref
            _state.update {
                when (finished?.status) {
                    // The list shows the confirmation for a destroy, since this screen closes.
                    OperationStatus.Succeeded if finished.kind == "deployment.destroy" -> it.copy(pending = null, destroyed = true)
                    OperationStatus.Succeeded -> it.copy(pending = null, message = done(finished, name))
                    OperationStatus.Failed, OperationStatus.Cancelled -> {
                        val error = finished.error ?: ApiError("FAILED", "The operation ${finished.status.wire}.")
                        it.copy(pending = null, actionError = ApiException(error, 0, null))
                    }
                    else -> it.copy(pending = null)
                }
            }
            load()
        }
    }

    /** Polls the operation until it finishes, or returns null if it can't be followed. */
    private suspend fun follow(operation: Operation): Operation? {
        var current = operation
        var failures = 0
        // A status this app doesn't know may never end; stop and show what the deployment says.
        while (!current.status.done && current.status != OperationStatus.Unknown) {
            delay(pollMillis)
            visible.first { it }
            current = try {
                api.operation(current.id).also { failures = 0 }
            } catch (e: ApiException) {
                if (++failures < 5) continue
                _state.update {
                    it.copy(actionError = ApiException(e.code, "Lost track of the operation: ${e.message} It continues on the server."))
                }
                return null
            }
        }
        return current
    }

    private fun done(operation: Operation, name: String): String = when (operation.kind) {
        "deployment.stop" -> "Stopped $name"
        "deployment.start" -> "$name is running"
        "deployment.update" -> {
            val result = operation.result?.let {
                runCatching { DeployerApi.json.decodeFromJsonElement(UpdateResult.serializer(), it) }.getOrNull()
            }
            when {
                result == null -> "Updated $name"
                result.changed -> "Updated $name to ${result.commit.take(7)}"
                else -> "$name is already at the latest commit"
            }
        }
        else -> "Done"
    }

    private suspend fun loadStatus() {
        try {
            val status = api.server()
            _state.update { it.copy(status = status) }
        } catch (_: ApiException) {
            // Retried on the next refresh; until then the deployment shows without actions.
        }
    }

    private suspend fun load() {
        val request = started.incrementAndGet()
        try {
            val deployment = api.deployment(ref)
            val updating = updateRunning(deployment)
            val log = if (deployment.state.inProgress || updating) buildLogTail() else emptyList()
            if (newest(request)) {
                _state.update { it.copy(deployment = deployment, updating = updating, error = null, buildLog = log) }
            }
        } catch (e: ApiException) {
            if (newest(request)) _state.update { it.copy(error = e) }
        }
    }

    /** Records [request] as shown, unless a request started after it already was. */
    private fun newest(request: Long): Boolean {
        while (true) {
            val current = shown.get()
            if (request < current) return false
            if (shown.compareAndSet(current, request)) return true
        }
    }

    /**
     * Whether an update is building. The deployment stays running meanwhile,
     * so this asks for its most recent operation, which may have been started
     * from another device.
     */
    private suspend fun updateRunning(deployment: Deployment): Boolean {
        val id = deployment.operationId ?: return false
        val known = latest
        val operation = if (known != null && known.id == id && known.status.done) {
            known
        } else {
            try {
                api.operation(id).also { latest = it }
            } catch (_: ApiException) {
                return _state.value.updating
            }
        }
        return operation.kind == "deployment.update" && !operation.status.done
    }

    private suspend fun buildLogTail(): List<String> = try {
        api.logs(ref, build = true, lines = 8).logs.trimEnd().lines()
    } catch (_: ApiException) {
        _state.value.buildLog
    }
}
