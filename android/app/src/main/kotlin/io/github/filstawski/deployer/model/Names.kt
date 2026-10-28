package io.github.filstawski.deployer.model

import io.github.filstawski.deployer.api.DeploymentsStatus
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds

// The CLI's rules for names and lifetimes, so a deployment started from the
// phone gets the same suggestions as `deployer deploy`.

private val label = Regex("^[a-z0-9]([a-z0-9-]*[a-z0-9])?$")

private val commit = Regex("^[0-9a-f]{40}([0-9a-f]{24})?$")

/** Names the installation uses for its own hostnames, such as api.<domain>. */
private val reserved = setOf("api", "www", "admin", "deployer", "status")

/** Lowercase letters and digits, with single hyphens between runs, at most [max] long: domain.Slug. */
fun slug(text: String, max: Int): String {
    val out = StringBuilder()
    var dash = false
    for (c in text.lowercase()) {
        if (c in 'a'..'z' || c in '0'..'9') {
            out.append(c)
            dash = false
        } else if (!dash && out.isNotEmpty()) {
            out.append('-')
            dash = true
        }
    }
    return out.toString().take(max).trim('-')
}

/** Why [name] can't be a deployment name, or null when it can: domain.ValidateDeploymentName. */
fun nameProblem(name: String): String? = when {
    name.length > 63 || !label.matches(name) ->
        "Use 1–63 lowercase letters, digits, or hyphens, starting and ending with a letter or digit."
    name in reserved -> "$name is reserved."
    else -> null
}

/**
 * A readable name no live deployment uses, such as "shop-feature-login",
 * like the CLI's suggestName.
 */
fun suggestName(project: String, branch: String, taken: Set<String>): String {
    val ref = if (commit.matches(branch)) branch.take(7) else branch
    val base = slug("$project-$ref", 63).takeIf { nameProblem(it) == null } ?: "preview"
    var name = base
    var n = 2
    while (name in taken) {
        val suffix = "-$n"
        name = base.take(63 - suffix.length).trimEnd('-') + suffix
        n++
    }
    return name
}

data class LifetimeOption(val duration: Duration, val label: String, val isDefault: Boolean, val isMax: Boolean)

/** The common lifetimes up to the server's maximum, plus its default and maximum: the CLI's lifetimeOptions. */
fun lifetimeOptions(status: DeploymentsStatus): List<LifetimeOption> {
    val default = status.defaultTtlSeconds.seconds
    val max = status.maxTtlSeconds.seconds
    val all = (commonDurations.filter { it <= max } + default + max).distinct().sorted()
    return all.map { LifetimeOption(it, durationLabel(it), it == default, it == max) }
}
