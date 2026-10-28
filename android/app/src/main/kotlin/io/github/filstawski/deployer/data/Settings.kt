package io.github.filstawski.deployer.data

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** The app's own preferences. */
class Settings(private val store: KeyValueStore) {
    private val _notifications = MutableStateFlow(store.get(NOTIFICATIONS) != "off")
    private val _askedForNotifications = MutableStateFlow(store.get(ASKED) == "yes")

    /** Notifications about deployments, on unless turned off. */
    val notifications: StateFlow<Boolean> = _notifications.asStateFlow()

    /** The list stops offering to turn notifications on once answered. */
    val askedForNotifications: StateFlow<Boolean> = _askedForNotifications.asStateFlow()

    fun setNotifications(on: Boolean) {
        _notifications.value = on
        store.put(NOTIFICATIONS, if (on) null else "off")
    }

    fun askedForNotifications() {
        _askedForNotifications.value = true
        store.put(ASKED, "yes")
    }

    private companion object {
        const val NOTIFICATIONS = "notifications"
        const val ASKED = "asked_for_notifications"
    }
}
