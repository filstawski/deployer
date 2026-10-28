package io.github.filstawski.deployer.ui.components

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.model.Tone
import io.github.filstawski.deployer.model.label
import io.github.filstawski.deployer.model.symbol
import io.github.filstawski.deployer.model.tone
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType
import java.time.Instant
import kotlinx.coroutines.delay
import io.github.filstawski.deployer.api.State as DeploymentState

/** The current time, updated every [periodMillis], for countdowns. */
@Composable
fun rememberNow(periodMillis: Long = 15_000): State<Instant> = produceState(Instant.now()) {
    while (true) {
        delay(periodMillis)
        value = Instant.now()
    }
}

/** A rounded panel on the page's ground. */
@Composable
fun Panel(
    modifier: Modifier = Modifier,
    onClick: (() -> Unit)? = null,
    border: Color? = null,
    radius: Dp = 16.dp,
    content: @Composable ColumnScope.() -> Unit,
) {
    val colors = DeployerTheme.colors
    val shape = RoundedCornerShape(radius)
    val stroke = border?.let { BorderStroke(1.dp, it) }
    val inner: @Composable () -> Unit = { Column(Modifier.padding(16.dp), content = content) }
    if (onClick != null) {
        Surface(onClick = onClick, modifier = modifier, shape = shape, color = colors.surface, border = stroke, content = inner)
    } else {
        Surface(modifier = modifier, shape = shape, color = colors.surface, border = stroke, content = inner)
    }
}

/** "● running" as a tinted pill. */
@Composable
fun StateBadge(state: DeploymentState, modifier: Modifier = Modifier) {
    val color = DeployerTheme.colors.tone(state.tone)
    Row(
        modifier
            .clip(RoundedCornerShape(14.dp))
            .background(color.copy(alpha = 0.14f))
            .padding(horizontal = 12.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Text(state.symbol, color = color, style = DeployerType.small, modifier = Modifier.clearAndSetSemantics { })
        Text(state.label, color = color, style = DeployerType.small, fontWeight = FontWeight.Medium)
    }
}

/** The state's symbol alone, in its color, for list rows. */
@Composable
fun StateSymbol(state: DeploymentState, modifier: Modifier = Modifier) {
    Text(
        state.symbol,
        color = DeployerTheme.colors.tone(state.tone),
        style = DeployerType.small,
        modifier = modifier.clearAndSetSemantics { contentDescription = state.label },
    )
}

/** A thin bar showing how much of the lifetime is left. */
@Composable
fun LeaseBar(remaining: Float, tone: Tone, modifier: Modifier = Modifier, height: Dp = 4.dp) {
    val colors = DeployerTheme.colors
    Box(
        modifier
            .fillMaxWidth()
            .height(height)
            .clip(RoundedCornerShape(height / 2))
            .background(colors.line),
    ) {
        Box(
            Modifier
                .fillMaxHeight()
                .fillMaxWidth(remaining.coerceIn(0.02f, 1f))
                .background(colors.tone(tone)),
        )
    }
}

/** An error the server or the network reported, with the hint when there is one. */
@Composable
fun ErrorPanel(
    error: ApiException,
    modifier: Modifier = Modifier,
    title: String? = null,
    actionLabel: String = "Retry",
    onAction: (() -> Unit)? = null,
) {
    val colors = DeployerTheme.colors
    Surface(
        modifier = modifier.fillMaxWidth(),
        shape = RoundedCornerShape(16.dp),
        color = colors.bad.copy(alpha = 0.10f),
        border = BorderStroke(1.dp, colors.bad.copy(alpha = 0.35f)),
    ) {
        Row(Modifier.padding(start = 14.dp, top = 12.dp, end = 8.dp, bottom = 12.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Icon(DeployerIcons.Alert, contentDescription = null, tint = colors.bad, modifier = Modifier.size(20.dp))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                if (title != null) Text(title, style = DeployerType.body, fontWeight = FontWeight.SemiBold, color = colors.text)
                Text(error.message, style = DeployerType.small, color = colors.text)
                error.hint?.let { Text(it, style = DeployerType.mono, color = colors.muted) }
            }
            if (onAction != null) {
                TextButton(onClick = onAction, modifier = Modifier.align(Alignment.CenterVertically)) { Text(actionLabel) }
            }
        }
    }
}

/** A labelled value in a details list. */
@Composable
fun DetailRow(label: String, value: String, mono: Boolean = false, showDivider: Boolean = true) {
    val colors = DeployerTheme.colors
    Column {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(vertical = 13.dp),
            horizontalArrangement = Arrangement.spacedBy(16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(label, style = DeployerType.small, color = colors.muted)
            Text(
                value,
                style = if (mono) DeployerType.mono else DeployerType.small,
                color = colors.text,
                modifier = Modifier.weight(1f),
                textAlign = androidx.compose.ui.text.style.TextAlign.End,
            )
        }
        if (showDivider) Box(Modifier.fillMaxWidth().height(1.dp).background(colors.line.copy(alpha = 0.6f)))
    }
}
