package io.github.filstawski.deployer.ui.deploy

import androidx.compose.foundation.BorderStroke
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
fun NewDeploymentScreen(viewModel: NewDeploymentViewModel, onClose: () -> Unit, onOpened: (name: String) -> Unit) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val colors = DeployerTheme.colors

    LaunchedEffect(state.opened) { state.opened?.let(onOpened) }

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
            item { Label("Branch", Modifier.padding(top = 10.dp)) }
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
                BranchRow(state, name, onClick = { viewModel.selectBranch(name) })
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
            if (state.branch != null) {
                item { Label("Name", Modifier.padding(top = 10.dp)) }
                item { NameField(state, viewModel) }
                if (state.existing == null) {
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
    ExposedDropdownMenuBox(expanded = open, onExpandedChange = { open = it }) {
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

@Composable
private fun BranchRow(state: NewDeploymentState, name: String, onClick: () -> Unit) {
    val colors = DeployerTheme.colors
    val selected = name == state.branch
    val branch = state.branches.firstOrNull { it.name == name }
    val live = state.live.firstOrNull { it.projectId == state.project?.id && it.ref == name }
    Surface(
        onClick = onClick,
        shape = RoundedCornerShape(14.dp),
        color = if (selected) colors.accentSoft else colors.surface,
        border = if (selected) BorderStroke(1.dp, colors.accent) else null,
        modifier = Modifier.semantics {
            role = Role.RadioButton
            this.selected = selected
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
