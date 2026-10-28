package io.github.filstawski.deployer.ui.deployments

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.model.StateFilter
import io.github.filstawski.deployer.model.TimeFormat
import io.github.filstawski.deployer.model.Tone
import io.github.filstawski.deployer.model.expiresSoon
import io.github.filstawski.deployer.model.formatDuration
import io.github.filstawski.deployer.model.label
import io.github.filstawski.deployer.model.leaseRemaining
import io.github.filstawski.deployer.model.timeLeft
import io.github.filstawski.deployer.model.tone
import io.github.filstawski.deployer.ui.components.ErrorPanel
import io.github.filstawski.deployer.ui.components.LeaseBar
import io.github.filstawski.deployer.ui.components.Panel
import io.github.filstawski.deployer.ui.components.StateSymbol
import io.github.filstawski.deployer.ui.components.rememberNow
import io.github.filstawski.deployer.ui.extend.ExtendSheet
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType
import java.time.Instant
import kotlin.time.Duration.Companion.seconds
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DeploymentsScreen(
    viewModel: DeploymentsViewModel,
    serverName: String,
    times: TimeFormat,
    message: String?,
    onMessageShown: () -> Unit,
    onOpen: (Deployment) -> Unit,
    onDeploy: () -> Unit,
    askForNotifications: Boolean,
    onAllowNotifications: () -> Unit,
    onDeclineNotifications: () -> Unit,
    onServers: (ServerStatus?) -> Unit,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val now by rememberNow()
    val colors = DeployerTheme.colors
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    var extending by remember { mutableStateOf<Deployment?>(null) }

    val lifecycle = LocalLifecycleOwner.current
    LaunchedEffect(viewModel, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { viewModel.poll() }
    }
    LaunchedEffect(message) {
        if (message == null) return@LaunchedEffect
        // Clearing the message restarts this effect, so show it first.
        snackbar.showSnackbar(message)
        onMessageShown()
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        containerColor = colors.background,
        floatingActionButton = {
            if (state.status?.token?.canWrite == true) {
                ExtendedFloatingActionButton(
                    onClick = onDeploy,
                    icon = { Icon(DeployerIcons.Plus, contentDescription = null) },
                    text = { Text("Deploy", fontWeight = FontWeight.SemiBold) },
                    containerColor = colors.accent,
                    contentColor = colors.onAccent,
                )
            }
        },
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = state.refreshing,
            onRefresh = viewModel::refresh,
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            LazyColumn(
                Modifier.fillMaxSize(),
                // Room at the end for the Deploy button.
                contentPadding = androidx.compose.foundation.layout.PaddingValues(start = 20.dp, end = 20.dp, top = 12.dp, bottom = 96.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                item(key = "header") {
                    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        ServerChip(serverName, healthy = state.loaded && state.error == null, onClick = { onServers(state.status) })
                        Text("Deployments", style = DeployerType.title, color = colors.text)
                    }
                }
                state.error?.let { error ->
                    item(key = "error") { ErrorPanel(error, title = "Can't refresh", onAction = viewModel::refresh) }
                }
                if (askForNotifications) {
                    item(key = "notifications") { NotificationsOffer(onAllowNotifications, onDeclineNotifications) }
                }
                state.status?.let { status ->
                    item(key = "capacity") { Capacity(status, state.deployments) }
                }
                if (state.deployments.isNotEmpty()) {
                    item(key = "filters") { Filters(state, viewModel::setFilter) }
                }
                items(state.visible, key = { it.id }) { deployment ->
                    DeploymentCard(
                        deployment,
                        now,
                        canExtend = state.status?.token?.canWrite == true,
                        onClick = { onOpen(deployment) },
                        onExtend = { extending = deployment },
                    )
                }
                if (!state.loaded) {
                    item(key = "loading") {
                        Box(Modifier.fillMaxWidth().padding(top = 48.dp), contentAlignment = Alignment.Center) {
                            CircularProgressIndicator(color = colors.accent)
                        }
                    }
                } else if (state.deployments.isEmpty() && state.error == null) {
                    item(key = "empty") { Empty() }
                }
            }
        }
    }

    val status = state.status
    extending?.let { deployment ->
        if (status != null) {
            ExtendSheet(
                deployment = deployment,
                maxTtl = status.deployments.maxTtlSeconds.seconds,
                times = times,
                extend = { seconds -> viewModel.extend(deployment, seconds) },
                onExtended = { updated ->
                    extending = null
                    val until = updated.expiresAt?.let { "now expires " + times.at(it, Instant.now()) } ?: "was extended"
                    scope.launch { snackbar.showSnackbar("${updated.name} $until") }
                },
                onDismiss = { extending = null },
            )
        }
    }
}

