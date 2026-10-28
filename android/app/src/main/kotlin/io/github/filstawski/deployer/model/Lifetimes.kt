package io.github.filstawski.deployer.model

import io.github.filstawski.deployer.api.Deployment
import java.time.Instant
import kotlin.time.Duration
import kotlin.time.Duration.Companion.days
import kotlin.time.Duration.Companion.hours
import kotlin.time.Duration.Companion.minutes
import kotlin.time.Duration.Companion.seconds
import kotlin.time.toKotlinDuration

/**
 * Renders a duration compactly, like the CLI's domain.FormatDuration: "2d",
 * "1d 4h", "2h 14m", "45m", "30s". Only the two most significant units show.
 */
fun formatDuration(d: Duration): String {
    val abs = d.absoluteValue
    if (abs < 1.minutes) return "${abs.inWholeSeconds}s"
    val days = abs.inWholeDays
    val hours = abs.inWholeHours % 24
    val minutes = abs.inWholeMinutes % 60
    return when {
        days > 0 && hours > 0 -> "${days}d ${hours}h"
        days > 0 -> "${days}d"
        hours > 0 && minutes > 0 -> "${hours}h ${minutes}m"
        hours > 0 -> "${hours}h"
        else -> "${minutes}m"
    }
}

/** Names a duration for people: "4 hours", "3 days", "2 weeks". */
fun durationLabel(d: Duration): String {
    fun unit(n: Long, name: String) = if (n == 1L) "1 $name" else "$n ${name}s"
    val week = 7.days
    return when {
        d.inWholeSeconds % week.inWholeSeconds == 0L && d / week <= 4.0 -> unit(d.inWholeDays / 7, "week")
        d.inWholeSeconds % 1.days.inWholeSeconds == 0L -> unit(d.inWholeDays, "day")
        d.inWholeSeconds % 1.hours.inWholeSeconds == 0L -> unit(d.inWholeHours, "hour")
        else -> formatDuration(d)
    }
}

/** The lifetimes the CLI offers in prompts, as far as the server allows. */
val commonDurations = listOf(1.hours, 4.hours, 1.days, 3.days, 7.days, 14.days, 30.days)

fun Deployment.ttl(): Duration = ttlSeconds.seconds

/** Time until the deployment expires, or null before it is first ready. */
fun Deployment.timeLeft(now: Instant): Duration? =
    expiresAt?.let { java.time.Duration.between(now, it).toKotlinDuration() }

/** The lease's full length: the lifetime, plus any extensions since it became ready. */
fun Deployment.leaseLength(): Duration {
    val ready = readyAt
    val expires = expiresAt
    if (ready == null || expires == null) return ttl()
    return maxOf(ttl(), java.time.Duration.between(ready, expires).toKotlinDuration())
}

/** The lease was extended beyond the lifetime it started with. */
val Deployment.extended: Boolean get() = leaseLength() - ttl() >= 1.minutes

/** The share of the lease still left, from 1 down to 0. */
fun Deployment.leaseRemaining(now: Instant): Float? {
    val left = timeLeft(now) ?: return null
    val length = leaseLength()
    if (length <= Duration.ZERO) return null
    return (left / length).toFloat().coerceIn(0f, 1f)
}

/**
 * How much longer the server lets the deployment run, mirroring the
 * controller's Extend: before readiness the lifetime may grow to [maxTtl];
 * afterwards the lease may end at most [maxTtl] from now, and an expired
 * deployment gets a new lease starting now.
 */
fun Deployment.extensionLimit(maxTtl: Duration, now: Instant): Duration {
    val left = timeLeft(now)
    val limit = when {
        left == null -> maxTtl - ttl()
        left <= Duration.ZERO -> maxTtl
        else -> maxTtl - left
    }
    return limit.coerceAtLeast(Duration.ZERO)
}

data class ExtendOption(val by: Duration, val label: String, val isMax: Boolean)

/**
 * The common extensions that fit, plus the most the server still allows. The
 * maximum is rounded down and keeps a minute spare, so a phone clock that runs
 * a little behind the server's doesn't push it over the limit.
 */
fun Deployment.extendOptions(maxTtl: Duration, now: Instant): List<ExtendOption> {
    val limit = extensionLimit(maxTtl, now)
    val max = (limit - 1.minutes).let { spare ->
        if (spare >= 1.days) spare.inWholeHours.hours else spare.inWholeMinutes.minutes
    }
    if (max < 1.minutes) return emptyList()
    val common = commonDurations.filter { it < max }.map { ExtendOption(it, "+" + durationLabel(it), false) }
    return common + ExtendOption(max, "Max", true)
}

/** When the deployment would expire after extending it by [by]. */
fun Deployment.expiryAfter(by: Duration, now: Instant): Instant? {
    val expires = expiresAt ?: return null
    val base = if (expires.isAfter(now)) expires else now
    return base.plusSeconds(by.inWholeSeconds)
}
