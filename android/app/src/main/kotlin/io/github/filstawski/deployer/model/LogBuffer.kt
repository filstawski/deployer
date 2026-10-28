package io.github.filstawski.deployer.model

data class LogLine(val number: Long, val text: String, val tone: Tone?)

/**
 * Collects log output into lines, keeping the newest [limit]. Text can arrive
 * in pieces that end mid-line, as when following a build log by offset.
 */
class LogBuffer(private val limit: Int = 5_000) {
    private val lines = ArrayDeque<LogLine>()
    private var partial = ""
    private var next = 0L

    /** The complete lines, then the unfinished one. */
    fun snapshot(): List<LogLine> =
        if (partial.isEmpty()) lines.toList() else lines.toList() + LogLine(next, partial, classify(partial))

    fun append(text: String) {
        if (text.isEmpty()) return
        val pieces = (partial + text).split('\n')
        pieces.dropLast(1).forEach { add(it) }
        partial = pieces.last()
    }

    /** Replaces everything with [text], as when re-reading the app's output. */
    fun replace(text: String) {
        lines.clear()
        partial = ""
        append(text)
    }

    private fun add(text: String) {
        lines.addLast(LogLine(next++, text.trimEnd('\r'), classify(text)))
        while (lines.size > limit) lines.removeFirst()
    }
}

private val errorWords = Regex("""\b(error|err|fatal|panic|failed|exception)\b|✗""", RegexOption.IGNORE_CASE)
private val warnWords = Regex("""\b(warn|warning)\b""", RegexOption.IGNORE_CASE)

/** Colors lines that look like errors or warnings; most lines stay plain. */
fun classify(line: String): Tone? = when {
    errorWords.containsMatchIn(line) -> Tone.Bad
    warnWords.containsMatchIn(line) -> Tone.Busy
    else -> null
}
