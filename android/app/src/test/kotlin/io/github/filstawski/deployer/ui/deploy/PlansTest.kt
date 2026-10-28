package io.github.filstawski.deployer.ui.deploy

import io.github.filstawski.deployer.api.Project
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.model.deployment
import java.time.Instant
import org.junit.Assert.assertEquals
import org.junit.Test

class PlansTest {
    private val now = Instant.parse("2026-10-28T15:00:00Z")
    private val project = Project("prj_1", "shop", "https://github.com/acme/shop.git")
    private val live = deployment(now.plusSeconds(3_600)).copy(id = "dep_a", name = "shop-feature-x", projectId = "prj_1", ref = "release")

    @Test fun newPreviewsGetNamesNoOneElseHas() {
        // Both branches slug to shop-feature-x, which a live preview of another branch already has.
        val state = NewDeploymentState(project = project, live = listOf(live), selected = listOf("feature/x", "feature_x"), now = now)
        assertEquals(listOf("shop-feature-x-2", "shop-feature-x-3"), state.plans.map { it.name })
    }

    @Test fun branchesWithAPreviewUpdateItUnlessToldOtherwise() {
        val state = NewDeploymentState(project = project, live = listOf(live), selected = listOf("release", "main"), now = now)
        assertEquals(listOf("shop-feature-x" to "dep_a", "shop-main" to null), state.plans.map { it.name to it.update?.id })
        val fresh = state.copy(createNewFor = setOf("release"))
        assertEquals(listOf("shop-release" to null, "shop-main" to null), fresh.plans.map { it.name to it.update?.id })
    }

    @Test fun anExpiredPreviewIsNotUpdated() {
        val expired = live.copy(state = State.Expired, expiresAt = now.minusSeconds(60))
        val state = NewDeploymentState(project = project, live = listOf(expired), selected = listOf("release", "main"), now = now)
        assertEquals(listOf(null, null), state.plans.map { it.update })
    }

    @Test fun warnsWhenNewPreviewsWontFit() {
        val status = io.github.filstawski.deployer.api.DeployerApi.json.decodeFromString(
            io.github.filstawski.deployer.api.ServerStatus.serializer(),
            """{"version":"v1","api_version":1,"domain":"d","preview_url":"u","deployments":{"live":9,"max":10,"default_ttl_seconds":86400,"max_ttl_seconds":604800},
               "token":{"id":"t","name":"n","scopes":["admin"],"projects":["*"]}}""",
        )
        val state = NewDeploymentState(status = status, project = project, live = listOf(live), selected = listOf("a", "b", "release"), now = now)
        // a and b are new; release updates its preview, which takes no new slot.
        assertEquals(1, state.overLimit)
    }
}
