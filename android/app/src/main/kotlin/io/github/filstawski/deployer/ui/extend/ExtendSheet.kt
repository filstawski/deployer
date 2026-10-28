package io.github.filstawski.deployer.ui.extend

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.model.TimeFormat
import io.github.filstawski.deployer.model.durationLabel
import io.github.filstawski.deployer.model.expiryAfter
import io.github.filstawski.deployer.model.extendOptions
import io.github.filstawski.deployer.model.formatDuration
import io.github.filstawski.deployer.model.ttl
import io.github.filstawski.deployer.ui.components.ErrorPanel
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType
import java.time.Instant
import kotlin.time.Duration
import kotlinx.coroutines.launch

/**
 * Offers extensions up to what the server still allows and applies the
 * chosen one. [extend] calls the API; [onExtended] receives the result.
 */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun ExtendSheet(
    deployment: Deployment,
    maxTtl: Duration,
    times: TimeFormat,
    extend: suspend (seconds: Long) -> Deployment,
    onExtended: (Deployment) -> Unit,
    onDismiss: () -> Unit,
) {
    val colors = DeployerTheme.colors
    val now = remember { Instant.now() }
    val options = remember(deployment, maxTtl) { deployment.extendOptions(maxTtl, now) }
    var selected by remember { mutableStateOf(options.firstOrNull { it.by.inWholeHours == 24L } ?: options.firstOrNull()) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<ApiException?>(null) }
    val scope = rememberCoroutineScope()
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState, containerColor = colors.sheet) {
        Column(
            Modifier
                .padding(horizontal = 20.dp)
                .padding(bottom = 20.dp)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(18.dp),
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text("Extend", fontSize = 21.sp, fontWeight = FontWeight.SemiBold, color = colors.text)
                    Text(deployment.name, fontSize = 21.sp, style = DeployerType.name, color = colors.text)
                }
                val expires = deployment.expiresAt
                val current = when {
                    expires == null -> "It will run for ${durationLabel(deployment.ttl())} once it's ready."
                    expires.isAfter(now) -> "It expires ${times.at(expires, now)}."
                    else -> "It expired ${times.ago(expires, now)}. Extending gives it a new lifetime from now."
                }
                Text(
                    "$current The server allows at most ${durationLabel(maxTtl)} from now.",
                    style = DeployerType.body,
                    color = colors.muted,
                )
            }

            if (options.isEmpty()) {
                Text("It already has the longest lifetime the server allows.", style = DeployerType.body, color = colors.text)
            } else {
                FlowRow(
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                    maxItemsInEachRow = 3,
                ) {
                    options.forEach { option ->
                        val isSelected = option == selected
                        Surface(
                            onClick = { selected = option; error = null },
                            shape = RoundedCornerShape(14.dp),
                            color = if (isSelected) colors.accentSoft else colors.surfaceRaised,
                            border = BorderStroke(1.dp, if (isSelected) colors.accent else colors.line),
                            modifier = Modifier
                                .weight(1f)
                                .height(52.dp)
                                .semantics {
                                    role = Role.RadioButton
                                    this.selected = isSelected
                                },
                        ) {
                            Row(horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
                                if (isSelected) {
                                    Icon(DeployerIcons.Check, contentDescription = null, tint = colors.accent, modifier = Modifier.size(16.dp))
                                }
                                Text(
                                    option.label,
                                    color = if (isSelected) colors.accent else colors.text,
                                    fontWeight = if (isSelected) FontWeight.SemiBold else FontWeight.Medium,
                                    fontSize = 14.sp,
                                    modifier = Modifier.padding(start = if (isSelected) 6.dp else 0.dp),
                                )
                            }
                        }
                    }
                }
                selected?.let { option ->
                    Surface(shape = RoundedCornerShape(16.dp), color = colors.background) {
                        Row(
                            Modifier
                                .fillMaxWidth()
                                .padding(horizontal = 16.dp, vertical = 14.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            val after = deployment.expiryAfter(option.by, now)
                            Column(Modifier.weight(1f)) {
                                Text(if (after != null) "New expiry" else "New lifetime", style = DeployerType.small, color = colors.muted)
                                Text(
                                    after?.let { times.at(it, now).replaceFirstChar(Char::uppercase) }
                                        ?: durationLabel(deployment.ttl() + option.by),
                                    fontSize = 17.sp,
                                    fontWeight = FontWeight.Medium,
                                    color = colors.text,
                                )
                            }
                            Text("+" + formatDuration(option.by), style = DeployerType.mono, color = colors.muted)
                        }
                    }
                }
            }

            error?.let { ErrorPanel(it) }

            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                TextButton(onClick = onDismiss, modifier = Modifier.height(52.dp)) { Text("Cancel", fontSize = 15.sp) }
                Button(
                    onClick = {
                        val option = selected ?: return@Button
                        busy = true
                        error = null
                        scope.launch {
                            try {
                                onExtended(extend(option.by.inWholeSeconds))
                            } catch (e: ApiException) {
                                error = e
                            } finally {
                                busy = false
                            }
                        }
                    },
                    enabled = selected != null && !busy,
                    modifier = Modifier
                        .weight(1f)
                        .height(52.dp),
                ) {
                    if (busy) {
                        CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = colors.onAccent)
                    } else {
                        Text(
                            selected?.let { "Extend by " + if (it.isMax) formatDuration(it.by) else durationLabel(it.by) } ?: "Extend",
                            fontWeight = FontWeight.SemiBold,
                            fontSize = 15.sp,
                        )
                    }
                }
            }
        }
    }
}
