package io.github.filstawski.deployer.widget

import android.content.Context
import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.GlanceAppWidgetReceiver
import androidx.glance.appwidget.action.actionStartActivity
import androidx.glance.appwidget.cornerRadius
import androidx.glance.appwidget.provideContent
import androidx.glance.background
import androidx.glance.color.ColorProvider
import androidx.glance.layout.Alignment
import androidx.glance.layout.Column
import androidx.glance.layout.Row
import androidx.glance.layout.Spacer
import androidx.glance.layout.fillMaxSize
import androidx.glance.layout.fillMaxWidth
import androidx.glance.layout.height
import androidx.glance.layout.padding
import androidx.glance.layout.width
import androidx.glance.text.FontFamily
import androidx.glance.text.FontWeight
import androidx.glance.text.Text
import androidx.glance.text.TextStyle
import io.github.filstawski.deployer.DeployerApplication
import io.github.filstawski.deployer.MainActivity
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.model.TimeFormat
import io.github.filstawski.deployer.model.Tone
import io.github.filstawski.deployer.model.symbol
import io.github.filstawski.deployer.model.tone
import io.github.filstawski.deployer.ui.theme.DarkPalette
import io.github.filstawski.deployer.ui.theme.LightPalette
import java.time.Instant

/** The active server's deployments on the home screen, as of the last refresh. */
class DeploymentsWidget : GlanceAppWidget() {
    override suspend fun provideGlance(context: Context, id: GlanceId) {
        val store = (context.applicationContext as DeployerApplication).container.widget
        provideContent {
            // Read in the composition: an update during a running session only recomposes it.
            val data by store.data.collectAsState()
            Content(context, data)
        }
    }
}

class DeploymentsWidgetReceiver : GlanceAppWidgetReceiver() {
    override val glanceAppWidget = DeploymentsWidget()
}

private val background = ColorProvider(day = LightPalette.surface, night = DarkPalette.sheet)
private val text = ColorProvider(day = LightPalette.text, night = DarkPalette.text)
private val muted = ColorProvider(day = LightPalette.muted, night = DarkPalette.muted)

private fun tone(tone: Tone) = ColorProvider(day = LightPalette.tone(tone), night = DarkPalette.tone(tone))

@Composable
private fun Content(context: Context, data: WidgetData?) {
    Column(
        GlanceModifier
            .fillMaxSize()
            .background(background)
            .cornerRadius(24.dp)
            .padding(16.dp)
            .clickable(actionStartActivity(Intent(context, MainActivity::class.java))),
    ) {
        if (data == null) {
            Text("Deployer", style = TextStyle(color = text, fontSize = 15.sp, fontWeight = FontWeight.Bold))
            Spacer(GlanceModifier.height(6.dp))
            Text("Open the app to connect to a server.", style = TextStyle(color = muted, fontSize = 13.sp))
            return@Column
        }
        Row(GlanceModifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text(data.server, style = TextStyle(color = text, fontSize = 15.sp, fontWeight = FontWeight.Bold), modifier = GlanceModifier.defaultWeight())
            Text(
                if (data.max > 0) "${data.live} / ${data.max} live" else "${data.live} live",
                style = TextStyle(color = muted, fontSize = 13.sp, fontFamily = FontFamily.Monospace),
            )
        }
        Spacer(GlanceModifier.height(8.dp))
        if (data.rows.isEmpty()) {
            Text("No deployments", style = TextStyle(color = muted, fontSize = 13.sp))
        }
        val now = Instant.now()
        val times = TimeFormat()
        data.rows.forEach { row ->
            Row(
                GlanceModifier
                    .fillMaxWidth()
                    .padding(vertical = 5.dp)
                    .clickable(actionStartActivity(openIntent(context, row.name))),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(row.state.symbol, style = TextStyle(color = tone(row.state.tone), fontSize = 13.sp))
                Spacer(GlanceModifier.width(10.dp))
                Text(
                    row.name,
                    style = TextStyle(color = text, fontSize = 14.sp, fontFamily = FontFamily.Monospace),
                    maxLines = 1,
                    modifier = GlanceModifier.defaultWeight(),
                )
                val until = row.expiresAt?.let { Instant.parse(it) }?.takeIf { row.state == State.Running && it.isAfter(now) }
                Text(
                    when {
                        until != null -> "until " + times.short(until, now)
                        else -> row.state.wire
                    },
                    style = TextStyle(color = if (until == null) tone(row.state.tone) else muted, fontSize = 13.sp),
                )
            }
        }
    }
}

private fun openIntent(context: Context, name: String) =
    Intent(context, MainActivity::class.java)
        .putExtra(MainActivity.EXTRA_DEPLOYMENT, name)
        .putExtra(MainActivity.EXTRA_SERVER, (context.applicationContext as DeployerApplication).container.connections.activeId.value)
