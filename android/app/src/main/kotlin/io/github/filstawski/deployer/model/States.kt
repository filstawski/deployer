package io.github.filstawski.deployer.model

import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.State
import kotlin.time.Duration.Companion.hours

/** How a state reads at a glance; the CLI colors states the same way. */
enum class Tone { Ok, Busy, Bad, Muted }

val State.tone: Tone
    get() = when (this) {
        State.Running -> Tone.Ok
        State.Queued, State.Building, State.Starting, State.Expired -> Tone.Busy
        State.Failed -> Tone.Bad
        else -> Tone.Muted
    }

/** The CLI's state symbols: "● running", "◐ building", "✗ failed". */
val State.symbol: String
    get() = when (this) {
        State.Running -> "●"
        State.Queued, State.Building, State.Starting -> "◐"
        State.Stopped -> "○"
        State.Expired -> "◌"
        State.Failed -> "✗"
        else -> "–"
    }

val State.label: String get() = wire

/** Groups for the list's filter chips. */
enum class StateFilter(val label: String) {
    All("All"),
    Running("Running"),
    InProgress("In progress"),
    Failed("Failed"),
    Stopped("Stopped");

    fun matches(state: State): Boolean = when (this) {
        All -> true
        Running -> state == State.Running
        InProgress -> state.inProgress
        Failed -> state == State.Failed
        Stopped -> state == State.Stopped || state == State.Expired
    }
}

/** A running deployment with less than an hour left, worth a prompt to extend. */
fun Deployment.expiresSoon(now: java.time.Instant): Boolean {
    val left = timeLeft(now) ?: return false
    return state == State.Running && left < 1.hours
}

// What a deployment can do next, following the controller's checks. The server
// has the final word; these only decide which buttons are offered.
val Deployment.canStop: Boolean get() = state == State.Running
val Deployment.canStart: Boolean get() = (state == State.Stopped || state == State.Failed) && image.isNotEmpty()
val Deployment.canUpdate: Boolean get() = state != State.Expired && state != State.Destroyed && !state.inProgress
