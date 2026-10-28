package io.github.filstawski.deployer.model

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.TextStyle
import java.time.temporal.ChronoUnit
import java.util.Locale
import kotlin.time.toKotlinDuration

/**
 * Formats wall-clock times relative to today: "today at 18:40", "tomorrow at
 * 18:40", "Saturday at 18:40", "Nov 12 at 18:40". [time] formats the clock
 * part, so the app can follow the phone's 12- or 24-hour setting.
 */
class TimeFormat(
    private val zone: ZoneId = ZoneId.systemDefault(),
    private val locale: Locale = Locale.getDefault(),
    private val time: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm", locale),
) {
    private val date = DateTimeFormatter.ofPattern("MMM d", locale)

    fun at(instant: Instant, now: Instant): String {
        val then = instant.atZone(zone)
        val days = ChronoUnit.DAYS.between(now.atZone(zone).toLocalDate(), then.toLocalDate())
        val clock = then.format(time)
        return when (days) {
            0L -> "today at $clock"
            1L -> "tomorrow at $clock"
            -1L -> "yesterday at $clock"
            in 2L..6L -> "${then.dayOfWeek.getDisplayName(TextStyle.FULL, locale)} at $clock"
            else -> "${then.format(date)} at $clock"
        }
    }

    /**
     * A compact time for small spaces: "18:40" today, then "tomorrow", a
     * weekday, or a date.
     */
    fun short(instant: Instant, now: Instant): String {
        val then = instant.atZone(zone)
        return when (ChronoUnit.DAYS.between(now.atZone(zone).toLocalDate(), then.toLocalDate())) {
            0L -> then.format(time)
            1L -> "tomorrow"
            in 2L..6L -> then.dayOfWeek.getDisplayName(TextStyle.SHORT, locale)
            else -> then.format(date)
        }
    }

    /** "3h ago", "2d ago", or "just now". */
    fun ago(instant: Instant, now: Instant): String {
        val elapsed = java.time.Duration.between(instant, now).toKotlinDuration()
        return if (elapsed.inWholeSeconds < 60) "just now" else formatDuration(elapsed) + " ago"
    }
}
