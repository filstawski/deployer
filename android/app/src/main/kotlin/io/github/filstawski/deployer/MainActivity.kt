package io.github.filstawski.deployer

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import io.github.filstawski.deployer.model.parsePairingLink
import io.github.filstawski.deployer.ui.DeployerApp
import io.github.filstawski.deployer.ui.theme.DeployerTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val container = (application as DeployerApplication).container
        if (savedInstanceState == null) receive(intent)
        setContent {
            DeployerTheme {
                Surface(Modifier.fillMaxSize(), color = DeployerTheme.colors.background) {
                    DeployerApp(container)
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        receive(intent)
    }

    /**
     * Takes a deployer://pair link opened from another app, such as the
     * camera, or a deployment to show from a notification or the widget.
     */
    private fun receive(intent: Intent) {
        val container = (application as DeployerApplication).container
        intent.getStringExtra(EXTRA_DEPLOYMENT)?.let { name ->
            container.opens.value = OpenRequest(intent.getStringExtra(EXTRA_SERVER), name)
            return
        }
        // Recents relaunches the intent that started the task, with a token already used.
        if ((intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY) != 0) return
        val pairing = intent.dataString?.let { parsePairingLink(it, container.allowPlainHttp) } ?: return
        container.pairings.value = PairingRequest(pairing, connect = false)
        // Don't keep the token around in the activity's intent.
        setIntent(Intent(this, MainActivity::class.java))
    }

    companion object {
        const val EXTRA_SERVER = "server"
        const val EXTRA_DEPLOYMENT = "deployment"
    }
}
