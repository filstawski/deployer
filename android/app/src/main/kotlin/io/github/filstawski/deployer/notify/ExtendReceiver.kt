package io.github.filstawski.deployer.notify

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import io.github.filstawski.deployer.DeployerApplication
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.data.Connection
import kotlin.time.Duration
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

/** Extends a deployment from the buttons on an expiry warning, without opening the app. */
class ExtendReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val container = (context.applicationContext as DeployerApplication).container
        val connection = container.connections.connections.value.firstOrNull { it.id == intent.getStringExtra(SERVER) } ?: return
        val deployment = intent.getStringExtra(DEPLOYMENT) ?: return
        val seconds = intent.getLongExtra(SECONDS, 0)
        val notification = intent.getIntExtra(NOTIFICATION, 0)
        val pending = goAsync()
        CoroutineScope(Dispatchers.IO).launch {
            try {
                val extended = container.api(connection).extend(deployment, seconds)
                container.notifier.extended(connection, notification, extended, null)
            } catch (e: ApiException) {
                container.notifier.extended(connection, notification, null, e.message)
            } finally {
                pending.finish()
            }
        }
    }

    companion object {
        private const val SERVER = "server"
        private const val DEPLOYMENT = "deployment"
        private const val SECONDS = "seconds"
        private const val NOTIFICATION = "notification"

        fun intent(context: Context, connection: Connection, d: Deployment, by: Duration, notification: Int): PendingIntent {
            val intent = Intent(context, ExtendReceiver::class.java)
                .putExtra(SERVER, connection.id)
                .putExtra(DEPLOYMENT, d.id)
                .putExtra(SECONDS, by.inWholeSeconds)
                .putExtra(NOTIFICATION, notification)
            val code = "${connection.id}/${d.id}/${by.inWholeSeconds}".hashCode()
            return PendingIntent.getBroadcast(context, code, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        }
    }
}
