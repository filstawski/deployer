package io.github.filstawski.deployer.ui.deployment

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.withResumed
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.model.TimeFormat
import io.github.filstawski.deployer.model.Tone
import io.github.filstawski.deployer.model.canStart
import io.github.filstawski.deployer.model.canStop
import io.github.filstawski.deployer.model.canUpdate
import io.github.filstawski.deployer.model.durationLabel
import io.github.filstawski.deployer.model.expiresSoon
import io.github.filstawski.deployer.model.extended
import io.github.filstawski.deployer.model.formatDuration
import io.github.filstawski.deployer.model.leaseRemaining
import io.github.filstawski.deployer.model.timeLeft
import io.github.filstawski.deployer.model.ttl
import io.github.filstawski.deployer.ui.components.DetailRow
import io.github.filstawski.deployer.ui.components.ErrorPanel
import io.github.filstawski.deployer.ui.components.LeaseBar
import io.github.filstawski.deployer.ui.components.Panel
import io.github.filstawski.deployer.ui.components.StateBadge
import io.github.filstawski.deployer.ui.components.rememberCopy
import io.github.filstawski.deployer.ui.components.rememberNow
import io.github.filstawski.deployer.ui.components.shareText
import io.github.filstawski.deployer.ui.extend.ExtendSheet
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType
import java.time.Instant
import kotlin.time.Duration.Companion.seconds

private enum class Confirm { Update, Stop, Destroy }

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DeploymentScreen(
    viewModel: DeploymentViewModel,
    name: String,
    times: TimeFormat,
    onBack: () -> Unit,
    onLogs: (build: Boolean) -> Unit,
    onDestroyed: (message: String) -> Unit,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val colors = DeployerTheme.colors
    val snackbar = remember { SnackbarHostState() }
    var confirm by remember { mutableStateOf<Confirm?>(null) }
    var extending by remember { mutableStateOf(false) }

    val lifecycle = LocalLifecycleOwner.current
    LaunchedEffect(viewModel, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { viewModel.poll() }
    }
    LaunchedEffect(state.message) {
        val message = state.message ?: return@LaunchedEffect
        // Clearing the message restarts this effect, so show it first.
        snackbar.showSnackbar(message)
        viewModel.messageShown()
    }
    LaunchedEffect(state.destroyed) {
        // Leave once the screen is in front again, if it was put away meanwhile.
        if (state.destroyed) lifecycle.lifecycle.withResumed { onDestroyed("Destroyed $name") }
    }

    Scaffold(
        containerColor = colors.background,
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(DeployerIcons.Back, contentDescription = "Back") }
                },
                title = {
                    Column {
                        Text(name, style = DeployerType.name, fontSize = 17.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        state.deployment?.let { Text(it.project, style = DeployerType.small, color = colors.muted) }
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = colors.background),
            )
        },
    ) { padding ->
        val deployment = state.deployment
        if (deployment == null) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                val error = state.error
                if (error != null) {
                    ErrorPanel(error, Modifier.padding(20.dp), onAction = viewModel::refresh)
                } else {
                    CircularProgressIndicator(color = colors.accent)
                }
            }
            return@Scaffold
        }
        Column(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(start = 20.dp, end = 20.dp, top = 4.dp, bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            StateLine(deployment)
            state.error?.let { ErrorPanel(it, title = "Can't refresh", onAction = viewModel::refresh) }
            state.pending?.let { Working(it) }
            state.actionError?.let { ErrorPanel(it, title = "That didn't work", actionLabel = "Dismiss", onAction = viewModel::dismissActionError) }
            if (deployment.state == State.Failed) deployment.error?.let { error ->
                ErrorPanel(ApiException(error, 0, null), title = "Why it failed")
            }
            if (deployment.state.inProgress) Steps(deployment)
            if (state.building) BuildLog(state.buildLog, updating = state.updating, onLogs = { onLogs(true) })
            Preview(deployment)
            if (deployment.state != State.Destroyed) {
                Lease(deployment, times, canExtend = state.canWrite && state.status != null, onExtend = { extending = true })
            }
            Actions(
                deployment,
                canWrite = state.canWrite,
                busy = state.pending != null,
                onUpdate = { confirm = Confirm.Update },
                onLogs = { onLogs(state.building || deployment.state == State.Failed && deployment.image.isEmpty()) },
                onStop = { confirm = Confirm.Stop },
                onStart = viewModel::start,
                onDestroy = { confirm = Confirm.Destroy },
            )
            Details(deployment, times, mine = deployment.createdBy == state.status?.token?.id)
        }

        val status = state.status
        if (extending && status != null) {
            ExtendSheet(
                deployment = deployment,
                maxTtl = status.deployments.maxTtlSeconds.seconds,
                times = times,
                extend = viewModel::extend,
                onExtended = { updated ->
                    extending = false
                    val until = updated.expiresAt?.let { "now expires " + times.at(it, Instant.now()) }
                        ?: "will run for ${durationLabel(updated.ttl())}"
                    viewModel.showMessage("${updated.name} $until")
                },
                onDismiss = { extending = false },
            )
        }
        confirm?.let { action ->
            ConfirmDialog(action, deployment, onDismiss = { confirm = null }) {
                confirm = null
                when (action) {
                    Confirm.Update -> viewModel.update()
                    Confirm.Stop -> viewModel.stop()
                    Confirm.Destroy -> viewModel.destroy()
                }
            }
        }
    }
}

