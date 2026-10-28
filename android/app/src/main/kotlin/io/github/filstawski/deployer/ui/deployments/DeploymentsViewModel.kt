package io.github.filstawski.deployer.ui.deployments

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.Operation
import io.github.filstawski.deployer.api.OperationStatus
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.model.BulkAction
import io.github.filstawski.deployer.model.StateFilter
import io.github.filstawski.deployer.model.deployments
import io.github.filstawski.deployer.model.extensionFor
import io.github.filstawski.deployer.model.runEach
import java.time.Instant
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/** An action running on several deployments, and how far it got. */
data class BulkRun(
    val action: BulkAction,
    val total: Int,
    /** How many have finished, well or not. */
    val done: Int = 0,
    /** Deployment names with why the action failed for them. */
    val failures: List<Pair<String, ApiException>> = emptyList(),
) {
    val finished: Boolean get() = done == total
}

data class DeploymentsState(
    val status: ServerStatus? = null,
    val deployments: List<Deployment> = emptyList(),
    /** The first load finished, successfully or not. */
    val loaded: Boolean = false,
    val refreshing: Boolean = false,
    /** The last refresh failed; earlier data stays on screen. */
    val error: ApiException? = null,
    val filter: StateFilter = StateFilter.All,
    /** Deployments chosen for a bulk action, by ID; selecting while not empty. */
    val selected: Set<String> = emptySet(),
    val bulk: BulkRun? = null,
    /** A short confirmation to show once, such as "Stopped 3 deployments". */
    val message: String? = null,
) {
    val visible: List<Deployment> get() = deployments.filter { filter.matches(it.state) }

    val selecting: Boolean get() = selected.isNotEmpty()

    val selection: List<Deployment> get() = deployments.filter { it.id in selected }

    val canWrite: Boolean get() = status?.token?.canWrite == true

    fun count(filter: StateFilter): Int = deployments.count { filter.matches(it.state) }

    /** The selected deployments [action] applies to. */
    fun targets(action: BulkAction): List<Deployment> = selection.filter(action::appliesTo)
}

