package io.github.filstawski.deployer.ui.theme

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.addPathNodes
import androidx.compose.ui.unit.dp

/**
 * Line icons on a 24-unit grid, drawn with round strokes. Icon() tints them,
 * so the stroke color here doesn't matter.
 */
object DeployerIcons {
    val Back = icon("back", "M19 12H5", "M12 19l-7-7 7-7")
    val Close = icon("close", "M6 6l12 12", "M18 6L6 18")
    val Search = icon("search", circle(11f, 11f, 7f), "M21 21l-4.3-4.3")
    val ChevronDown = icon("chevron-down", "M6 9l6 6 6-6")
    val ChevronRight = icon("chevron-right", "M9 6l6 6-6 6")
    val Plus = icon("plus", "M12 5v14", "M5 12h14")
    val Check = icon("check", "M5 12l5 5 9-11", width = 2.4f)
    val More = icon("more", circle(12f, 5f, 0.6f), circle(12f, 12f, 0.6f), circle(12f, 19f, 0.6f), width = 2.6f)
    val Update = icon("update", "M21 12a9 9 0 1 1-2.64-6.36L21 8", "M21 3v5h-5")
    val Stop = icon("stop", "M8 6h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2z")
    val Play = icon("play", "M7 5l12 7-12 7z")
    val Trash = icon("trash", "M3 6h18", "M8 6V4h8v2", "M6 6l1 14h10l1-14")
    val Terminal = icon("terminal", "M4 17l6-5-6-5", "M12 19h8")
    val Clock = icon("clock", circle(12f, 12f, 9f), "M12 7v5l3 2")
    val OpenExternal = icon("open", "M15 3h6v6", "M10 14L21 3", "M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6")
    val Copy = icon("copy", "M11 9h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-8a2 2 0 0 1-2-2v-8a2 2 0 0 1 2-2z", "M5 15V5a2 2 0 0 1 2-2h10")
    val Share = icon("share", "M12 3v12", "M7 8l5-5 5 5", "M5 14v5a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-5")
    val Pause = icon("pause", "M9 5v14", "M15 5v14", width = 2.6f)
    val Latest = icon("latest", "M12 4v12", "M6 10l6 6 6-6", "M5 20h14")
    val Alert = icon("alert", "M12 9v4", "M12 17h.01", "M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z")
    val Layers = icon("layers", "M12 3l9 5-9 5-9-5 9-5z", "M3 13l9 5 9-5")
    val Server = icon(
        "server",
        "M5 4h14a2 2 0 0 1 2 2v3a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2z",
        "M5 13h14a2 2 0 0 1 2 2v3a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-3a2 2 0 0 1 2-2z",
        "M7 7.5h.01", "M7 16.5h.01",
    )
    val Eye = icon("eye", "M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z", circle(12f, 12f, 3f))
    val EyeOff = icon("eye-off", "M3 3l18 18", "M10.6 5.1A10.8 10.8 0 0 1 12 5c6.5 0 10 7 10 7a17.6 17.6 0 0 1-3.2 4.1", "M6.6 6.6C3.8 8.4 2 12 2 12s3.5 7 10 7a9.7 9.7 0 0 0 5.4-1.6", "M9.9 9.9a3 3 0 0 0 4.2 4.2")
    val Scan = icon("scan", "M4 8V5a1 1 0 0 1 1-1h3", "M16 4h3a1 1 0 0 1 1 1v3", "M20 16v3a1 1 0 0 1-1 1h-3", "M8 20H5a1 1 0 0 1-1-1v-3", "M7 12h10")
}

private fun circle(cx: Float, cy: Float, r: Float) =
    "M${cx - r} ${cy}a$r $r 0 1 0 ${2 * r} 0a$r $r 0 1 0 ${-2 * r} 0"

private fun icon(name: String, vararg paths: String, width: Float = 1.8f): ImageVector =
    ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f).apply {
        paths.forEach { d ->
            addPath(
                pathData = addPathNodes(d),
                fill = null,
                stroke = SolidColor(Color.Black),
                strokeLineWidth = width,
                strokeLineCap = StrokeCap.Round,
                strokeLineJoin = StrokeJoin.Round,
            )
        }
    }.build()
