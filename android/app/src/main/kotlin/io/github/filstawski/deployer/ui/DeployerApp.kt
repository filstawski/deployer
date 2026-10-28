package io.github.filstawski.deployer.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.provider.Settings
import android.text.format.DateFormat
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.compose.dropUnlessResumed
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import io.github.filstawski.deployer.AppContainer
import io.github.filstawski.deployer.BuildConfig
import io.github.filstawski.deployer.PairingRequest
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.model.TimeFormat
import io.github.filstawski.deployer.ui.connect.ConnectScreen
import io.github.filstawski.deployer.ui.connect.ConnectViewModel
import io.github.filstawski.deployer.ui.deploy.NewDeploymentScreen
import io.github.filstawski.deployer.ui.deploy.NewDeploymentViewModel
import io.github.filstawski.deployer.ui.deployment.DeploymentScreen
import io.github.filstawski.deployer.ui.deployment.DeploymentViewModel
import io.github.filstawski.deployer.ui.deployments.DeploymentsScreen
import io.github.filstawski.deployer.ui.deployments.DeploymentsViewModel
import io.github.filstawski.deployer.ui.logs.LogsScreen
import io.github.filstawski.deployer.ui.logs.LogsViewModel
import io.github.filstawski.deployer.ui.scan.ScanScreen
import io.github.filstawski.deployer.ui.servers.ServersSheet
import java.time.format.DateTimeFormatter
import kotlinx.serialization.Serializable

@Serializable data object ConnectRoute

@Serializable data object ScanRoute

@Serializable data object DeploymentsRoute

@Serializable data object NewDeploymentRoute

@Serializable data class DeploymentRoute(val name: String)

@Serializable data class LogsRoute(val name: String, val build: Boolean)

@Composable
fun DeployerApp(container: AppContainer) {
    val store = container.connections
    val connections by store.connections.collectAsStateWithLifecycle()
    val activeId by store.activeId.collectAsStateWithLifecycle()
    val active = connections.firstOrNull { it.id == activeId }
    val nav = rememberNavController()
    val times = rememberTimeFormat()
    var servers by remember { mutableStateOf<ServerStatus?>(null) }
    var showServers by remember { mutableStateOf(false) }
    val notifications = rememberNotifications(container)
    val context = LocalContext.current
    val hasCamera = remember { context.packageManager.hasSystemFeature(PackageManager.FEATURE_CAMERA_ANY) }

    // A deployer://pair link opened from elsewhere goes to the connect screen.
    val pairing by container.pairings.collectAsStateWithLifecycle()
    LaunchedEffect(pairing) {
        val destination = nav.currentDestination
        if (pairing?.connect == false && destination != null && !destination.hasRoute(ConnectRoute::class)) nav.navigate(ConnectRoute)
    }

    // A deployment from a notification or the widget, on whichever server it is.
    val open by container.opens.collectAsStateWithLifecycle()
    LaunchedEffect(open, active) {
        val request = open ?: return@LaunchedEffect
        container.opens.value = null
        if (request.serverId != null) {
            // From a server since removed from the phone: there is nothing to open.
            if (connections.none { it.id == request.serverId }) return@LaunchedEffect
            store.activate(request.serverId)
        }
        if (active != null) nav.navigate(DeploymentRoute(request.deployment)) { popUpTo<DeploymentsRoute>() }
    }

    // Without a server there is nothing else to show.
    LaunchedEffect(active == null) {
        val destination = nav.currentDestination
        if (active == null && destination != null && !destination.hasRoute(ConnectRoute::class)) nav.restart(ConnectRoute)
    }

    NavHost(nav, startDestination = if (active != null) DeploymentsRoute else ConnectRoute) {
        composable<ConnectRoute> {
            val vm = viewModel { ConnectViewModel(store, container::api, container.allowPlainHttp) }
            val request by container.pairings.collectAsStateWithLifecycle()
            LaunchedEffect(request) {
                val r = request ?: return@LaunchedEffect
                container.pairings.value = null
                vm.usePairing(r.pairing, r.connect)
            }
            ConnectScreen(
                vm,
                onScan = if (hasCamera) dropUnlessResumed { nav.navigate(ScanRoute) } else null,
                onConnected = {
                    container.serversChanged()
                    nav.restart(DeploymentsRoute)
                },
                onClose = if (connections.isNotEmpty()) dropUnlessResumed { nav.popBackStack() } else null,
            )
        }
        composable<ScanRoute> {
            ScanScreen(
                allowPlainHttp = container.allowPlainHttp,
                onScanned = { scanned ->
                    container.pairings.value = PairingRequest(scanned, connect = true)
                    nav.popBackStack()
                },
                onBack = dropUnlessResumed { nav.popBackStack() },
            )
        }
        composable<DeploymentsRoute> { entry ->
            val connection = active ?: return@composable
            val vm = viewModel(key = "deployments:${connection.id}") {
                DeploymentsViewModel(container.api(connection)) { status, list -> container.showOnWidget(connection.name, status, list) }
            }
            val message by entry.savedStateHandle.getStateFlow<String?>(MESSAGE, null).collectAsStateWithLifecycle()
            DeploymentsScreen(
                vm,
                serverName = connection.name,
                times = times,
                message = message,
                onMessageShown = { entry.savedStateHandle[MESSAGE] = null },
                onOpen = whenResumed { nav.navigate(DeploymentRoute(it.name)) },
                onDeploy = dropUnlessResumed { nav.navigate(NewDeploymentRoute) },
                askForNotifications = notifications.ask,
                onAllowNotifications = notifications.enable,
                onDeclineNotifications = notifications.decline,
                onServers = { status ->
                    servers = status
                    showServers = true
                },
            )
        }
        composable<NewDeploymentRoute> {
            val connection = active ?: return@composable
            val vm = viewModel(key = "new-deployment:${connection.id}") {
                NewDeploymentViewModel(container.api(connection)) { operation -> container.followUp(connection.id, operation) }
            }
            NewDeploymentScreen(
                vm,
                onClose = dropUnlessResumed { nav.popBackStack() },
                onOpened = { name ->
                    nav.navigate(DeploymentRoute(name)) { popUpTo<NewDeploymentRoute> { inclusive = true } }
                },
            )
        }
        composable<DeploymentRoute> { entry ->
            val connection = active ?: return@composable
            val route = entry.toRoute<DeploymentRoute>()
            val vm = viewModel(key = "deployment:${connection.id}:${route.name}") {
                DeploymentViewModel(container.api(connection), route.name) { operation -> container.followUp(connection.id, operation) }
            }
            DeploymentScreen(
                vm,
                name = route.name,
                times = times,
                onBack = dropUnlessResumed { nav.popBackStack() },
                onLogs = whenResumed { build -> nav.navigate(LogsRoute(route.name, build)) },
                onDestroyed = { message ->
                    nav.previousBackStackEntry?.savedStateHandle?.set(MESSAGE, message)
                    nav.popBackStack()
                },
            )
        }
        composable<LogsRoute> { entry ->
            val connection = active ?: return@composable
            val route = entry.toRoute<LogsRoute>()
            val vm = viewModel(key = "logs:${connection.id}:${route.name}") {
                LogsViewModel(container.api(connection), route.name, route.build)
            }
            LogsScreen(vm, name = route.name, onBack = dropUnlessResumed { nav.popBackStack() })
        }
    }

    if (showServers) {
        ServersSheet(
            connections = connections,
            activeId = activeId,
            status = servers,
            appVersion = BuildConfig.VERSION_NAME,
            notifications = notifications.on,
            onNotifications = { on -> if (on) notifications.enable() else container.settings.setNotifications(false) },
            onSelect = {
                store.activate(it.id)
                showServers = false
            },
            onAdd = {
                showServers = false
                nav.navigate(ConnectRoute)
            },
            onRemove = {
                store.remove(it.id)
                container.checker.forget(it.id)
                container.serversChanged()
                showServers = false
            },
            onDismiss = { showServers = false },
        )
    }
}