class DeploymentsViewModel(
    private val api: DeployerApi,
    /** Receives each successful refresh, for the home screen widget. */
    private val onLoaded: (ServerStatus, List<Deployment>) -> Unit = { _, _ -> },
    /** Called with the operations a bulk action started, to follow them after the app is left. */
    private val onSubmitted: (operationIds: List<String>) -> Unit = {},
    private val pollMillis: Long = 1_500,
    private val clock: () -> Instant = Instant::now,
) : ViewModel() {
    private val _state = MutableStateFlow(DeploymentsState())
    val state: StateFlow<DeploymentsState> = _state.asStateFlow()

    private val visible = MutableStateFlow(false)
    private val wake = Channel<Unit>(Channel.CONFLATED)
    private var bulkJob: Job? = null

    /** Refreshes while the screen is visible: often while something is building or a bulk action runs. */
    suspend fun poll() {
        visible.value = true
        try {
            while (true) {
                load()
                val s = _state.value
                val busy = s.deployments.any { it.state.inProgress } || s.bulk?.finished == false
                withTimeoutOrNull(if (busy) 3_000L else 10_000L) { wake.receive() }
            }
        } finally {
            visible.value = false
        }
    }

    fun refresh() {
        viewModelScope.launch {
            _state.update { it.copy(refreshing = true) }
            load()
            _state.update { it.copy(refreshing = false) }
        }
    }

    fun setFilter(filter: StateFilter) = _state.update { it.copy(filter = filter) }

    fun messageShown() = _state.update { it.copy(message = null) }

    suspend fun extend(deployment: Deployment, seconds: Long): Deployment {
        val updated = api.extend(deployment.id, seconds)
        replace(updated)
        return updated
    }

    /** Adds a deployment to the selection, or takes it out. */
    fun toggle(deployment: Deployment) = _state.update { s ->
        s.copy(selected = if (deployment.id in s.selected) s.selected - deployment.id else s.selected + deployment.id)
    }

    fun selectAll() = _state.update { s -> s.copy(selected = s.selected + s.visible.map { it.id }) }

    fun clearSelection() = _state.update { it.copy(selected = emptySet()) }

    /**
     * Hides the bulk banner. A run still in progress stops being followed
     * here; its operations continue on the server, and background checks
     * still notify how they end.
     */
    fun dismissBulk() {
        bulkJob?.cancel()
        _state.update { it.copy(bulk = null) }
    }

    /**
     * Runs [action] on the selected deployments it applies to. For Extend,
     * [extendBy] is the extension each gets as far as the server allows, or
     * null for each one's maximum.
     */
    fun runBulk(action: BulkAction, extendBy: Duration? = null) {
        val s = _state.value
        val targets = s.targets(action)
        if (targets.isEmpty() || s.bulk?.finished == false) return
        _state.update { it.copy(selected = emptySet(), bulk = BulkRun(action, targets.size)) }
        bulkJob = viewModelScope.launch {
            if (action == BulkAction.Extend) {
                extendAll(targets, extendBy, s.status?.deployments?.maxTtlSeconds?.seconds ?: Duration.ZERO)
            } else {
                val outcomes = runEach(targets) { d -> submit(action, d) }
                outcomes.forEach { o -> o.error?.let { fail(o.item.name, it) } }
                val started = outcomes.mapNotNull { o -> o.result?.let { o.item.name to it } }
                if (started.isNotEmpty()) onSubmitted(started.map { it.second.id })
                wake.trySend(Unit)
                follow(started)
            }
            finish()
        }
    }

    private suspend fun submit(action: BulkAction, d: Deployment): Operation = when (action) {
        BulkAction.Start -> api.start(d.id)
        BulkAction.Stop -> api.stop(d.id)
        BulkAction.Update -> api.update(d.id)
        BulkAction.Destroy -> api.destroy(d.id)
        BulkAction.Extend -> error("extending is not an operation")
    }

    private suspend fun extendAll(targets: List<Deployment>, choice: Duration?, maxTtl: Duration) {
        val now = clock()
        runEach(targets) { d ->
            val by = d.extensionFor(choice, maxTtl, now)
                ?: throw ApiException(ApiException.INVALID_INPUT, "It already has the longest lifetime the server allows.")
            api.extend(d.id, by.inWholeSeconds).also(::replace)
        }.forEach { o -> if (o.error != null) fail(o.item.name, o.error) else advance() }
    }

    /** An operation being followed, and how many times in a row looking it up failed. */
    private data class Followed(val name: String, val operation: Operation, val failures: Int = 0)

    /**
     * Polls the operations until each finishes, while the screen is visible.
     * One that can't be looked up five times in a row is given up on, like
     * the deployment screen does.
     */
    private suspend fun follow(started: List<Pair<String, Operation>>) {
        var pending = started.map { (name, op) -> Followed(name, op) }.filterNot { settle(it.name, it.operation) }
        while (pending.isNotEmpty()) {
            delay(pollMillis)
            visible.first { it }
            pending = coroutineScope {
                pending.map { followed ->
                    async {
                        try {
                            val current = api.operation(followed.operation.id)
                            if (settle(followed.name, current)) null else followed.copy(operation = current, failures = 0)
                        } catch (e: ApiException) {
                            when {
                                // Operations of a removed project go with it; nothing is left to follow.
                                e.code == ApiException.NOT_FOUND -> null.also { advance() }
                                followed.failures + 1 >= GIVE_UP -> null.also {
                                    fail(followed.name, ApiException(e.code, "Lost track of it: ${e.message} It continues on the server."))
                                }
                                else -> followed.copy(failures = followed.failures + 1)
                            }
                        }
                    }
                }.mapNotNull { it.await() }
            }
        }
    }

    /** Counts a finished operation, and its failure; returns whether it finished. */
    private fun settle(name: String, op: Operation): Boolean {
        val error = op.error
        when (op.status) {
            OperationStatus.Queued, OperationStatus.Running -> return false
            OperationStatus.Succeeded -> advance()
            OperationStatus.Failed, OperationStatus.Cancelled ->
                fail(name, ApiException(error?.code ?: "FAILED", error?.message ?: "The operation ${op.status.wire}.", error?.hint))
            // Not knowing how it ended, don't claim it worked.
            OperationStatus.Unknown ->
                fail(name, ApiException("UNKNOWN", "It ended in a way this app doesn't know. Check the deployment, or update the app."))
        }
        return true
    }

    private fun advance() = _state.update { s -> s.copy(bulk = s.bulk?.let { it.copy(done = it.done + 1) }) }

    private fun fail(name: String, error: ApiException) = _state.update { s ->
        s.copy(bulk = s.bulk?.let { it.copy(done = it.done + 1, failures = it.failures + (name to error)) })
    }

    /** Reports a clean run as a message, and keeps the banner when something failed. */
    private suspend fun finish() {
        _state.update { s ->
            val run = s.bulk ?: return@update s
            if (run.failures.isEmpty()) s.copy(bulk = null, message = "${run.action.done} ${deployments(run.total)}") else s
        }
        load()
    }

    private fun replace(deployment: Deployment) = _state.update { s ->
        s.copy(deployments = s.deployments.map { if (it.id == deployment.id) deployment else it })
    }

    private suspend fun load() {
        try {
            coroutineScope {
                val status = async { api.server() }
                val deployments = async { api.deployments() }
                val list = deployments.await()
                onLoaded(status.await(), list)
                _state.update { s ->
                    val filter = if (s.filter == StateFilter.All || list.any { s.filter.matches(it.state) }) s.filter else StateFilter.All
                    // Deployments that are gone can't stay selected.
                    val ids = list.map { it.id }.toSet()
                    s.copy(status = status.await(), deployments = list, loaded = true, error = null, filter = filter, selected = s.selected intersect ids)
                }
            }
        } catch (e: ApiException) {
            _state.update { it.copy(loaded = true, error = e) }
        }
    }

    private companion object {
        const val GIVE_UP = 5
    }
}
