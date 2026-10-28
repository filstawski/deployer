package io.github.filstawski.deployer.ui.servers

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.data.Connection
import io.github.filstawski.deployer.model.serverIsNewer
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType

/** Switches between saved servers, and shows what this phone's token may do. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServersSheet(
    connections: List<Connection>,
    activeId: String?,
    status: ServerStatus?,
    appVersion: String,
    notifications: Boolean,
    onNotifications: (Boolean) -> Unit,
    onSelect: (Connection) -> Unit,
    onAdd: () -> Unit,
    onRemove: (Connection) -> Unit,
    onDismiss: () -> Unit,
) {
    val colors = DeployerTheme.colors
    var confirmRemove by remember { mutableStateOf<Connection?>(null) }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = colors.sheet,
    ) {
        Column(
            Modifier
                .padding(horizontal = 12.dp)
                .padding(bottom = 16.dp)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                "Servers",
                fontSize = 21.sp,
                fontWeight = FontWeight.SemiBold,
                color = colors.text,
                modifier = Modifier.padding(horizontal = 8.dp, vertical = 8.dp),
            )
            connections.forEach { connection ->
                val active = connection.id == activeId
                Surface(
                    onClick = { onSelect(connection) },
                    shape = RoundedCornerShape(16.dp),
                    color = if (active) colors.surface else colors.sheet,
                ) {
                    Row(
                        Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 12.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        Icon(DeployerIcons.Server, contentDescription = null, tint = colors.muted, modifier = Modifier.size(22.dp))
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                            Text(connection.name, style = DeployerType.body, fontWeight = FontWeight.Medium, color = colors.text)
                            Text(connection.url, style = DeployerType.mono, color = colors.muted)
                            if (active && status != null) {
                                val role = status.token.role?.let { " · $it" } ?: ""
                                Text(
                                    "Token ${status.token.name} (${status.token.id})$role · server ${status.version}",
                                    style = DeployerType.small,
                                    color = colors.muted,
                                )
                            }
                        }
                        if (active) {
                            Icon(DeployerIcons.Check, contentDescription = "Active", tint = colors.accent, modifier = Modifier.size(20.dp))
                        }
                    }
                }
            }
            if (status != null && serverIsNewer(appVersion, status.version)) {
                val uri = LocalUriHandler.current
                Surface(
                    onClick = { uri.openUri("https://github.com/filstawski/deployer/releases/tag/${status.version}") },
                    shape = RoundedCornerShape(16.dp),
                    color = colors.accentSoft,
                    modifier = Modifier.padding(top = 4.dp),
                ) {
                    Column(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 10.dp)) {
                        Text("A newer app is available", style = DeployerType.body, fontWeight = FontWeight.Medium, color = colors.text)
                        Text(
                            "This app is $appVersion; the server runs ${status.version}. Tap to download the matching app.",
                            style = DeployerType.small,
                            color = colors.muted,
                        )
                    }
                }
            }
            Row(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    Text("Notifications", style = DeployerType.body, fontWeight = FontWeight.Medium, color = colors.text)
                    Text("Ready, failed, updated, and about to expire", style = DeployerType.small, color = colors.muted)
                }
                Switch(checked = notifications, onCheckedChange = onNotifications)
            }
            Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                TextButton(onClick = onAdd) {
                    Icon(DeployerIcons.Plus, contentDescription = null, modifier = Modifier.size(18.dp))
                    Text("Add server", modifier = Modifier.padding(start = 6.dp))
                }
                connections.firstOrNull { it.id == activeId }?.let { active ->
                    TextButton(onClick = { confirmRemove = active }) {
                        Text("Remove from this phone", color = colors.bad)
                    }
                }
            }
        }
    }

    confirmRemove?.let { connection ->
        AlertDialog(
            onDismissRequest = { confirmRemove = null },
            title = { Text("Remove ${connection.name}?") },
            text = {
                val revoke = status?.takeIf { connection.id == activeId }?.let { "deployer token revoke ${it.token.id}" }
                    ?: "deployer token revoke"
                Text("This phone forgets the server and its token. The token stays valid until an administrator runs $revoke.")
            },
            confirmButton = {
                TextButton(onClick = { confirmRemove = null; onRemove(connection) }) { Text("Remove", color = colors.bad) }
            },
            dismissButton = { TextButton(onClick = { confirmRemove = null }) { Text("Cancel") } },
        )
    }
}