/** Whether to notify, and asking Android for permission where it needs to be asked. */
private class Notifications(
    val on: Boolean,
    /** Offer to turn notifications on: Android 13 and later ask the person first. */
    val ask: Boolean,
    val enable: () -> Unit,
    val decline: () -> Unit,
)

@Composable
private fun rememberNotifications(container: AppContainer): Notifications {
    val context = LocalContext.current
    val enabled by container.settings.notifications.collectAsStateWithLifecycle()
    val asked by container.settings.askedForNotifications.collectAsStateWithLifecycle()
    var allowed by remember { mutableStateOf(container.notifier.allowed()) }
    // Permission can change in the system settings while the app is away.
    LifecycleResumeEffect(Unit) {
        allowed = container.notifier.allowed()
        onPauseOrDispose {}
    }
    val request = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        container.settings.askedForNotifications()
        container.settings.setNotifications(granted)
        allowed = container.notifier.allowed()
    }
    return Notifications(
        on = enabled && allowed,
        ask = Build.VERSION.SDK_INT >= 33 && !allowed && !asked,
        enable = {
            when {
                allowed -> container.settings.setNotifications(true)
                Build.VERSION.SDK_INT >= 33 && !asked -> request.launch(Manifest.permission.POST_NOTIFICATIONS)
                // Refused before, or blocked: only the system settings can allow them now.
                else -> context.startActivity(
                    Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName),
                )
            }
        },
        decline = { container.settings.askedForNotifications() },
    )
}

/** A confirmation for the deployment list to show, such as "Destroyed feat-checkout". */
private const val MESSAGE = "message"

/**
 * Runs [block] only while this screen is in front. A screen that is leaving
 * still takes taps during its exit animation; acting on them would navigate
 * twice, or pop the last screen and leave nothing.
 */
@Composable
private fun <T> whenResumed(block: (T) -> Unit): (T) -> Unit {
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    return { value -> if (lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)) block(value) }
}

/** Clears the back stack and shows [route]. */
private fun NavHostController.restart(route: Any) = navigate(route) {
    popUpTo(graph.id) { inclusive = true }
}

/** Times in the phone's 12- or 24-hour style. */
@Composable
private fun rememberTimeFormat(): TimeFormat {
    val context = LocalContext.current
    val is24 = DateFormat.is24HourFormat(context)
    return remember(is24) { TimeFormat(time = DateTimeFormatter.ofPattern(if (is24) "HH:mm" else "h:mm a")) }
}
