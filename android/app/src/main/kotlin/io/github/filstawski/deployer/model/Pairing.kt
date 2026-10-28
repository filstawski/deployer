package io.github.filstawski.deployer.model

import io.github.filstawski.deployer.api.display
import io.github.filstawski.deployer.api.parseServerAddress
import java.net.URI
import java.net.URLDecoder

/** A server and token from `deployer pair`, which shows them as a QR code. */
data class Pairing(val server: String, val token: String)

/**
 * Reads a pairing link, deployer://pair?server=...&token=..., or returns null
 * for anything else, including a server address the app wouldn't connect to.
 */
fun parsePairingLink(text: String, allowPlainHttp: Boolean = false): Pairing? {
    val uri = runCatching { URI(text.trim()) }.getOrNull() ?: return null
    if (uri.scheme != "deployer" || uri.host != "pair") return null
    val params = runCatching {
        uri.rawQuery.orEmpty().split('&').associate { pair ->
            val (key, value) = pair.split('=', limit = 2).let { it[0] to it.getOrElse(1) { "" } }
            // The Charset overload needs Android 13; the charset name works everywhere.
            URLDecoder.decode(key, "UTF-8") to URLDecoder.decode(value, "UTF-8")
        }
    }.getOrNull() ?: return null
    val token = params["token"]?.takeIf { it.startsWith("dpl_") } ?: return null
    val server = params["server"] ?: return null
    val url = runCatching { parseServerAddress(server, allowPlainHttp) }.getOrNull() ?: return null
    return Pairing(url.display(), token)
}
