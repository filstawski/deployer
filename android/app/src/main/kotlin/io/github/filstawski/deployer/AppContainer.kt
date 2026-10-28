package io.github.filstawski.deployer

import android.content.Context
import androidx.glance.appwidget.updateAll
import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.api.deployerHttpClient
import io.github.filstawski.deployer.data.Connection
import io.github.filstawski.deployer.data.ConnectionStore
import io.github.filstawski.deployer.data.KeystoreCipher
import io.github.filstawski.deployer.data.PreferencesStore
import io.github.filstawski.deployer.data.Settings
import io.github.filstawski.deployer.model.Pairing
import io.github.filstawski.deployer.notify.Checker
import io.github.filstawski.deployer.notify.Checks
import io.github.filstawski.deployer.notify.Monitor
import io.github.filstawski.deployer.notify.Notifier
import io.github.filstawski.deployer.widget.DeploymentsWidget
import io.github.filstawski.deployer.widget.WidgetStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import okhttp3.HttpUrl.Companion.toHttpUrl

/**
 * A pairing to use. One scanned in the app connects at once; one from a link
 * opened elsewhere waits for a tap on Connect, so a link alone can't add a server.
 */
data class PairingRequest(val pairing: Pairing, val connect: Boolean)

/** A deployment to show, from a notification or the widget. */
data class OpenRequest(val serverId: String?, val deployment: String)

/** The app's long-lived objects, created once in [DeployerApplication]. */
class AppContainer(private val context: Context, val inForeground: () -> Boolean) {
    private val prefs = PreferencesStore(context.getSharedPreferences("deployer", Context.MODE_PRIVATE))

    val connections = ConnectionStore(prefs, KeystoreCipher())

    val settings = Settings(prefs)

    /** Debug builds may talk to a server on the development machine over HTTP. */
    val allowPlainHttp: Boolean = BuildConfig.DEBUG

    /**
     * A pairing waiting for the connect screen: scanned in the app, or opened
     * from a deployer://pair link. Kept in memory only, since it holds a token.
     */
    val pairings = MutableStateFlow<PairingRequest?>(null)

    val opens = MutableStateFlow<OpenRequest?>(null)

    val checker = Checker(prefs)
    val notifier = Notifier(context)
    val widget = WidgetStore(prefs)
    val monitor = Monitor(context, this)

    /** Work that outlives a screen, such as refreshing the widget. */
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    private val http = deployerHttpClient()

    private val userAgent = "deployer-android/${BuildConfig.VERSION_NAME}"

    fun api(url: String, token: String) = DeployerApi(url.toHttpUrl(), token, http, userAgent)

    fun api(connection: Connection) = api(connection.url, connection.token)

    /** After actions from the phone: check often until their operations are done, to notify how they ended. */
    fun followUp(serverId: String, operationIds: List<String>) = Checks.follow(context, serverId, operationIds)

    /** Starts background checks once a server is saved, and stops them when none is left. */
    fun serversChanged() {
        if (connections.connections.value.isEmpty()) {
            Checks.stop(context)
            widget.clear()
        } else {
            Checks.schedule(context)
        }
        scope.launch { DeploymentsWidget().updateAll(context) }
    }

    /** Shows what the app just loaded on the widget, too. */
    fun showOnWidget(server: String, status: ServerStatus, deployments: List<Deployment>) {
        if (widget.save(server, status, deployments)) scope.launch { DeploymentsWidget().updateAll(context) }
    }
}