@Composable
private fun StateLine(d: Deployment) {
    val colors = DeployerTheme.colors
    val text = when (d.state) {
        State.Queued -> "Waiting for a build slot"
        State.Building -> "Fetching the source and building the image"
        State.Starting -> "Started; waiting for the health check"
        State.Running -> "Healthy and serving visitors"
        State.Stopped -> "Stopped; visitors see an offline page"
        State.Expired -> "Its lifetime ended. Extend it, then start it again."
        State.Failed -> "It didn't start"
        State.Destroyed -> "Removed; kept as history"
        State.Unknown -> ""
    }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        StateBadge(d.state)
        Text(text, style = DeployerType.small, color = colors.muted)
    }
}

@Composable
private fun Working(label: String) {
    val colors = DeployerTheme.colors
    Surface(shape = RoundedCornerShape(16.dp), color = colors.accentSoft) {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = colors.accent)
            Text("$label…", style = DeployerType.body, color = colors.text)
        }
    }
}

/** Queued → building → starting → running. */
@Composable
private fun Steps(d: Deployment) {
    val colors = DeployerTheme.colors
    val steps = listOf(State.Queued to "Queued", State.Building to "Building", State.Starting to "Starting", State.Running to "Running")
    val current = steps.indexOfFirst { it.first == d.state }
    Panel(Modifier.fillMaxWidth(), radius = 20.dp) {
        steps.forEachIndexed { index, (_, label) ->
            Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(vertical = 6.dp)) {
                val color = when {
                    index < current -> colors.ok
                    index == current -> colors.busy
                    else -> colors.line
                }
                Box(
                    Modifier
                        .size(22.dp)
                        .clip(CircleShape)
                        .then(if (index < current) Modifier.background(color) else Modifier.border(2.dp, color, CircleShape)),
                    contentAlignment = Alignment.Center,
                ) {
                    if (index < current) Icon(DeployerIcons.Check, null, tint = colors.background, modifier = Modifier.size(14.dp))
                    if (index == current) CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp, color = colors.busy)
                }
                Spacer(Modifier.width(14.dp))
                Text(
                    label,
                    style = DeployerType.body,
                    fontWeight = if (index == current) FontWeight.SemiBold else FontWeight.Normal,
                    color = when {
                        index == current -> colors.busy
                        index < current -> colors.text
                        else -> colors.muted
                    },
                )
            }
        }
    }
}

