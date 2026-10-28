package io.github.filstawski.deployer.ui.deployments

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.model.BulkAction
import io.github.filstawski.deployer.model.deployments
import io.github.filstawski.deployer.model.names
import io.github.filstawski.deployer.ui.components.Panel
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType

/** A card's check mark while selecting: filled when selected. */
@Composable
fun SelectionMark(selected: Boolean) {
    val colors = DeployerTheme.colors
    Box(
        Modifier
            .size(18.dp)
            .clip(CircleShape)
            .then(if (selected) Modifier.background(colors.accent) else Modifier.border(2.dp, colors.muted, CircleShape)),
        contentAlignment = Alignment.Center,
    ) {
        if (selected) Icon(DeployerIcons.Check, contentDescription = null, tint = colors.onAccent, modifier = Modifier.size(12.dp))
    }
}


/**
 * The selection's count and what can be done to it, each action with how many
 * of the selected it applies to. It sits at the bottom, so no card moves or
 * hides when selecting starts.
 */
@Composable
fun BulkActions(state: DeploymentsState, onClose: () -> Unit, onSelectAll: () -> Unit, onAction: (BulkAction) -> Unit) {
    val colors = DeployerTheme.colors
    val busy = state.bulk?.finished == false
    Surface(color = colors.sheet) {
        Column(Modifier.navigationBarsPadding()) {
            Row(Modifier.padding(start = 4.dp, end = 8.dp, top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = onClose) { Icon(DeployerIcons.Close, contentDescription = "Stop selecting", tint = colors.text) }
                Text(
                    "${state.selected.size} selected",
                    style = DeployerType.body,
                    fontWeight = FontWeight.SemiBold,
                    color = colors.text,
                    modifier = Modifier.weight(1f),
                )
                TextButton(onClick = onSelectAll) { Text("Select all") }
            }
            Row(
                Modifier.padding(start = 12.dp, end = 12.dp, bottom = 10.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                listOf(
                    BulkAction.Start to DeployerIcons.Play,
                    BulkAction.Stop to DeployerIcons.Stop,
                    BulkAction.Update to DeployerIcons.Update,
                    BulkAction.Extend to DeployerIcons.Clock,
                    BulkAction.Destroy to DeployerIcons.Trash,
                ).forEach { (action, icon) ->
                    ActionButton(
                        action, icon,
                        count = state.targets(action).size,
                        enabled = !busy,
                        destructive = action == BulkAction.Destroy,
                        onClick = { onAction(action) },
                    )
                }
            }
        }
    }
}

@Composable
private fun RowScope.ActionButton(action: BulkAction, icon: ImageVector, count: Int, enabled: Boolean, destructive: Boolean, onClick: () -> Unit) {
    val colors = DeployerTheme.colors
    val on = enabled && count > 0
    val tint = (if (destructive) colors.bad else colors.text).copy(alpha = if (on) 1f else 0.35f)
    Surface(
        onClick = onClick,
        enabled = on,
        shape = RoundedCornerShape(14.dp),
        color = colors.surface,
        modifier = Modifier
            .weight(1f)
            .height(68.dp),
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
            Icon(icon, contentDescription = null, tint = tint, modifier = Modifier.size(20.dp))
            Spacer(Modifier.height(4.dp))
            Text(if (count > 0) "${action.verb} $count" else action.verb, style = DeployerType.small, fontWeight = FontWeight.Medium, color = tint, maxLines = 1)
        }
    }
}

/** Asks before stopping, updating, or destroying several at once, naming them. */
@Composable
fun BulkConfirm(action: BulkAction, targets: List<Deployment>, onDismiss: () -> Unit, onConfirm: () -> Unit) {
    val colors = DeployerTheme.colors
    val list = names(targets.map { it.name })
    val text = when (action) {
        BulkAction.Stop -> "$list. Visitors see an offline page until you start them again."
        BulkAction.Update -> "$list. Each is rebuilt from the latest commit of its branch and started, stopped ones too. Visitors " +
            "see a progress page while the new version starts; if it fails, a running one keeps its current version."
        BulkAction.Destroy -> "$list and everything in them will be removed. This can't be undone."
        else -> list
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("${action.verb} ${deployments(targets.size)}?") },
        text = { Text(text) },
        confirmButton = {
            TextButton(onClick = onConfirm) {
                Text(action.verb, color = if (action == BulkAction.Destroy) colors.bad else colors.accent)
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/** How far a bulk action got, and what failed once it is done. */
@Composable
fun BulkProgress(run: BulkRun, onDismiss: () -> Unit) {
    val colors = DeployerTheme.colors
    if (!run.finished) {
        Surface(shape = RoundedCornerShape(16.dp), color = colors.accentSoft) {
            Row(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = colors.accent)
                Column(Modifier.weight(1f)) {
                    Text("${run.action.doing} ${deployments(run.total)}…", style = DeployerType.body, color = colors.text)
                    Text("${run.done} of ${run.total} done", style = DeployerType.small, color = colors.muted)
                }
                // Updates can build for a long time; their notifications still come.
                TextButton(onClick = onDismiss) { Text("Hide") }
            }
        }
        return
    }
    Panel(Modifier.fillMaxWidth(), border = colors.bad.copy(alpha = 0.4f)) {
        val succeeded = run.total - run.failures.size
        Text(
            "${run.action.done} $succeeded of ${run.total}",
            style = DeployerType.body,
            fontWeight = FontWeight.SemiBold,
            color = colors.text,
        )
        run.failures.forEach { (name, error) ->
            Spacer(Modifier.height(6.dp))
            Text(name, style = DeployerType.mono, color = colors.text)
            Text(error.message, style = DeployerType.small, color = colors.bad)
            error.hint?.let { Text(it, style = DeployerType.mono, color = colors.muted) }
        }
        TextButton(onClick = onDismiss, modifier = Modifier.align(Alignment.End)) { Text("Dismiss") }
    }
}
