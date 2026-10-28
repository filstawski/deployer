package io.github.filstawski.deployer.data

import java.util.UUID
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json

/** A server this phone can talk to: the counterpart of a CLI context. */
data class Connection(
    val id: String,
    /** Shown in the app: the server's domain, or the API host. */
    val name: String,
    /** The API address, such as https://api.example.com. */
    val url: String,
    val token: String,
)

/** Small persistent strings; SharedPreferences in the app. */
interface KeyValueStore {
    fun get(key: String): String?
    fun put(key: String, value: String?)
}

/** Encrypts secrets at rest; the Android Keystore in the app. */
interface SecretCipher {
    fun encrypt(plain: String): String
    fun decrypt(sealed: String): String
}

/**
 * The saved connections and which one is active. Tokens are stored encrypted.
 * A token that can no longer be decrypted, for example after the keystore was
 * reset, drops its connection, and the app asks to connect again.
 */
class ConnectionStore(private val store: KeyValueStore, private val cipher: SecretCipher) {
    private val _connections = MutableStateFlow(load())
    private val _activeId = MutableStateFlow(store.get(ACTIVE)?.takeIf { id -> _connections.value.any { it.id == id } }
        ?: _connections.value.firstOrNull()?.id)

    val connections: StateFlow<List<Connection>> = _connections.asStateFlow()
    val activeId: StateFlow<String?> = _activeId.asStateFlow()

    val active: Connection? get() = _connections.value.firstOrNull { it.id == _activeId.value }

    /**
     * Saves a connection and makes it active. A connection to the same address
     * is replaced, keeping its place, so reconnecting with a new token works.
     */
    fun add(name: String, url: String, token: String): Connection {
        val existing = _connections.value.firstOrNull { it.url == url }
        val connection = Connection(existing?.id ?: UUID.randomUUID().toString(), name, url, token)
        _connections.update { list ->
            if (existing != null) list.map { if (it.id == existing.id) connection else it } else list + connection
        }
        save()
        activate(connection.id)
        return connection
    }

    fun remove(id: String) {
        _connections.update { list -> list.filterNot { it.id == id } }
        save()
        if (_activeId.value == id) activate(_connections.value.firstOrNull()?.id)
    }

    fun activate(id: String?) {
        _activeId.value = id
        store.put(ACTIVE, id)
    }

    private fun load(): List<Connection> {
        val raw = store.get(CONNECTIONS) ?: return emptyList()
        val saved = runCatching { json.decodeFromString(ListSerializer(Saved.serializer()), raw) }.getOrDefault(emptyList())
        return saved.mapNotNull { s ->
            runCatching { Connection(s.id, s.name, s.url, cipher.decrypt(s.token)) }.getOrNull()
        }
    }

    private fun save() {
        val saved = _connections.value.map { Saved(it.id, it.name, it.url, cipher.encrypt(it.token)) }
        store.put(CONNECTIONS, json.encodeToString(ListSerializer(Saved.serializer()), saved))
    }

    @Serializable
    private data class Saved(val id: String, val name: String, val url: String, val token: String)

    private companion object {
        const val CONNECTIONS = "connections"
        const val ACTIVE = "active_connection"
        val json = Json { ignoreUnknownKeys = true }
    }
}
