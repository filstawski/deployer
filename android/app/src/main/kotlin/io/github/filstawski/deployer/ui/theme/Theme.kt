package io.github.filstawski.deployer.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import io.github.filstawski.deployer.model.Tone

/** The app's colors: a quiet ground, one cyan accent, and the CLI's state colors. */
@Immutable
data class Palette(
    val background: Color,
    val surface: Color,
    val surfaceRaised: Color,
    val sheet: Color,
    val code: Color,
    val line: Color,
    val text: Color,
    val muted: Color,
    val accent: Color,
    val onAccent: Color,
    val accentSoft: Color,
    val ok: Color,
    val busy: Color,
    val bad: Color,
    val idle: Color,
) {
    fun tone(tone: Tone): Color = when (tone) {
        Tone.Ok -> ok
        Tone.Busy -> busy
        Tone.Bad -> bad
        Tone.Muted -> idle
    }
}

val DarkPalette = Palette(
    background = Color(0xFF0E1014),
    surface = Color(0xFF171A20),
    surfaceRaised = Color(0xFF1F232B),
    sheet = Color(0xFF1A1D24),
    code = Color(0xFF08090C),
    line = Color(0xFF2A2F38),
    text = Color(0xFFECEEF1),
    muted = Color(0xFFA0A6B0),
    accent = Color(0xFF5CCFE6),
    onAccent = Color(0xFF062A31),
    accentSoft = Color(0x295CCFE6),
    ok = Color(0xFF5BD48A),
    busy = Color(0xFFF5B544),
    bad = Color(0xFFFF8F8F),
    idle = Color(0xFF8B919C),
)

val LightPalette = Palette(
    background = Color(0xFFF6F6F7),
    surface = Color(0xFFFFFFFF),
    surfaceRaised = Color(0xFFEEF0F3),
    sheet = Color(0xFFFFFFFF),
    code = Color(0xFF14161A),
    line = Color(0xFFE1E3E8),
    text = Color(0xFF18181B),
    muted = Color(0xFF5F6672),
    accent = Color(0xFF0E7490),
    onAccent = Color(0xFFFFFFFF),
    accentSoft = Color(0x1F0E7490),
    ok = Color(0xFF15803D),
    busy = Color(0xFFB45309),
    bad = Color(0xFFB91C1C),
    idle = Color(0xFF6B7280),
)

/** Text styles beyond Material's: deployment names and logs are monospaced. */
object DeployerType {
    val title = TextStyle(fontSize = 30.sp, lineHeight = 38.sp, fontWeight = FontWeight.SemiBold)
    val name = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 15.sp, fontWeight = FontWeight.Medium)
    val mono = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 13.sp)
    val log = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 12.sp, lineHeight = 18.sp)
    val body = TextStyle(fontSize = 15.sp, lineHeight = 22.sp)
    val small = TextStyle(fontSize = 13.sp, lineHeight = 18.sp)
}

private val LocalPalette = staticCompositionLocalOf { DarkPalette }

object DeployerTheme {
    val colors: Palette
        @Composable @ReadOnlyComposable get() = LocalPalette.current
}

@Composable
fun DeployerTheme(dark: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    val p = if (dark) DarkPalette else LightPalette
    val base = if (dark) darkColorScheme() else lightColorScheme()
    val scheme = base.copy(
        primary = p.accent,
        onPrimary = p.onAccent,
        primaryContainer = p.accentSoft,
        onPrimaryContainer = p.accent,
        secondaryContainer = p.accentSoft,
        onSecondaryContainer = p.accent,
        background = p.background,
        onBackground = p.text,
        surface = p.background,
        onSurface = p.text,
        surfaceVariant = p.surface,
        onSurfaceVariant = p.muted,
        surfaceContainerLowest = p.background,
        surfaceContainerLow = p.surface,
        surfaceContainer = p.surface,
        surfaceContainerHigh = p.sheet,
        surfaceContainerHighest = p.surfaceRaised,
        outline = p.line,
        outlineVariant = p.line,
        error = p.bad,
        inverseSurface = p.text,
        inverseOnSurface = p.background,
        inversePrimary = p.accent,
    )
    CompositionLocalProvider(LocalPalette provides p) {
        MaterialTheme(colorScheme = scheme, content = content)
    }
}
