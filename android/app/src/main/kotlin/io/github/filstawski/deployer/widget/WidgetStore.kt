package io.github.filstawski.deployer.widget

import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.data.KeyValueStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/** What the home screen widget shows: the active server at its last refresh. */
@Serializable
data class WidgetData(
    val server: String,
    val live: Int,
    val max: Int,
    val rows: List<WidgetRow>,
)

@Serializable
data class WidgetRow(val name: String, val state: State, val expiresAt: String? = null)

/**
 * Keeps the widget's data, written by the app and by background checks. The
 * widget itself never goes to the network; it watches [data].
 */
class WidgetStore(private val store: KeyValueStore) {
    private val _data = MutableStateFlow(store.get(KEY)?.let(::decode))

    /** What the widget shows; a widget on screen redraws as this changes. */
    val data: StateFlow<WidgetData?> = _data.asStateFlow()

    fun load(): WidgetData? = _data.value

    /** Saves new data, and reports whether it differs from what the widget shows. */
    fun save(server: String, status: ServerStatus, deployments: List<Deployment>): Boolean {
        val data = WidgetData(server, maxOf(status.deployments.live, deployments.size), status.deployments.max, rows(deployments))
        val text = json.encodeToString(WidgetData.serializer(), data)
        if (text == store.get(KEY)) return false
        store.put(KEY, text)
        _data.value = data
        return true
    }

    fun clear() {
        store.put(KEY, null)
        _data.value = null
    }

    private fun decode(text: String) = runCatching { json.decodeFromString(WidgetData.serializer(), text) }.getOrNull()

    private companion object {
        const val KEY = "widget"
        val json = Json { ignoreUnknownKeys = true }
    }
}

/**
 * The deployments worth a glance, most urgent first: building, then failed,
 * then running by how soon they expire, then the rest.
 */
fun rows(deployments: List<Deployment>, limit: Int = 5): List<WidgetRow> {
    fun rank(d: Deployment) = when {
        d.state.inProgress -> 0
        d.state == State.Failed -> 1
        d.state == State.Running -> 2
        else -> 3
    }
    return deployments
        .filter { it.state != State.Destroyed }
        .sortedWith(compareBy<Deployment>({ rank(it) }, { it.expiresAt ?: java.time.Instant.MAX }))
        .take(limit)
        .map { WidgetRow(it.name, it.state, it.expiresAt?.toString()) }
}
