package io.github.filstawski.deployer.ui.scan

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import io.github.filstawski.deployer.model.Pairing
import io.github.filstawski.deployer.model.parsePairingLink
import io.github.filstawski.deployer.ui.components.CommandBox
import io.github.filstawski.deployer.ui.theme.DeployerIcons
import io.github.filstawski.deployer.ui.theme.DeployerTheme
import io.github.filstawski.deployer.ui.theme.DeployerType
import java.util.concurrent.CancellationException
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/** Scans the QR code that `deployer pair` shows, and hands over its server and token. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ScanScreen(allowPlainHttp: Boolean, onScanned: (Pairing) -> Unit, onBack: () -> Unit) {
    val context = LocalContext.current
    val colors = DeployerTheme.colors
    var granted by remember {
        mutableStateOf(ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED)
    }
    var asked by remember { mutableStateOf(false) }
    val request = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        granted = it
        asked = true
    }
    LaunchedEffect(Unit) { if (!granted) request.launch(Manifest.permission.CAMERA) }
    // A code that isn't a pairing link, such as a URL, gets a hint instead.
    var other by remember { mutableStateOf(false) }
    // No usable back camera, or the camera failed to start.
    var unavailable by remember { mutableStateOf(false) }

    Scaffold(
        containerColor = colors.background,
        topBar = {
            TopAppBar(
                navigationIcon = { IconButton(onClick = onBack) { Icon(DeployerIcons.Back, contentDescription = "Back") } },
                title = { Text("Scan QR code", fontWeight = FontWeight.SemiBold) },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = colors.background),
            )
        },
    ) { padding ->
        Column(
            Modifier
                .fillMaxSize()
                .padding(padding)
                .navigationBarsPadding()
                .padding(20.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text("On a computer where the CLI is logged in as an administrator, run:", style = DeployerType.body, color = colors.muted)
            CommandBox("deployer pair")
            Surface(
                shape = RoundedCornerShape(24.dp),
                color = Color.Black,
                border = BorderStroke(1.dp, colors.line),
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
            ) {
                if (unavailable) {
                    Box(Modifier.padding(24.dp), contentAlignment = Alignment.Center) {
                        Text(
                            "The camera isn't available. Go back and enter the address and token deployer pair prints.",
                            style = DeployerType.body,
                            color = Color(0xFFECEEF1),
                            textAlign = TextAlign.Center,
                        )
                    }
                } else if (granted) {
                    Box(contentAlignment = Alignment.Center) {
                        Camera(
                            onCode = { text ->
                                val pairing = parsePairingLink(text, allowPlainHttp)
                                if (pairing != null) onScanned(pairing) else other = true
                                pairing != null
                            },
                            onUnavailable = { unavailable = true },
                        )
                        Box(
                            Modifier
                                .size(240.dp)
                                .border(3.dp, colors.accent, RoundedCornerShape(20.dp)),
                        )
                    }
                } else {
                    Column(
                        Modifier.padding(24.dp),
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
                    ) {
                        Text(
                            "Scanning needs the camera. The app uses it only on this screen.",
                            style = DeployerType.body,
                            color = Color(0xFFECEEF1),
                            textAlign = TextAlign.Center,
                        )
                        if (asked) {
                            // Android stops asking after a second refusal; the setting is in the app's info.
                            OutlinedButton(onClick = {
                                context.startActivity(
                                    Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null)),
                                )
                            }) { Text("Open settings", color = Color(0xFFECEEF1)) }
                        }
                        Button(onClick = { request.launch(Manifest.permission.CAMERA) }) { Text("Allow the camera") }
                    }
                }
            }
            Text(
                if (other) "That QR code isn't from deployer pair. Scan the one it shows." else "Point the camera at the code.",
                style = DeployerType.small,
                color = if (other) colors.busy else colors.muted,
                modifier = Modifier.align(Alignment.CenterHorizontally),
            )
        }
    }
}

/**
 * The back camera's preview, with each frame searched for a QR code. [onCode]
 * returns true to stop scanning; [onUnavailable] is called when there is no
 * back camera or it won't start.
 */
@Composable
private fun Camera(onCode: (String) -> Boolean, onUnavailable: () -> Unit) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current
    val currentOnCode by rememberUpdatedState(onCode)
    val currentOnUnavailable by rememberUpdatedState(onUnavailable)
    val preview = remember { PreviewView(context) }

    DisposableEffect(lifecycle) {
        val executor = Executors.newSingleThreadExecutor()
        val decoder = QrDecoder()
        val done = AtomicBoolean(false)
        val main = ContextCompat.getMainExecutor(context)
        val future = ProcessCameraProvider.getInstance(context)
        future.addListener({
            try {
                val provider = future.get()
                val show = Preview.Builder().build().also { it.setSurfaceProvider(preview.surfaceProvider) }
                val analysis = ImageAnalysis.Builder().setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST).build()
                analysis.setAnalyzer(executor) { image ->
                    val text = image.use { if (done.get()) null else decoder.decode(it.luminance(), it.width, it.height) }
                    if (text != null) main.execute { if (!done.get() && currentOnCode(text)) done.set(true) }
                }
                provider.unbindAll()
                provider.bindToLifecycle(lifecycle, CameraSelector.DEFAULT_BACK_CAMERA, show, analysis)
            } catch (e: Exception) {
                // No back camera, a camera disabled by policy, or CameraX failing to start.
                if (e is CancellationException) return@addListener
                currentOnUnavailable()
            }
        }, main)
        onDispose {
            done.set(true)
            future.addListener({ runCatching { future.get().unbindAll() } }, main)
            executor.shutdown()
        }
    }
    AndroidView(factory = { preview }, modifier = Modifier.fillMaxSize())
}

/** The frame's luminance plane, without the padding at the end of each row. */
private fun ImageProxy.luminance(): ByteArray {
    val plane = planes[0]
    val buffer = plane.buffer
    val data = ByteArray(width * height)
    if (plane.rowStride == width) {
        buffer.get(data, 0, data.size)
    } else {
        for (row in 0 until height) {
            buffer.position(row * plane.rowStride)
            buffer.get(data, row * width, width)
        }
    }
    return data
}
