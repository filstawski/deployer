package io.github.filstawski.deployer.ui.deploy

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Spacer
import androidx.compose.ui.draw.clip
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.ui.components.Panel
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
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
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.toggleableState
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.github.filstawski.deployer.ui.components.CommandBox
import io.github.filstawski.deployer.ui.components.ErrorPanel
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun NewDeploymentScreen(
    viewModel: NewDeploymentViewModel,
    onClose: () -> Unit,
    onOpened: (name: String) -> Unit,
    /** Several branches were started; [message] says what. */
    onFinished: (message: String) -> Unit,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val colors = DeployerTheme.colors

    LaunchedEffect(state.opened) { state.opened?.let(onOpened) }
    LaunchedEffect(state.finished) { state.finished?.let(onFinished) }

    Scaffold(
        containerColor = colors.background,
        topBar = {
            TopAppBar(
                navigationIcon = { IconButton(onClick = onClose) { Icon(DeployerIcons.Close, contentDescription = "Close") } },
                title = { Text("New deployment", fontWeight = FontWeight.SemiBold) },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = colors.background),
            )
        },
        bottomBar = {
            if (state.projects.isNotEmpty()) {
                Column(
                    Modifier
                        .navigationBarsPadding()
                        .imePadding()
                        .padding(horizontal = 20.dp, vertical = 12.dp),
                    verticalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    state.error?.let { ErrorPanel(it) }
                    if (state.failures.isNotEmpty()) Failures(state.failures, state.started)
                    Button(
                        onClick = viewModel::submit,
                        enabled = state.canSubmit,
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(52.dp),
                    ) {
                        if (state.submitting) {
                            CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = colors.onAccent)
                        } else {
                            val existing = state.existing
                            Text(
                                when {
                                    state.selected.size > 1 -> {
                                        val updates = state.plans.count { it.update != null }
                                        val new = state.plans.size - updates
                                        when {
                                            updates == 0 -> "Deploy $new branches"
                                            new == 0 -> "Update $updates previews"
                                            else -> "Deploy $new, update $updates"
                                        }
                                    }
                                    existing != null -> "Update ${existing.name}"
                                    state.branch != null -> "Deploy ${state.branch}"
                                    else -> "Deploy"
                                },
                                fontWeight = FontWeight.SemiBold,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                }
            }
        },
    ) { padding ->
        if (!state.loaded) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) { CircularProgressIndicator(color = colors.accent) }
            return@Scaffold
        }
        LazyColumn(
            Modifier
                .fillMaxSize()
                .padding(padding),
            contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 4.dp, bottom = 16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            if (state.projects.isEmpty()) {
                item {
                    state.error?.let { ErrorPanel(it, onAction = viewModel::load) } ?: NoProjects()
                }
                return@LazyColumn
            }
            item { Label("Project") }
            item { ProjectPicker(state, viewModel) }
            item {
                Row(Modifier.padding(top = 10.dp), verticalAlignment = Alignment.Bottom) {
                    Label(if (state.picking) "Branches · ${state.selected.size} chosen" else "Branch", Modifier.weight(1f))
                    Label(if (state.picking) "Tap to add or remove" else "Hold a branch to choose several")
                }
            }
            item {
                OutlinedTextField(
                    value = state.query,
                    onValueChange = viewModel::setQuery,
                    placeholder = { Text("Search, or type a branch or commit") },
                    leadingIcon = { Icon(DeployerIcons.Search, contentDescription = null, modifier = Modifier.size(20.dp)) },
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Done, autoCorrectEnabled = false),
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            state.branchError?.let { error -> item { ErrorPanel(error, title = "Can't list branches") } }
            if (state.loadingBranches) {
                item {
                    Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
                        CircularProgressIndicator(Modifier.size(24.dp), color = colors.accent)
                    }
                }
            }
            items(state.matches.take(MAX_BRANCHES), key = { "branch:$it" }) { name ->
                BranchRow(state, name, onClick = { viewModel.selectBranch(name) }, onLongClick = { viewModel.pick(name) })
            }
            if (state.matches.size > MAX_BRANCHES) {
                item {
                    Text(
                        "${state.matches.size - MAX_BRANCHES} more; search to narrow the list.",
                        style = DeployerType.small,
                        color = colors.muted,
                        modifier = Modifier.padding(4.dp),
                    )
                }
            }
            if (state.selected.size > 1) {
                item { Label("What happens", Modifier.padding(top = 10.dp)) }
                items(state.plans, key = { "plan:${it.branch}" }) { plan -> PlanRow(state, plan, viewModel) }
            } else if (state.branch != null) {
                item { Label("Name", Modifier.padding(top = 10.dp)) }
                item { NameField(state, viewModel) }
            }
            if (state.overLimit > 0) {
                item { OverLimit(state) }
            }
            if (state.plans.any { it.update == null }) {
                item { Label("Lifetime", Modifier.padding(top = 10.dp)) }
                item {
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        state.lifetimes.forEach { option ->
                            val selected = option == state.lifetime
                            Surface(
                                onClick = { viewModel.setLifetime(option) },
                                shape = RoundedCornerShape(22.dp),
                                color = if (selected) colors.accentSoft else colors.background,
                                border = BorderStroke(1.dp, if (selected) colors.accent else colors.line),
                                modifier = Modifier.semantics {
                                    role = Role.RadioButton
                                    this.selected = selected
                                },
                            ) {
                                Text(
                                    option.label + when {
                                        option.isMax -> " (max)"
                                        option.isDefault -> " (default)"
                                        else -> ""
                                    },
                                    color = if (selected) colors.accent else colors.text,
                                    fontSize = 14.sp,
                                    fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Medium,
                                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 11.dp),
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

private const val MAX_BRANCHES = 100

@Composable
private fun Label(text: String, modifier: Modifier = Modifier) {
    Text(text, style = DeployerType.small, color = DeployerTheme.colors.muted, modifier = modifier)
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ProjectPicker(state: NewDeploymentState, viewModel: NewDeploymentViewModel) {
    var open by remember { mutableStateOf(false) }
    // Locked while deploying: the result is about this project's branches.
    ExposedDropdownMenuBox(expanded = open, onExpandedChange = { open = it && !state.submitting }) {
        OutlinedTextField(
            value = state.project?.name.orEmpty(),
            onValueChange = {},
            readOnly = true,
            singleLine = true,
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = open) },
            modifier = Modifier
                .fillMaxWidth()
                .menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable),
        )
        ExposedDropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            state.projects.forEach { project ->
                DropdownMenuItem(
                    text = {
                        Column {
                            Text(project.name)
                            Text(project.repository, style = DeployerType.small, color = DeployerTheme.colors.muted)
                        }
                    },
                    onClick = {
                        open = false
                        if (project != state.project) viewModel.selectProject(project)
                    },
                )
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun BranchRow(state: NewDeploymentState, name: String, onClick: () -> Unit, onLongClick: () -> Unit) {
    val colors = DeployerTheme.colors
    val haptics = LocalHapticFeedback.current
    val selected = name in state.selected
    val branch = state.branches.firstOrNull { it.name == name }
    val live = state.live.firstOrNull { it.projectId == state.project?.id && it.ref == name }
    val shape = RoundedCornerShape(14.dp)
    Surface(
        shape = shape,
        color = if (selected) colors.accentSoft else colors.surface,
        border = if (selected) BorderStroke(1.dp, colors.accent) else null,
        modifier = Modifier
            .clip(shape)
            .combinedClickable(
                onClick = onClick,
                onLongClick = {
                    haptics.performHapticFeedback(HapticFeedbackType.LongPress)
                    onLongClick()
                },
                onLongClickLabel = "Choose several",
            )
            .semantics {
                if (state.picking) {
                    role = Role.Checkbox
                    toggleableState = ToggleableState(selected)
                } else {
                    role = Role.RadioButton
                    this.selected = selected
                }
            },
    ) {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(name, style = DeployerType.mono, fontSize = 14.sp, color = colors.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                val details = listOfNotNull(
                    "default".takeIf { branch?.default == true },
                    branch?.commit?.take(7),
                    live?.let { "live as ${it.name}" },
                    "not listed; deploys as typed".takeIf { branch == null },
                )
                Text(details.joinToString(" · "), style = DeployerType.small, color = colors.muted)
            }
            if (selected) Icon(DeployerIcons.Check, contentDescription = null, tint = colors.accent, modifier = Modifier.size(20.dp))
        }
    }
}

@Composable
private fun NameField(state: NewDeploymentState, viewModel: NewDeploymentViewModel) {
    val colors = DeployerTheme.colors
    val existing = state.existing
    if (existing != null) {
        Text(
            "${existing.name} already runs this branch. Updating rebuilds it from the latest commit, keeping its URL and lifetime.",
            style = DeployerType.body,
            color = colors.text,
        )
        TextButton(onClick = { viewModel.setCreateNew(true) }) { Text("Create a new preview instead") }
        return
    }
    state.match?.let { match ->
        TextButton(onClick = { viewModel.setCreateNew(false) }) { Text("Update ${match.name} instead") }
    }
    OutlinedTextField(
        value = state.name,
        onValueChange = viewModel::setName,
        singleLine = true,
        isError = state.nameError != null,
        textStyle = DeployerType.mono.copy(fontSize = 15.sp),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Ascii, imeAction = ImeAction.Done, autoCorrectEnabled = false),
        supportingText = {
            val preview = state.status?.previewUrl?.replace("{name}", state.name.ifEmpty { "name" })
            Text(state.nameError ?: preview?.let { "Preview at $it" } ?: "")
        },
        modifier = Modifier.fillMaxWidth(),
    )
}

@Composable
private fun NoProjects() {
    val colors = DeployerTheme.colors
    Column(Modifier.padding(top = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("No projects yet", style = DeployerType.body, fontWeight = FontWeight.SemiBold, color = colors.text)
        Text(
            "A project is a repository the server builds from. Running deployer deploy in a repository registers it; or register one directly:",
            style = DeployerType.body,
            color = colors.muted,
        )
        CommandBox("deployer project add shop --repo https://github.com/acme/shop")
        HorizontalDivider(color = colors.line)
    }
}

/** One of several chosen branches: the preview it creates, or the one it updates. */
@Composable
private fun PlanRow(state: NewDeploymentState, plan: Plan, viewModel: NewDeploymentViewModel) {
    val colors = DeployerTheme.colors
    val match = state.matchFor(plan.branch)
    Panel(Modifier.fillMaxWidth()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(plan.branch, style = DeployerType.mono, fontSize = 14.sp, color = colors.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    if (plan.update != null) "Updates ${plan.name}" else "New preview ${plan.name}",
                    style = DeployerType.small,
                    color = colors.muted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (match != null) {
                TextButton(onClick = { viewModel.setCreateNew(plan.branch, plan.update != null) }) {
                    Text(if (plan.update != null) "New instead" else "Update instead")
                }
            }
        }
    }
}

/** Warns when the new previews won't all fit under the server's limit. */
@Composable
private fun OverLimit(state: NewDeploymentState) {
    val colors = DeployerTheme.colors
    val limits = state.status?.deployments ?: return
    Panel(Modifier.fillMaxWidth(), border = colors.busy.copy(alpha = 0.5f)) {
        Text(
            "The server allows ${limits.max} live deployments and has ${limits.live}. " +
                "${state.overLimit} of these won't start; destroy some first, or choose fewer.",
            style = DeployerType.small,
            color = colors.text,
        )
    }
}

/** The branches that didn't start, with why; they stay chosen to try again. */
@Composable
private fun Failures(failures: List<Pair<String, ApiException>>, started: Int) {
    val colors = DeployerTheme.colors
    Panel(Modifier.fillMaxWidth(), border = colors.bad.copy(alpha = 0.4f)) {
        Text(
            if (failures.size == 1) "1 branch didn't start" else "${failures.size} branches didn't start",
            style = DeployerType.body,
            fontWeight = FontWeight.SemiBold,
            color = colors.text,
        )
        failures.forEach { (branch, error) ->
            Spacer(Modifier.height(6.dp))
            Text(branch, style = DeployerType.mono, color = colors.text)
            Text(error.message, style = DeployerType.small, color = colors.bad)
        }
        Spacer(Modifier.height(6.dp))
        Text(
            if (started == 0) "They stay chosen to try again." else "The other $started started. These stay chosen to try again.",
            style = DeployerType.small,
            color = colors.muted,
        )
    }
}
