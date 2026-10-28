package io.github.filstawski.deployer.ui.logs

import androidx.lifecycle.ViewModel
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.model.LogBuffer
import io.github.filstawski.deployer.model.LogLine
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

data class LogsState(
    val build: Boolean,
    val lines: List<LogLine> = emptyList(),
    val loaded: Boolean = false,
    val paused: Boolean = false,
    val error: ApiException? = null,
)

/**
 * Follows a deployment's output. The app's output is re-read each time; the
 * build log is read once and then followed from its byte offset, like
 * `deployer logs --build --follow`, until another build replaces it.
 */
class LogsViewModel(
    private val api: DeployerApi,
    private val ref: String,
    build: Boolean,
    private val appPollMillis: Long = 3_000,
    private val buildPollMillis: Long = 2_000,
) : ViewModel() {
    private val _state = MutableStateFlow(LogsState(build = build))
    val state: StateFlow<LogsState> = _state.asStateFlow()

    private var buffer = LogBuffer()
    private var offset: Long? = null
    /** The deployment's latest operation when the build log was read. */
    private var operationId: String? = null
    /** Bumped on every source switch, so a fetch for the old source is dropped. */
    private var generation = 0

    suspend fun poll() {
        while (true) {
            if (!_state.value.paused) load()
            delay(if (_state.value.build) buildPollMillis else appPollMillis)
        }
    }

    fun setBuild(build: Boolean) {
        if (build == _state.value.build) return
        generation++
        buffer = LogBuffer()
        offset = null
        operationId = null
        _state.update { LogsState(build = build, paused = it.paused) }
    }

    fun setPaused(paused: Boolean) = _state.update { it.copy(paused = paused) }

    /**
     * The lines as text, for copying or sharing: the newest, up to a size the
     * system can hand to other apps.
     */
    fun text(): String {
        val all = _state.value.lines.joinToString("\n") { it.text }
        return if (all.length <= SHARE_LIMIT) all else all.takeLast(SHARE_LIMIT).substringAfter('\n')
    }

    private suspend fun load() {
        val started = generation
        val build = _state.value.build
        try {
            if (build) {
                // An update clears the build log when it is queued, and the new
                // build writes from the start. Following the old offset would
                // splice the two, so a new operation means reading from the end again.
                val latest = api.deployment(ref).operationId
                if (started != generation) return
                if (latest != operationId) {
                    operationId = latest
                    offset = null
                }
            }
            val from = offset
            val logs = when {
                !build -> api.logs(ref, lines = APP_LINES)
                from == null -> api.logs(ref, build = true, lines = BUILD_LINES)
                else -> api.logs(ref, build = true, offset = from)
            }
            if (started != generation) return
            if (from == null) buffer.replace(logs.logs) else buffer.append(logs.logs)
            if (build) offset = logs.offset
            _state.update { it.copy(lines = buffer.snapshot(), loaded = true, error = null) }
        } catch (e: ApiException) {
            if (started != generation) return
            if (build && e.code == ApiException.NOT_FOUND) {
                _state.update { it.copy(lines = emptyList(), loaded = true, error = null) }
            } else {
                _state.update { it.copy(loaded = true, error = e) }
            }
        }
    }

    private companion object {
        const val APP_LINES = 500
        const val BUILD_LINES = 2_000
        /** Characters; Android fails to pass much more than this between apps. */
        const val SHARE_LIMIT = 100_000
    }
}
