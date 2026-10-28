package io.github.filstawski.deployer.model

/**
 * Whether the server runs a newer release than this app. The app and the
 * server are released together, so a newer server means a newer app exists.
 * Development builds, which aren't releases, never count.
 */
fun serverIsNewer(app: String, server: String): Boolean {
    val a = release(app) ?: return false
    val s = release(server) ?: return false
    return s.zip(a).firstOrNull { (x, y) -> x != y }?.let { (x, y) -> x > y } ?: false
}

private fun release(version: String): List<Int>? =
    Regex("""^v?(\d+)\.(\d+)\.(\d+)""").find(version)?.destructured?.toList()
        ?.map(String::toInt)
        ?.takeIf { parts -> parts.any { it > 0 } }
