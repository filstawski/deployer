package io.github.filstawski.deployer.ui.extend

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
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
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.model.commonDurations
import io.github.filstawski.deployer.model.deployments
import io.github.filstawski.deployer.model.durationLabel
import io.github.filstawski.deployer.model.extensionFor
import io.github.filstawski.deployer.model.formatDuration
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType
import java.time.Instant
import kotlin.time.Duration

/**
 * Extends several deployments at once. Each gets the chosen extension, or as
 * much of it as the server still allows that one; "Max" gives each its own
 * maximum. [onExtend] receives the choice, null for "Max".
 */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun BulkExtendSheet(
    deployments: List<Deployment>,
    maxTtl: Duration,
    onExtend: (Duration?) -> Unit,
    onDismiss: () -> Unit,
) {
    val colors = DeployerTheme.colors
    val now = remember { Instant.now() }
    val most = remember(deployments) { deployments.mapNotNull { it.extensionFor(null, maxTtl, now) }.maxOrNull() }
    // Choices are the usual extensions that at least one can take, then Max (null).
    val choices: List<Duration?> = remember(most) { most?.let { m -> commonDurations.filter { it < m } + null }.orEmpty() }
    var choice by remember { mutableStateOf(choices.firstOrNull { it?.inWholeHours == 24L } ?: choices.firstOrNull()) }

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true), containerColor = colors.sheet) {
        Column(
            Modifier
                .padding(horizontal = 20.dp)
                .padding(bottom = 20.dp)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(18.dp),
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text("Extend ${deployments(deployments.size)}", fontSize = 21.sp, fontWeight = FontWeight.SemiBold, color = colors.text)
                Text(
                    "Each gets your choice, or as much of it as the server still allows that one: at most ${durationLabel(maxTtl)} from now.",
                    style = DeployerType.body,
                    color = colors.muted,
                )
            }
            if (choices.isEmpty()) {
                Text("They already have the longest lifetimes the server allows.", style = DeployerType.body, color = colors.text)
            } else {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp), maxItemsInEachRow = 3) {
                    choices.forEach { option ->
                        val selected = option == choice
                        Surface(
                            onClick = { choice = option },
                            shape = RoundedCornerShape(14.dp),
                            color = if (selected) colors.accentSoft else colors.surfaceRaised,
                            border = BorderStroke(1.dp, if (selected) colors.accent else colors.line),
                            modifier = Modifier
                                .weight(1f)
                                .height(52.dp)
                                .semantics {
                                    role = Role.RadioButton
                                    this.selected = selected
                                },
                        ) {
                            Row(horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
                                if (selected) Icon(DeployerIcons.Check, contentDescription = null, tint = colors.accent, modifier = Modifier.size(16.dp))
                                Text(
                                    option?.let { "+" + durationLabel(it) } ?: "Max",
                                    color = if (selected) colors.accent else colors.text,
                                    fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Medium,
                                    fontSize = 14.sp,
                                    modifier = Modifier.padding(start = if (selected) 6.dp else 0.dp),
                                )
                            }
                        }
                    }
                }
                Text(summary(deployments, choice, maxTtl, now), style = DeployerType.small, color = colors.muted)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                TextButton(onClick = onDismiss, modifier = Modifier.height(52.dp)) { Text("Cancel", fontSize = 15.sp) }
                Button(
                    onClick = { onExtend(choice) },
                    enabled = choices.isNotEmpty(),
                    modifier = Modifier
                        .weight(1f)
                        .height(52.dp),
                ) {
                    Text("Extend ${deployments(deployments.size)}", fontWeight = FontWeight.SemiBold, fontSize = 15.sp)
                }
            }
        }
    }
}

/** What each deployment gets: "All 3 get +1 day", "2 get +1 day; 1 gets 6h, its most". */
private fun summary(deployments: List<Deployment>, choice: Duration?, maxTtl: Duration, now: Instant): String {
    val by = deployments.map { it.extensionFor(choice, maxTtl, now) }
    val full = if (choice == null) by.count { it != null } else by.count { it == choice }
    val less = by.filter { it != null && (choice != null && it < choice) }
    val none = by.count { it == null }
    val what = choice?.let { "+" + durationLabel(it) } ?: "its maximum"
    return listOfNotNull(
        when {
            full == deployments.size && choice == null -> "Each gets its maximum."
            full == deployments.size -> "All get $what."
            full > 0 -> "$full get $what."
            else -> null
        },
        less.takeIf { it.isNotEmpty() }?.let { l ->
            if (l.size == 1) "1 gets ${formatDuration(l.single()!!)}, its most." else "${l.size} get less, as much as each still may."
        },
        if (none == 1) "1 can't be extended further." else if (none > 1) "$none can't be extended further." else null,
    ).joinToString(" ")
}
