package io.github.filstawski.deployer.ui.connect

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.github.filstawski.deployer.data.Connection
import io.github.filstawski.deployer.ui.components.CommandBox
import io.github.filstawski.deployer.ui.components.ErrorPanel
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType

/**
 * Adds a server by its API address and a token. [onClose] is set when other
 * servers exist, so adding one can be abandoned.
 */
@Composable
fun ConnectScreen(
    viewModel: ConnectViewModel,
    /** Null on a device without a camera. */
    onScan: (() -> Unit)?,
    onConnected: (Connection) -> Unit,
    onClose: (() -> Unit)?,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val colors = DeployerTheme.colors
    var showToken by rememberSaveable { mutableStateOf(false) }

    LaunchedEffect(state.connected) { state.connected?.let(onConnected) }

    Box(
        Modifier
            .fillMaxSize()
            .safeDrawingPadding()
            .imePadding(),
    ) {
        Column(
            Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            if (onClose != null) {
                IconButton(onClick = onClose, modifier = Modifier.padding(start = 0.dp)) {
                    Icon(DeployerIcons.Close, contentDescription = "Close", tint = colors.text)
                }
            } else {
                Spacer(Modifier.height(24.dp))
            }
            Surface(shape = RoundedCornerShape(16.dp), color = colors.accent, modifier = Modifier.size(56.dp)) {
                Box(contentAlignment = Alignment.Center) {
                    Icon(DeployerIcons.Layers, contentDescription = null, tint = colors.onAccent, modifier = Modifier.size(30.dp))
                }
            }
            Text(
                if (onClose != null) "Add a server" else "Connect to your server",
                fontSize = 28.sp,
                lineHeight = 35.sp,
                fontWeight = FontWeight.SemiBold,
                color = colors.text,
            )
            Text(
                if (onScan != null) {
                    "On a computer where the CLI is logged in as an administrator, run this and scan the QR code it shows:"
                } else {
                    "On a computer where the CLI is logged in as an administrator, run this:"
                },
                style = DeployerType.body,
                color = colors.muted,
            )
            CommandBox("deployer pair")
            Text(
                buildAnnotatedString {
                    append("Add ")
                    withStyle(SpanStyle(fontFamily = FontFamily.Monospace, color = colors.text)) { append("--role viewer") }
                    append(" for a phone that should only look, not change anything.")
                },
                style = DeployerType.small,
                color = colors.muted,
            )
            if (onScan != null) {
                Button(
                    onClick = onScan,
                    enabled = !state.busy,
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(52.dp),
                ) {
                    Icon(DeployerIcons.Scan, contentDescription = null, modifier = Modifier.size(20.dp))
                    Text("Scan QR code", fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(start = 8.dp))
                }
            }
            Text(
                if (onScan != null) "Or enter the address and token it prints:" else "Then enter the address and token it prints:",
                style = DeployerType.body,
                color = colors.muted,
                modifier = Modifier.padding(top = 8.dp),
            )
            OutlinedTextField(
                value = state.address,
                onValueChange = viewModel::setAddress,
                label = { Text("Server address") },
                placeholder = { Text("https://api.example.com") },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Next, autoCorrectEnabled = false),
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = state.token,
                onValueChange = viewModel::setToken,
                label = { Text("Token") },
                placeholder = { Text("dpl_…") },
                singleLine = true,
                visualTransformation = if (showToken) VisualTransformation.None else PasswordVisualTransformation(),
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done, autoCorrectEnabled = false),
                keyboardActions = KeyboardActions(onDone = { viewModel.connect() }),
                trailingIcon = {
                    IconButton(onClick = { showToken = !showToken }) {
                        Icon(
                            if (showToken) DeployerIcons.EyeOff else DeployerIcons.Eye,
                            contentDescription = if (showToken) "Hide token" else "Show token",
                            modifier = Modifier.size(20.dp),
                        )
                    }
                },
                modifier = Modifier.fillMaxWidth(),
            )
            state.error?.let { ErrorPanel(it) }
            OutlinedButton(
                onClick = viewModel::connect,
                enabled = !state.busy,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(52.dp),
            ) {
                if (state.busy) {
                    CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = colors.accent)
                } else {
                    Text("Connect", fontWeight = FontWeight.SemiBold, color = colors.text)
                }
            }
            Text(
                "The token is stored encrypted on this phone. An administrator can revoke it anytime.",
                style = DeployerType.small,
                color = colors.muted,
            )
        }
    }
}
