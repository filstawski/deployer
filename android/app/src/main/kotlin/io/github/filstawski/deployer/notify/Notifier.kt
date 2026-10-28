package io.github.filstawski.deployer.notify

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.core.net.toUri
import io.github.filstawski.deployer.MainActivity
import io.github.filstawski.deployer.R
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.data.Connection
import io.github.filstawski.deployer.model.TimeFormat
import io.github.filstawski.deployer.model.durationLabel
import io.github.filstawski.deployer.model.extendOptions
import io.github.filstawski.deployer.model.formatDuration
import java.time.Instant
import kotlin.time.Duration
import kotlin.time.Duration.Companion.days
import kotlin.time.Duration.Companion.hours

/** Turns alerts into notifications, each opening its deployment in the app. */
class Notifier(private val context: Context, private val times: TimeFormat = TimeFormat()) {
    private val manager = NotificationManagerCompat.from(context)

    fun createChannels() {
        val system = context.getSystemService(NotificationManager::class.java)
        system.createNotificationChannel(
            NotificationChannel(CHANGES, "Deployment changes", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "A deployment is ready, failed, or was updated."
            },
        )
        system.createNotificationChannel(
            NotificationChannel(EXPIRY, "Expiry warnings", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "A running deployment has less than an hour left."
            },
        )
    }

    /** Whether Android lets the app post notifications at all. */
    fun allowed(): Boolean =
        manager.areNotificationsEnabled() &&
            (Build.VERSION.SDK_INT < 33 || ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED)

    /** Posts [alerts]; expiry warnings get Extend buttons when the token may extend. */
    fun post(connection: Connection, alerts: List<Alert>, maxTtl: Duration, canExtend: Boolean, showServer: Boolean) {
        if (!allowed()) return
        val now = Instant.now()
        for (alert in alerts) {
            val d = alert.deployment
            val builder = base(connection, d, if (alert is Alert.Expiring) EXPIRY else CHANGES, showServer)
            when (alert) {
                is Alert.Ready -> builder
                    .setContentTitle("${d.name} is live")
                    .setContentText("${d.project} · ${d.ref}")
                    .addAction(0, "Open preview", viewUrl(d))
                is Alert.Failed -> builder
                    .setContentTitle("${d.name} failed")
                    .setContentText(alert.error?.message ?: "It didn't start.")
                    .setStyle(NotificationCompat.BigTextStyle().bigText(listOfNotNull(alert.error?.message, alert.error?.hint).joinToString("\n")))
                is Alert.Updated -> builder
                    .setContentTitle("${d.name} was updated")
                    .setContentText("Now at ${alert.commit.take(7)} on ${d.ref}")
                    .addAction(0, "Open preview", viewUrl(d))
                is Alert.UpdateFailed -> {
                    // A running deployment keeps its previous version; one that wasn't running stays failed.
                    val kept = "The previous version is still running.".takeIf { d.state == State.Running }
                    builder
                        .setContentTitle("Updating ${d.name} failed")
                        .setContentText(kept ?: alert.error?.message ?: "")
                        .setStyle(NotificationCompat.BigTextStyle().bigText(listOfNotNull(alert.error?.message, kept).joinToString("\n")))
                }
                is Alert.Expiring -> {
                    builder
                        .setContentTitle("${d.name} expires in ${formatDuration(alert.left)}")
                        .setContentText(d.expiresAt?.let { "It stops ${times.at(it, now)} unless you extend it." } ?: "")
                    // Two of the usual extensions, as far as the server allows.
                    val options = if (canExtend) d.extendOptions(maxTtl, now) else emptyList()
                    val wanted = listOf(4.hours, 1.days)
                    (options.filter { it.by in wanted }.ifEmpty { options.takeLast(1) }).forEach { option ->
                        builder.addAction(0, "Extend " + durationLabel(option.by), ExtendReceiver.intent(context, connection, d, option.by, id(connection, d, EXPIRY)))
                    }
                }
            }
            post(id(connection, d, if (alert is Alert.Expiring) EXPIRY else CHANGES), builder)
        }
    }

    /** Replaces an expiry warning with the result of extending from it. */
    fun extended(connection: Connection, notificationId: Int, deployment: Deployment?, error: String?) {
        if (!allowed()) return
        val target = deployment
        val builder = if (target != null) {
            base(connection, target, EXPIRY, showServer = false)
                .setContentTitle("${target.name} was extended")
                .setContentText(target.expiresAt?.let { "It now expires ${times.at(it, Instant.now())}." } ?: "")
                .setOnlyAlertOnce(true)
        } else {
            NotificationCompat.Builder(context, EXPIRY)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle("Couldn't extend")
                .setContentText(error)
                .setAutoCancel(true)
        }
        post(notificationId, builder)
    }

    private fun base(connection: Connection, d: Deployment, channel: String, showServer: Boolean) =
        NotificationCompat.Builder(context, channel)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentIntent(open(connection, d))
            .setAutoCancel(true)
            .apply { if (showServer) setSubText(connection.name) }

    private fun post(id: Int, builder: NotificationCompat.Builder) {
        try {
            manager.notify(id, builder.build())
        } catch (_: SecurityException) {
            // The permission was withdrawn between the check and here.
        }
    }

    private fun open(connection: Connection, d: Deployment): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
            .putExtra(MainActivity.EXTRA_SERVER, connection.id)
            .putExtra(MainActivity.EXTRA_DEPLOYMENT, d.name)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        return PendingIntent.getActivity(context, "${connection.id}/${d.id}".hashCode(), intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    }

    private fun viewUrl(d: Deployment): PendingIntent =
        PendingIntent.getActivity(context, d.url.hashCode(), Intent(Intent.ACTION_VIEW, d.url.toUri()), PendingIntent.FLAG_IMMUTABLE)

    companion object {
        const val CHANGES = "changes"
        const val EXPIRY = "expiry"

        /** One notification per deployment and kind, so newer news replaces older. */
        fun id(connection: Connection, d: Deployment, channel: String) = "${connection.id}/${d.id}/$channel".hashCode()
    }
}
