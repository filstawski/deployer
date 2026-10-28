package io.github.filstawski.deployer.ui.deployments

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.model.StateFilter
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class DeploymentsState(
    val status: ServerStatus? = null,
    val deployments: List<Deployment> = emptyList(),
    /** The first load finished, successfully or not. */
    val loaded: Boolean = false,
    val refreshing: Boolean = false,
    /** The last refresh failed; earlier data stays on screen. */
    val error: ApiException? = null,
    val filter: StateFilter = StateFilter.All,
) {
    val visible: List<Deployment> get() = deployments.filter { filter.matches(it.state) }

    fun count(filter: StateFilter): Int = deployments.count { filter.matches(it.state) }
}

class DeploymentsViewModel(
    private val api: DeployerApi,
    /** Receives each successful refresh, for the home screen widget. */
    private val onLoaded: (ServerStatus, List<Deployment>) -> Unit = { _, _ -> },
) : ViewModel() {
    private val _state = MutableStateFlow(DeploymentsState())
    val state: StateFlow<DeploymentsState> = _state.asStateFlow()

    /** Refreshes while the screen is visible: often while something is building. */
    suspend fun poll() {
        while (true) {
            load()
            delay(if (_state.value.deployments.any { it.state.inProgress }) 3_000 else 10_000)
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

    suspend fun extend(deployment: Deployment, seconds: Long): Deployment {
        val updated = api.extend(deployment.id, seconds)
        replace(updated)
        return updated
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
                    s.copy(status = status.await(), deployments = list, loaded = true, error = null, filter = filter)
                }
            }
        } catch (e: ApiException) {
            _state.update { it.copy(loaded = true, error = e) }
        }
    }
}
