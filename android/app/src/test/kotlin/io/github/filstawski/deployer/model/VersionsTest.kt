package io.github.filstawski.deployer.model

import org.junit.Assert.assertEquals
import org.junit.Test

class VersionsTest {
    @Test fun comparesReleases() {
        val cases = listOf(
            Triple("0.4.0", "v0.5.0", true),
            Triple("0.4.0", "v0.4.1", true),
            Triple("0.4.0", "v1.0.0", true),
            Triple("0.4.0", "v0.4.0", false),
            Triple("0.5.0", "v0.4.9", false),
            Triple("0.10.0", "v0.9.0", false),
            Triple("0.4.0-rc.1", "v0.4.0", false),
            // Development builds and test servers aren't releases.
            Triple("0.0.0-dev", "v0.5.0", false),
            Triple("0.4.0", "v0.0.0-test", false),
            Triple("0.4.0", "dev", false),
        )
        for ((app, server, newer) in cases) assertEquals("$app vs $server", newer, serverIsNewer(app, server))
    }
}