@Composable
private fun NotificationsOffer(onAllow: () -> Unit, onDecline: () -> Unit) {
    val colors = DeployerTheme.colors
    Panel(border = colors.accent.copy(alpha = 0.4f)) {
        Text("Get notified", style = DeployerType.body, fontWeight = FontWeight.SemiBold, color = colors.text)
        Spacer(Modifier.height(4.dp))
        Text(
            "When a deployment is ready, fails, or has less than an hour left. The app checks about every 15 minutes, " +
                "and every few seconds after you start something from here.",
            style = DeployerType.small,
            color = colors.muted,
        )
        Spacer(Modifier.height(8.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            androidx.compose.material3.Button(onClick = onAllow) { Text("Turn on") }
            androidx.compose.material3.TextButton(onClick = onDecline) { Text("Not now") }
        }
    }
}

@Composable
private fun ServerChip(name: String, healthy: Boolean, onClick: () -> Unit) {
    val colors = DeployerTheme.colors
    Surface(
        onClick = onClick,
        shape = RoundedCornerShape(20.dp),
        color = colors.surface,
        border = BorderStroke(1.dp, colors.line),
        modifier = Modifier.semantics { contentDescription = "Server $name. Switch servers" },
    ) {
        Row(
            Modifier
                .height(40.dp)
                .padding(horizontal = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Box(
                Modifier
                    .size(8.dp)
                    .clip(CircleShape)
                    .background(if (healthy) colors.ok else colors.idle),
            )
            Text(name, style = DeployerType.small, fontWeight = FontWeight.Medium, color = colors.text)
            Icon(DeployerIcons.ChevronDown, contentDescription = null, tint = colors.muted, modifier = Modifier.size(16.dp))
        }
    }
}

/**
 * Live deployments against the server's limit, one segment per slot. The
 * count is the server's: a token limited to some projects doesn't see the
 * others' deployments, which still fill slots and show in gray.
 */
@Composable
private fun Capacity(status: ServerStatus, deployments: List<Deployment>) {
    val colors = DeployerTheme.colors
    val live = deployments.filter { it.state != State.Destroyed }
    val total = maxOf(status.deployments.live, live.size)
    val max = status.deployments.max
    Panel {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.Bottom) {
            Text("Live on this server", style = DeployerType.small, color = colors.muted, modifier = Modifier.weight(1f))
            Text(total.toString(), style = DeployerType.name, fontSize = 18.sp, color = colors.text)
            if (max > 0) Text(" / $max", style = DeployerType.mono, color = colors.muted)
        }
        if (max in 1..20) {
            Spacer(Modifier.height(10.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                repeat(max) { slot ->
                    val color = when {
                        slot < live.size -> colors.tone(live[slot].state.tone)
                        slot < total -> colors.idle.copy(alpha = 0.5f)
                        else -> colors.line
                    }
                    Box(
                        Modifier
                            .weight(1f)
                            .height(6.dp)
                            .clip(RoundedCornerShape(3.dp))
                            .background(color),
                    )
                }
            }
        }
    }
}

@Composable
private fun Filters(state: DeploymentsState, onSelect: (StateFilter) -> Unit) {
    val colors = DeployerTheme.colors
    Row(
        Modifier.horizontalScroll(rememberScrollState()),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        StateFilter.entries.forEach { filter ->
            val count = state.count(filter)
            if (filter != StateFilter.All && count == 0) return@forEach
            FilterChip(
                selected = state.filter == filter,
                onClick = { onSelect(filter) },
                label = { Text("${filter.label} $count") },
                shape = RoundedCornerShape(22.dp),
                colors = FilterChipDefaults.filterChipColors(
                    selectedContainerColor = colors.accentSoft,
                    selectedLabelColor = colors.accent,
                    labelColor = colors.text,
                ),
                border = FilterChipDefaults.filterChipBorder(
                    enabled = true,
                    selected = state.filter == filter,
                    borderColor = colors.line,
                    selectedBorderColor = colors.accent,
                ),
                modifier = Modifier.height(40.dp),
            )
        }
    }
}

@Composable
private fun DeploymentCard(
    deployment: Deployment,
    now: Instant,
    canExtend: Boolean,
    onClick: () -> Unit,
    onExtend: () -> Unit,
) {
    val colors = DeployerTheme.colors
    val soon = deployment.expiresSoon(now)
    Panel(onClick = onClick, border = if (soon) colors.busy.copy(alpha = 0.35f) else colors.line) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            StateSymbol(deployment.state, Modifier.width(16.dp))
            Text(
                deployment.name,
                style = DeployerType.name,
                color = colors.text,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Text(deployment.state.label, style = DeployerType.small, color = colors.tone(deployment.state.tone))
        }
        Spacer(Modifier.height(6.dp))
        Row(Modifier.padding(start = 26.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(
                "${deployment.project} · ${deployment.ref}",
                style = DeployerType.small,
                color = colors.muted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Text(
                summary(deployment, now),
                style = DeployerType.small,
                color = if (soon) colors.busy else colors.muted,
                fontWeight = if (soon) FontWeight.Medium else FontWeight.Normal,
            )
        }
        deployment.error?.takeIf { deployment.state == State.Failed }?.let { error ->
            Spacer(Modifier.height(6.dp))
            Text(
                error.message,
                style = DeployerType.small,
                color = colors.bad,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(start = 26.dp),
            )
        }
        if (deployment.state == State.Running) {
            deployment.leaseRemaining(now)?.let { remaining ->
                Spacer(Modifier.height(10.dp))
                LeaseBar(remaining, if (soon) Tone.Busy else Tone.Ok, Modifier.padding(start = 26.dp))
            }
        }
        if (soon && canExtend) {
            Spacer(Modifier.height(10.dp))
            Surface(
                onClick = onExtend,
                shape = RoundedCornerShape(22.dp),
                color = colors.accentSoft,
                modifier = Modifier.padding(start = 26.dp),
            ) {
                Row(
                    Modifier
                        .height(40.dp)
                        .padding(horizontal = 16.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    Icon(DeployerIcons.Clock, contentDescription = null, tint = colors.accent, modifier = Modifier.size(18.dp))
                    Text("Extend", color = colors.accent, style = DeployerType.small, fontWeight = FontWeight.Medium)
                }
            }
        }
    }
}

/** The right-hand detail of a card: time left, or how long it has been working. */
private fun summary(d: Deployment, now: Instant): String {
    val left = d.timeLeft(now)
    return when {
        d.state.inProgress -> formatDuration(java.time.Duration.between(d.updatedAt, now).toKotlinSeconds())
        left == null -> ""
        left.isNegative() -> "expired"
        d.state == State.Running || d.state == State.Stopped -> formatDuration(left) + " left"
        else -> ""
    }
}

private fun java.time.Duration.toKotlinSeconds() = seconds.seconds

@Composable
private fun Empty() {
    val colors = DeployerTheme.colors
    Column(
        Modifier
            .fillMaxWidth()
            .padding(top = 32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Icon(DeployerIcons.Layers, contentDescription = null, tint = colors.muted, modifier = Modifier.size(36.dp))
        Text("No deployments yet", style = DeployerType.body, fontWeight = FontWeight.Medium, color = colors.text)
        Text(
            "Deploy a branch with the button below, or from a computer with deployer deploy.",
            style = DeployerType.small,
            color = colors.muted,
        )
    }
}
