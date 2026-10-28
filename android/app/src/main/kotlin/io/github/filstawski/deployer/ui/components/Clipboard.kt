package io.github.filstawski.deployer.ui.components

import android.content.ClipData
import android.content.Context
import android.content.Intent
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.unit.dp
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType
import kotlinx.coroutines.launch

/** Returns a function that puts text on the clipboard. */
@Composable
fun rememberCopy(): (label: String, text: String) -> Unit {
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    return { label, text -> scope.launch { clipboard.setClipEntry(ClipEntry(ClipData.newPlainText(label, text))) } }
}

/** Opens the system share sheet for a piece of text. */
fun Context.shareText(text: String, title: String? = null) {
    val send = Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, text)
        if (title != null) putExtra(Intent.EXTRA_TITLE, title)
    }
    startActivity(Intent.createChooser(send, title))
}

/** A shell command with a copy button, like a code block in the docs. */
@Composable
fun CommandBox(command: String, modifier: Modifier = Modifier) {
    val colors = DeployerTheme.colors
    val copy = rememberCopy()
    Surface(
        modifier = modifier
            .fillMaxWidth()
            .border(1.dp, colors.line, RoundedCornerShape(14.dp)),
        shape = RoundedCornerShape(14.dp),
        color = colors.code,
    ) {
        Row(
            Modifier.padding(start = 16.dp, top = 4.dp, bottom = 4.dp, end = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text("$", style = DeployerType.mono, color = Color(0xFF80868F))
            Text(command, style = DeployerType.mono, color = Color(0xFFECEEF1), modifier = Modifier.weight(1f))
            IconButton(onClick = { copy("Command", command) }) {
                Icon(DeployerIcons.Copy, contentDescription = "Copy command", tint = Color(0xFFECEEF1), modifier = Modifier.size(18.dp))
            }
        }
    }
}
