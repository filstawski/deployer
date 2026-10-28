package io.github.filstawski.deployer.api

import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * Turns what someone typed into a server's API address, following the CLI's
 * rules: HTTPS only, no path, query, or credentials. A missing scheme means
 * HTTPS. Debug builds set [allowPlainHttp], which permits plain HTTP to a
 * server on the development machine, and nowhere else.
 */
fun parseServerAddress(input: String, allowPlainHttp: Boolean = false): HttpUrl {
    val text = input.trim().trimEnd('/')
    if (text.isEmpty()) invalid("Enter your server's API address, such as https://api.example.com.")
    if (text.startsWith("ssh://")) {
        invalid("SSH connections work only from the CLI. Enter the server's HTTPS address, such as https://api.example.com.")
    }
    val withScheme = if ("://" in text) text else "https://$text"
    val url = withScheme.toHttpUrlOrNull()
        ?: invalid("That isn't a web address. It should look like https://api.example.com.")
    if (url.username.isNotEmpty() || url.password.isNotEmpty() || url.query != null || url.fragment != null) {
        invalid("Enter only the address, such as https://api.example.com.")
    }
    if (url.encodedPath != "/") invalid("Enter the address without a path, such as https://${url.host}.")
    if (url.scheme == "http" && !(allowPlainHttp && url.host in developmentHosts)) {
        invalid("Deployer only sends tokens over HTTPS. Use https://${url.host}.")
    }
    return url
}

/**
 * The development machine as a debug build sees it: the phone itself, or the
 * computer running the emulator. Debug builds' network security config allows
 * plain HTTP to the same hosts.
 */
private val developmentHosts = setOf("localhost", "127.0.0.1", "10.0.2.2")

/** The address as people write it: https://api.example.com. */
fun HttpUrl.display(): String = toString().trimEnd('/')

private fun invalid(message: String): Nothing = throw ApiException(ApiException.INVALID_INPUT, message)
