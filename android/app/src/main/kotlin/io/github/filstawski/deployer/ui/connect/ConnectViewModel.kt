package io.github.filstawski.deployer.ui.connect

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.api.display
import io.github.filstawski.deployer.api.parseServerAddress
import io.github.filstawski.deployer.data.Connection
import io.github.filstawski.deployer.data.ConnectionStore
import io.github.filstawski.deployer.model.Pairing
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class ConnectState(
    val address: String = "",
    val token: String = "",
    val busy: Boolean = false,
    val error: ApiException? = null,
    /** Set once the server accepted the token and the connection is saved. */
    val connected: Connection? = null,
)

/**
 * Checks an address and token against the server, like `deployer login`, and
 * saves the connection only when the server accepts them.
 */
class ConnectViewModel(
    private val connections: ConnectionStore,
    private val api: (url: String, token: String) -> DeployerApi,
    private val allowPlainHttp: Boolean,
) : ViewModel() {
    private val _state = MutableStateFlow(ConnectState())
    val state: StateFlow<ConnectState> = _state.asStateFlow()

    fun setAddress(value: String) = _state.update { it.copy(address = value, error = null) }

    fun setToken(value: String) = _state.update { it.copy(token = value.trim(), error = null) }

    /** Fills in a server and token from `deployer pair`, and connects at once when [connect] is set. */
    fun usePairing(pairing: Pairing, connect: Boolean) {
        _state.update { it.copy(address = pairing.server, token = pairing.token, error = null) }
        if (connect) connect()
    }

    fun connect() {
        val current = _state.value
        if (current.busy) return
        _state.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            try {
                val url = parseServerAddress(current.address, allowPlainHttp)
                if (current.token.isEmpty()) {
                    throw ApiException(ApiException.INVALID_INPUT, "Enter the token deployer pair printed, or scan its QR code.")
                }
                val status = api(url.display(), current.token).server()
                val name = status.domain.ifEmpty { url.host }
                val connection = connections.add(name, url.display(), current.token)
                _state.update { it.copy(busy = false, connected = connection) }
            } catch (e: ApiException) {
                _state.update { it.copy(busy = false, error = explain(e)) }
            }
        }
    }

    /** Rewords the errors people hit while connecting. */
    private fun explain(e: ApiException): ApiException = when (e.code) {
        ApiException.UNAUTHENTICATED -> ApiException(
            e.code,
            "The server didn't accept this token. It may be mistyped, expired, or revoked. Pair again for a new one.",
            "deployer pair",
        )
        else -> e
    }
}
