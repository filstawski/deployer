package io.github.filstawski.deployer.ui.logs

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import io.github.filstawski.deployer.ui.components.ErrorPanel
import io.github.filstawski.deployer.ui.components.rememberCopy
import io.github.filstawski.deployer.ui.components.shareText
import io.github.filstawski.deployer.ui.theme.DarkPalette
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LogsScreen(viewModel: LogsViewModel, name: String, onBack: () -> Unit) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val colors = DeployerTheme.colors
    val copy = rememberCopy()
    val context = LocalContext.current
    val list = rememberLazyListState()
    val scope = rememberCoroutineScope()
    // Follow new lines until the reader scrolls up; scrolling back down resumes.
    var follow by remember { mutableStateOf(true) }

    val lifecycle = LocalLifecycleOwner.current
    LaunchedEffect(viewModel, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { viewModel.poll() }
    }
    LaunchedEffect(list) {
        snapshotFlow { list.isScrollInProgress }.collect { scrolling ->
            if (!scrolling) follow = !list.canScrollForward
        }
    }
    LaunchedEffect(state.lines.lastOrNull()?.number, state.build) {
        if (follow && state.lines.isNotEmpty()) list.scrollToItem(state.lines.lastIndex)
    }

    Scaffold(
        containerColor = colors.background,
        topBar = {
            TopAppBar(
                navigationIcon = { IconButton(onClick = onBack) { Icon(DeployerIcons.Back, contentDescription = "Back") } },
                title = {
                    Column {
                        Text("Logs", fontWeight = FontWeight.SemiBold)
                        Text(name, style = DeployerType.mono, color = colors.muted)
                    }
                },
                actions = {
                    IconButton(onClick = { copy("Logs", viewModel.text()) }, enabled = state.lines.isNotEmpty()) {
                        Icon(DeployerIcons.Copy, contentDescription = "Copy logs", modifier = Modifier.size(20.dp))
                    }
                    IconButton(onClick = { context.shareText(viewModel.text(), "$name logs") }, enabled = state.lines.isNotEmpty()) {
                        Icon(DeployerIcons.Share, contentDescription = "Share logs", modifier = Modifier.size(20.dp))
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = colors.background),
            )
        },
    ) { padding ->
        Column(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .navigationBarsPadding(),
        ) {
            SingleChoiceSegmentedButtonRow(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 20.dp, vertical = 8.dp),
            ) {
                listOf(false to "App", true to "Build").forEachIndexed { index, (build, label) ->
                    SegmentedButton(
                        selected = state.build == build,
                        onClick = { follow = true; viewModel.setBuild(build) },
                        shape = SegmentedButtonDefaults.itemShape(index, 2),
                        icon = {},
                    ) { Text(label) }
                }
            }
            state.error?.let { ErrorPanel(it, Modifier.padding(horizontal = 12.dp, vertical = 4.dp)) }
            Surface(
                shape = RoundedCornerShape(16.dp),
                color = colors.code,
                border = BorderStroke(1.dp, colors.line),
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp),
            ) {
                when {
                    !state.loaded -> Box(contentAlignment = Alignment.Center) { CircularProgressIndicator(color = colors.accent) }
                    state.lines.isEmpty() -> Box(contentAlignment = Alignment.Center) {
                        Text(
                            if (state.build) "No build log yet" else "No output yet",
                            style = DeployerType.small,
                            color = Color(0xFFA0A6B0),
                        )
                    }
                    else -> SelectionContainer {
                        LazyColumn(state = list, contentPadding = androidx.compose.foundation.layout.PaddingValues(14.dp)) {
                            items(state.lines, key = { it.number }) { line ->
                                Text(
                                    line.text.ifEmpty { " " },
                                    style = DeployerType.log,
                                    // Logs keep the dark terminal look in both themes.
                                    color = line.tone?.let { DarkPalette.tone(it) } ?: Color(0xFFD5D9DF),
                                )
                            }
                        }
                    }
                }
            }
            Row(
                Modifier.padding(horizontal = 20.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Box(
                    Modifier
                        .size(8.dp)
                        .clip(CircleShape)
                        .background(if (state.paused) colors.idle else colors.ok),
                )
                Text(
                    when {
                        state.paused -> "Paused"
                        follow -> "Following new lines"
                        else -> "Scrolled back"
                    },
                    style = DeployerType.small,
                    color = colors.muted,
                    modifier = Modifier.weight(1f),
                )
                OutlinedButton(onClick = { viewModel.setPaused(!state.paused) }, modifier = Modifier.height(44.dp)) {
                    Icon(if (state.paused) DeployerIcons.Play else DeployerIcons.Pause, contentDescription = null, modifier = Modifier.size(16.dp))
                    Text(if (state.paused) "Resume" else "Pause", color = colors.text, modifier = Modifier.padding(start = 6.dp))
                }
                if (!follow) {
                    Surface(
                        onClick = {
                            follow = true
                            scope.launch { if (state.lines.isNotEmpty()) list.scrollToItem(state.lines.lastIndex) }
                        },
                        shape = RoundedCornerShape(22.dp),
                        color = colors.accentSoft,
                    ) {
                        Row(
                            Modifier
                                .height(44.dp)
                                .padding(horizontal = 16.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(6.dp),
                        ) {
                            Icon(DeployerIcons.Latest, contentDescription = null, tint = colors.accent, modifier = Modifier.size(16.dp))
                            Text("Latest", color = colors.accent, fontWeight = FontWeight.Medium)
                        }
                    }
                }
            }
        }
    }
}