/** The end of the build log, for a first build or an update's new version. */
@Composable
private fun BuildLog(log: List<String>, updating: Boolean, onLogs: () -> Unit) {
    val colors = DeployerTheme.colors
    Surface(shape = RoundedCornerShape(20.dp), color = colors.code, border = BorderStroke(1.dp, colors.line)) {
        Column {
            Row(Modifier.padding(start = 16.dp, end = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(
                    if (updating) "Building the new version; the current one keeps serving" else "Build log",
                    style = DeployerType.small,
                    color = Color(0xFFA0A6B0),
                    modifier = Modifier.weight(1f),
                )
                TextButton(onClick = onLogs) { Text("Full log") }
            }
            Column(Modifier.padding(start = 16.dp, end = 16.dp, bottom = 14.dp)) {
                if (log.isEmpty()) Text("Waiting for output…", style = DeployerType.log, color = Color(0xFFA0A6B0))
                log.forEach { Text(it, style = DeployerType.log, color = Color(0xFFD5D9DF), maxLines = 2, overflow = TextOverflow.Ellipsis) }
            }
        }
    }
}

@Composable
private fun Preview(d: Deployment) {
    val colors = DeployerTheme.colors
    val copy = rememberCopy()
    val uri = LocalUriHandler.current
    val context = LocalContext.current
    Panel(radius = 20.dp) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("Preview", style = DeployerType.small, color = colors.muted)
                Text(d.hostname, style = DeployerType.mono, fontSize = 14.sp, color = colors.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            IconButton(onClick = { copy("Preview address", d.url) }) {
                Icon(DeployerIcons.Copy, contentDescription = "Copy preview address", tint = colors.text, modifier = Modifier.size(18.dp))
            }
        }
        Spacer(Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = { uri.openUri(d.url) }, modifier = Modifier.weight(1f).height(48.dp)) {
                Icon(DeployerIcons.OpenExternal, contentDescription = null, modifier = Modifier.size(18.dp))
                Text("Open preview", fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(start = 8.dp))
            }
            OutlinedButton(onClick = { context.shareText(d.url, d.name) }, modifier = Modifier.height(48.dp)) {
                Text("Share", color = colors.text)
            }
        }
    }
}

@Composable
private fun Lease(d: Deployment, times: TimeFormat, canExtend: Boolean, onExtend: () -> Unit) {
    val colors = DeployerTheme.colors
    val now by rememberNow()
    val left = d.timeLeft(now)
    Panel(radius = 20.dp) {
        Row(verticalAlignment = Alignment.Top) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                when {
                    left == null -> {
                        Text("Lifetime", style = DeployerType.small, color = colors.muted)
                        Text(durationLabel(d.ttl()), fontSize = 26.sp, fontWeight = FontWeight.Medium, color = colors.text)
                    }
                    left.isNegative() -> {
                        Text("Expired", style = DeployerType.small, color = colors.muted)
                        Text(formatDuration(left) + " ago", style = DeployerType.name, fontSize = 26.sp, color = colors.busy)
                    }
                    else -> {
                        Text("Expires in", style = DeployerType.small, color = colors.muted)
                        Text(
                            formatDuration(left),
                            style = DeployerType.name,
                            fontSize = 28.sp,
                            color = if (d.expiresSoon(now)) colors.busy else colors.text,
                        )
                    }
                }
            }
            if (canExtend) {
                Surface(onClick = onExtend, shape = RoundedCornerShape(22.dp), color = colors.accentSoft) {
                    Row(
                        Modifier
                            .height(44.dp)
                            .padding(horizontal = 16.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(6.dp),
                    ) {
                        Icon(DeployerIcons.Clock, contentDescription = null, tint = colors.accent, modifier = Modifier.size(18.dp))
                        Text("Extend", color = colors.accent, fontWeight = FontWeight.Medium)
                    }
                }
            }
        }
        d.leaseRemaining(now)?.takeIf { left?.isPositive() == true }?.let { remaining ->
            Spacer(Modifier.height(10.dp))
            LeaseBar(remaining, if (d.expiresSoon(now)) Tone.Busy else Tone.Ok, height = 6.dp)
        }
        Spacer(Modifier.height(10.dp))
        val expires = d.expiresAt
        val detail = when {
            expires == null -> "Starts counting once it's ready"
            expires.isAfter(now) -> times.at(expires, now).replaceFirstChar(Char::uppercase) +
                if (d.extended) " · extended" else " · lifetime " + durationLabel(d.ttl())
            else -> d.purgeAt?.let { "Removed for good " + times.at(it, now) } ?: ""
        }
        Text(detail, style = DeployerType.small, color = colors.muted)
    }
}

@Composable
private fun Actions(
    d: Deployment,
    canWrite: Boolean,
    busy: Boolean,
    onUpdate: () -> Unit,
    onLogs: () -> Unit,
    onStop: () -> Unit,
    onStart: () -> Unit,
    onDestroy: () -> Unit,
) {
    val colors = DeployerTheme.colors
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        if (canWrite) {
            ActionTile(DeployerIcons.Update, "Update", enabled = !busy && d.canUpdate, onClick = onUpdate)
        }
        ActionTile(DeployerIcons.Terminal, "Logs", enabled = true, onClick = onLogs)
        if (canWrite) {
            if (d.canStart) {
                ActionTile(DeployerIcons.Play, "Start", enabled = !busy, onClick = onStart)
            } else {
                ActionTile(DeployerIcons.Stop, "Stop", enabled = !busy && d.canStop, onClick = onStop)
            }
            ActionTile(DeployerIcons.Trash, "Destroy", enabled = !busy && d.state != State.Destroyed, tint = colors.bad, onClick = onDestroy)
        }
    }
}

@Composable
private fun RowScope.ActionTile(icon: ImageVector, label: String, enabled: Boolean, tint: Color? = null, onClick: () -> Unit) {
    val colors = DeployerTheme.colors
    val color = (tint ?: colors.text).copy(alpha = if (enabled) 1f else 0.35f)
    Surface(
        onClick = onClick,
        enabled = enabled,
        shape = RoundedCornerShape(16.dp),
        color = colors.surface,
        modifier = Modifier
            .weight(1f)
            .height(76.dp),
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
            Icon(icon, contentDescription = null, tint = color, modifier = Modifier.size(22.dp))
            Spacer(Modifier.height(6.dp))
            Text(label, style = DeployerType.small, fontWeight = FontWeight.Medium, color = color)
        }
    }
}

@Composable
private fun Details(d: Deployment, times: TimeFormat, mine: Boolean) {
    val now = Instant.now()
    Panel(radius = 20.dp) {
        DetailRow("Branch", d.ref, mono = true)
        DetailRow("Commit", d.commit.take(7).ifEmpty { "not built yet" }, mono = d.commit.isNotEmpty())
        if (d.port > 0) DetailRow("Health check", ":${d.port}${d.healthPath}", mono = true)
        DetailRow("Created", times.ago(d.createdAt, now) + if (mine) " by this phone" else "", showDivider = false)
    }
}

@Composable
private fun ConfirmDialog(action: Confirm, d: Deployment, onDismiss: () -> Unit, onConfirm: () -> Unit) {
    val colors = DeployerTheme.colors
    val (title, text, button) = when (action) {
        Confirm.Update -> Triple(
            "Update ${d.name}?",
            "It is rebuilt from the latest commit of ${d.ref}. Visitors see a progress page while the new version starts. " +
                "If it fails, the current version comes back.",
            "Update",
        )
        Confirm.Stop -> Triple("Stop ${d.name}?", "Visitors see an offline page until you start it again.", "Stop")
        Confirm.Destroy -> Triple(
            "Destroy ${d.name}?",
            "${d.url} and everything in it will be removed. This can't be undone.",
            "Destroy",
        )
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(text) },
        confirmButton = {
            TextButton(onClick = onConfirm) {
                Text(button, color = if (action == Confirm.Destroy) colors.bad else colors.accent)
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}
