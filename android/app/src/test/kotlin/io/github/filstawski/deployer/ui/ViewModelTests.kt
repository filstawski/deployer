package io.github.filstawski.deployer.ui

import io.github.filstawski.deployer.api.TestServer
import io.github.filstawski.deployer.api.fixture
import io.github.filstawski.deployer.api.json
import io.github.filstawski.deployer.model.BulkAction
import io.github.filstawski.deployer.ui.deploy.NewDeploymentViewModel
import io.github.filstawski.deployer.ui.deployments.DeploymentsViewModel
import io.github.filstawski.deployer.ui.deployment.DeploymentViewModel
import io.github.filstawski.deployer.ui.logs.LogsViewModel
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlin.time.Duration.Companion.days
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.cancel
import kotlinx.coroutines.withTimeout
import androidx.lifecycle.viewModelScope
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/** View models against a real HTTP server; network callbacks drive them, so Main runs unconfined. */
@OptIn(ExperimentalCoroutinesApi::class)
class ViewModelTests {
    private val t = TestServer()

    @Before fun setUp() = Dispatchers.setMain(Dispatchers.Unconfined)

    @After fun tearDown() {
        Dispatchers.resetMain()
        t.close()
    }

    private fun route(handler: (RecordedRequest) -> MockResponse) {
        t.server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when (request.url.encodedPath) {
                "/v1/server" -> json(fixture("server.json"))
                else -> handler(request)
            }
        }
    }

    private fun operation(kind: String, status: String, extra: String = "", id: String = "op_1") = json(
        """{"data":{"id":"$id","kind":"$kind","status":"$status","created_at":"2026-10-28T14:00:00Z"$extra}}""",
    )

    private fun deployment(state: String = "running", operation: String = "op_8cf3q2m9xkwz") = json(
        fixture("deployment.json").replace("\"running\"", "\"$state\"").replace("op_8cf3q2m9xkwz", operation),
    )

    /** The finished operation the deployment fixture points at. */
    private val settled = operation("deployment.create", "succeeded", id = "op_8cf3q2m9xkwz")

    /** The fixtures' present: their deployments are live, and expire the next day. */
    private val fixtureTime = java.time.Instant.parse("2026-10-28T15:00:00Z")

    private suspend fun <T> StateFlow<T>.await(predicate: (T) -> Boolean): T = withTimeout(5_000) { first(predicate) }

    /** Runs [block] while the screen is visible, which is when operations are followed. */
    private fun visible(vm: DeploymentViewModel, block: suspend CoroutineScope.() -> Unit) = runBlocking {
        val polling = launch(Dispatchers.Default) { vm.poll() }
        try {
            block()
        } finally {
            polling.cancel()
        }
    }

    @Test fun anActionFollowsItsOperationUntilItFinishes() {
        val polls = AtomicInteger()
        route { r ->
            when {
                r.method == "POST" && r.url.encodedPath.endsWith("/stop") -> operation("deployment.stop", "queued")
                r.url.encodedPath == "/v1/operations/op_1" ->
                    operation("deployment.stop", if (polls.incrementAndGet() < 3) "running" else "succeeded")
                r.url.encodedPath.startsWith("/v1/operations/") -> settled
                else -> deployment()
            }
        }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        visible(vm) {
            vm.stop()
            assertEquals("pending from the tap", "Stopping", vm.state.value.pending)
            val done = vm.state.await { it.message != null }
            assertEquals("Stopped feat-checkout", done.message)
            assertNull(done.pending)
            assertEquals(3, polls.get())
        }
    }

    @Test fun operationsAreFollowedOnlyWhileTheScreenIsVisible() = runBlocking {
        val polls = AtomicInteger()
        route { r ->
            when {
                r.method == "POST" -> operation("deployment.stop", "queued")
                r.url.encodedPath == "/v1/operations/op_1" -> operation("deployment.stop", "succeeded").also { polls.incrementAndGet() }
                else -> deployment()
            }
        }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        vm.stop()
        delay(200)
        assertEquals("no polling in the background", 0, polls.get())
        val polling = launch(Dispatchers.Default) { vm.poll() }
        assertEquals("Stopped feat-checkout", vm.state.await { it.message != null }.message)
        polling.cancel()
    }

    @Test fun anUpdateShowsItsBuildWithoutWaitingForTheNextRefresh() {
        val submitted = AtomicBoolean(false)
        route { r ->
            when {
                r.method == "POST" -> operation("deployment.update", "queued", id = "op_u").also { submitted.set(true) }
                r.url.encodedPath == "/v1/operations/op_u" -> operation("deployment.update", "running", id = "op_u")
                r.url.encodedPath.startsWith("/v1/operations/") -> settled
                r.url.encodedPath.endsWith("/logs") -> json("""{"data":{"logs":"#1 load\n","offset":8}}""")
                // Once the update is queued, it is the deployment's latest operation.
                else -> deployment(operation = if (submitted.get()) "op_u" else "op_8cf3q2m9xkwz")
            }
        }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        visible(vm) {
            vm.state.await { it.deployment != null }
            // The next regular refresh is 10 seconds away; the build shows long before.
            vm.update()
            withTimeout(3_000) { vm.state.first { it.updating } }
        }
    }

    @Test fun aSecondTapDoesNotSubmitAgain() {
        val posts = AtomicInteger()
        route { r ->
            when {
                r.method == "POST" -> operation("deployment.start", "queued").also { posts.incrementAndGet() }
                r.url.encodedPath == "/v1/operations/op_1" -> operation("deployment.start", "succeeded")
                r.url.encodedPath.startsWith("/v1/operations/") -> settled
                else -> deployment()
            }
        }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        visible(vm) {
            vm.start()
            vm.start()
            val done = vm.state.await { it.message != null }
            assertEquals(1, posts.get())
            assertNull(done.actionError)
        }
    }

    @Test fun anOlderSlowerRefreshDoesNotUndoAnAction() {
        val stopped = AtomicBoolean(false)
        val gets = AtomicInteger()
        val firstGet = CountDownLatch(1)
        route { r ->
            when {
                r.method == "POST" -> operation("deployment.stop", "queued").also { stopped.set(true) }
                r.url.encodedPath == "/v1/operations/op_1" -> operation("deployment.stop", "succeeded")
                r.url.encodedPath.startsWith("/v1/operations/") -> settled
                r.url.encodedPath == "/v1/deployments/feat-checkout" && gets.incrementAndGet() == 1 -> {
                    firstGet.countDown()
                    // Started before the action, answered after it.
                    deployment("running").newBuilder().headersDelay(800, TimeUnit.MILLISECONDS).build()
                }
                else -> deployment(if (stopped.get()) "stopped" else "running")
            }
        }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        visible(vm) {
            firstGet.await(5, TimeUnit.SECONDS)
            vm.stop()
            vm.state.await { it.message != null }
            delay(1_200)
            assertEquals("stopped", vm.state.value.deployment?.state?.wire)
        }
    }

    @Test fun aFailedOperationExplainsWhy() {
        route { r ->
            when {
                r.method == "POST" -> operation("deployment.start", "queued")
                r.url.encodedPath == "/v1/operations/op_1" -> operation(
                    "deployment.start", "failed",
                    ""","error":{"code":"HEALTHCHECK_FAILED","message":"GET /healthz did not succeed","hint":"check the logs"}""",
                )
                r.url.encodedPath.startsWith("/v1/operations/") -> settled
                else -> deployment()
            }
        }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        visible(vm) {
            vm.start()
            val failed = vm.state.await { it.actionError != null }
            assertEquals("HEALTHCHECK_FAILED", failed.actionError?.code)
            assertEquals("check the logs", failed.actionError?.hint)
        }
    }

    @Test fun aRejectedActionShowsTheServersError() {
        route { r -> if (r.method == "POST") json(fixture("error.json"), code = 409) else deployment() }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        visible(vm) {
            vm.stop()
            val rejected = vm.state.await { it.actionError != null }
            assertEquals("INVALID_STATE", rejected.actionError?.code)
            assertNull(rejected.pending)
        }
    }

    @Test fun updatesSayWhetherAnythingChanged() {
        route { r ->
            when {
                r.method == "POST" -> operation("deployment.update", "queued")
                r.url.encodedPath == "/v1/operations/op_1" -> operation(
                    "deployment.update", "succeeded",
                    ""","result":{"previous_commit":"9f3c2e1","commit":"9f3c2e1","changed":false}""",
                )
                r.url.encodedPath.startsWith("/v1/operations/") -> settled
                else -> deployment()
            }
        }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        visible(vm) {
            vm.update()
            assertEquals("feat-checkout is already at the latest commit", vm.state.await { it.message != null }.message)
        }
    }

    @Test fun anUpdateFromAnotherDeviceShowsItsBuild() {
        // The deployment keeps running while the update builds; its latest operation says so.
        route { r ->
            when {
                r.url.encodedPath == "/v1/operations/op_u" -> operation("deployment.update", "running", id = "op_u")
                r.url.encodedPath.endsWith("/logs") -> json("""{"data":{"logs":"#1 load\n#2 npm ci\n","offset":17}}""")
                else -> deployment(operation = "op_u")
            }
        }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        visible(vm) {
            val s = vm.state.await { it.deployment != null }
            assertTrue(s.updating)
            assertTrue(s.building)
            assertEquals(listOf("#1 load", "#2 npm ci"), s.buildLog)
        }
    }

    @Test fun destroyingLeavesTheScreen() {
        val destroyed = AtomicBoolean(false)
        route { r ->
            when {
                r.method == "DELETE" -> operation("deployment.destroy", "queued").also { destroyed.set(true) }
                r.url.encodedPath == "/v1/operations/op_1" -> operation("deployment.destroy", "succeeded")
                r.url.encodedPath.startsWith("/v1/operations/") -> settled
                // The server still finds a destroyed deployment by name.
                else -> deployment(if (destroyed.get()) "destroyed" else "running")
            }
        }
        val vm = DeploymentViewModel(t.api, "feat-checkout", pollMillis = 10)
        visible(vm) {
            vm.destroy()
            val gone = vm.state.await { it.destroyed }
            assertNull("the list shows the confirmation, not this screen", gone.message)
        }
    }

    @Test fun deployingFromThePhoneStartsADeploymentWithTheSuggestedName() = runBlocking {
        val body = java.util.concurrent.atomic.AtomicReference<String>()
        route { r ->
            when {
                r.url.encodedPath == "/v1/projects" -> json(fixture("projects.json"))
                r.url.encodedPath.endsWith("/branches") -> json(fixture("branches.json"))
                r.method == "POST" && r.url.encodedPath == "/v1/deployments" -> {
                    body.set(r.body?.utf8())
                    json(fixture("create_deployment.json"), code = 202)
                }
                else -> json(fixture("deployments.json"))
            }
        }
        val vm = NewDeploymentViewModel(t.api, clock = { fixtureTime })
        // The first project and its default branch are chosen, with the CLI's name.
        val ready = vm.state.await { it.branch != null }
        assertEquals("main", ready.branch)
        assertEquals("storefront-main", ready.name)
        assertEquals(86_400L, ready.lifetime?.duration?.inWholeSeconds)

        // A branch the list doesn't show can be typed.
        vm.setQuery("feature/x")
        assertEquals(listOf("feature/x"), vm.state.value.matches)
        vm.selectBranch("feature/x")
        assertEquals("storefront-feature-x", vm.state.value.name)
        vm.submit()
        assertEquals("fix-cart-total", vm.state.await { it.opened != null }.opened)
        assertEquals(
            """{"project":"prj_9s6vn7ax49yx","ref":"feature/x","name":"storefront-feature-x","ttl_seconds":86400}""",
            body.get(),
        )
    }

    @Test fun deployingALiveBranchUpdatesItInstead() = runBlocking {
        val updated = AtomicBoolean(false)
        route { r ->
            when {
                r.url.encodedPath == "/v1/projects" -> json(fixture("projects.json"))
                r.url.encodedPath.endsWith("/branches") -> json(fixture("branches.json"))
                r.method == "POST" && r.url.encodedPath == "/v1/deployments/dep_k7m2q9x4abcd/update" ->
                    operation("deployment.update", "queued").also { updated.set(true) }
                else -> json(fixture("deployments.json"))
            }
        }
        val vm = NewDeploymentViewModel(t.api, clock = { fixtureTime })
        vm.state.await { it.branch != null }
        vm.selectBranch("feat/checkout")
        assertEquals("feat-checkout", vm.state.value.existing?.name)
        vm.submit()
        assertEquals("feat-checkout", vm.state.await { it.opened != null }.opened)
        assertTrue(updated.get())
    }

    @Test fun anExpiredPreviewIsNotOfferedForUpdating() = runBlocking {
        // feat-checkout's lease has ended; the server can't update it until it is extended.
        val expired = fixture("deployments.json").replace("\"expires_at\": \"2026-10-29T13:02:00Z\"", "\"expires_at\": \"2020-01-01T00:00:00Z\"")
        route { r ->
            when {
                r.url.encodedPath == "/v1/projects" -> json(fixture("projects.json"))
                r.url.encodedPath.endsWith("/branches") -> json(fixture("branches.json"))
                else -> json(expired)
            }
        }
        val vm = NewDeploymentViewModel(t.api, clock = { fixtureTime })
        vm.state.await { it.branch != null }
        vm.selectBranch("feat/checkout")
        assertNull(vm.state.value.existing)
        assertEquals("storefront-feat-checkout", vm.state.value.name)
    }

    @Test fun aLiveBranchCanStillGetANewPreview() = runBlocking {
        route { r ->
            when {
                r.url.encodedPath == "/v1/projects" -> json(fixture("projects.json"))
                r.url.encodedPath.endsWith("/branches") -> json(fixture("branches.json"))
                else -> json(fixture("deployments.json"))
            }
        }
        val vm = NewDeploymentViewModel(t.api, clock = { fixtureTime })
        vm.state.await { it.branch != null }
        vm.selectBranch("feat/checkout")
        assertEquals("feat-checkout", vm.state.value.existing?.name)
        vm.setCreateNew(true)
        assertNull(vm.state.value.existing)
        assertEquals("feat-checkout", vm.state.value.match?.name)
        assertTrue(vm.state.value.canSubmit)
        // Choosing a branch again starts from the update offer.
        vm.selectBranch("feat/checkout")
        assertEquals("feat-checkout", vm.state.value.existing?.name)
    }

    @Test fun aNameEditedByHandIsKept() = runBlocking {
        route { r ->
            when {
                r.url.encodedPath == "/v1/projects" -> json(fixture("projects.json"))
                r.url.encodedPath.endsWith("/branches") -> json(fixture("branches.json"))
                else -> json(fixture("deployments.json"))
            }
        }
        val vm = NewDeploymentViewModel(t.api, clock = { fixtureTime })
        vm.state.await { it.branch != null }
        vm.setName("Demo-For-Anna")
        assertEquals("demo-for-anna", vm.state.value.name)
        vm.selectBranch("feature/y")
        assertEquals("demo-for-anna", vm.state.value.name)
        vm.setName("api")
        assertEquals("api is reserved.", vm.state.value.nameError)
        assertFalse(vm.state.value.canSubmit)
    }

    /** The deployments fixture with fix-cart-total running too, ending [fixExpires]. */
    private fun twoRunning(fixExpires: String = "2026-10-29T14:25:00Z") = fixture("deployments.json").replace(
        "\"state\": \"building\",",
        "\"state\": \"running\", \"image\": \"sha256:b\", \"ready_at\": \"2026-10-28T14:25:00Z\", \"expires_at\": \"$fixExpires\",",
    )

    private fun DeploymentsViewModel.selectAllLoaded() = runBlocking {
        state.await { it.deployments.isNotEmpty() }
        selectAll()
    }

    @Test fun aBulkActionActsOnTheSelectedItAppliesTo() {
        val posts = java.util.Collections.synchronizedList(mutableListOf<String>())
        val followed = java.util.concurrent.atomic.AtomicReference<List<String>>()
        route { r ->
            val path = r.url.encodedPath
            when {
                r.method == "POST" && path.endsWith("/stop") -> {
                    val id = path.split("/")[3]
                    posts += id
                    operation("deployment.stop", "queued", id = "op_$id")
                }
                path.startsWith("/v1/operations/") -> operation("deployment.stop", "succeeded", id = path.substringAfterLast("/"))
                else -> json(twoRunning())
            }
        }
        val vm = DeploymentsViewModel(t.api, onSubmitted = { followed.set(it) }, pollMillis = 10, clock = { fixtureTime })
        visibleList(vm) {
            vm.selectAllLoaded()
            assertEquals(3, vm.state.value.selected.size)
            assertEquals("docs-v2 failed and isn't running", 2, vm.state.value.targets(BulkAction.Stop).size)
            vm.runBulk(BulkAction.Stop)
            assertTrue("selection ends when the action starts", vm.state.value.selected.isEmpty())
            assertEquals("Stopped 2 deployments", vm.state.await { it.message != null }.message)
            assertNull(vm.state.value.bulk)
            assertEquals(setOf("dep_k7m2q9x4abcd", "dep_b2x8n4v6qwer"), posts.toSet())
            assertEquals(setOf("op_dep_k7m2q9x4abcd", "op_dep_b2x8n4v6qwer"), followed.get().toSet())
        }
    }

    @Test fun aBulkActionReportsWhatFailedAndWhy() {
        route { r ->
            val path = r.url.encodedPath
            when {
                r.method == "POST" && path.contains("dep_b2x8n4v6qwer") -> json(fixture("error.json"), code = 409)
                r.method == "POST" -> operation("deployment.stop", "queued")
                path.startsWith("/v1/operations/") -> operation("deployment.stop", "succeeded")
                else -> json(twoRunning())
            }
        }
        val vm = DeploymentsViewModel(t.api, pollMillis = 10, clock = { fixtureTime })
        visibleList(vm) {
            vm.selectAllLoaded()
            vm.runBulk(BulkAction.Stop)
            val run = vm.state.await { it.bulk?.finished == true }.bulk!!
            assertEquals(listOf("fix-cart-total"), run.failures.map { it.first })
            assertEquals("INVALID_STATE", run.failures.single().second.code)
            assertNull("no all-clear when something failed", vm.state.value.message)
            vm.dismissBulk()
            assertNull(vm.state.value.bulk)
        }
    }

    @Test fun aBulkExtensionGivesEachAsMuchAsItMay() {
        val bodies = java.util.concurrent.ConcurrentHashMap<String, String>()
        route { r ->
            val path = r.url.encodedPath
            when {
                r.method == "POST" && path.endsWith("/extend") -> {
                    bodies[path.split("/")[3]] = r.body?.utf8().orEmpty()
                    json(fixture("deployment.json"))
                }
                // fix-cart-total may run only 2 more hours under the 7-day limit.
                else -> json(twoRunning(fixExpires = "2026-11-04T13:00:00Z"))
            }
        }
        val vm = DeploymentsViewModel(t.api, pollMillis = 10, clock = { fixtureTime })
        visibleList(vm) {
            vm.selectAllLoaded()
            vm.runBulk(BulkAction.Extend, 1.days)
            assertEquals("Extended 3 deployments", vm.state.await { it.message != null }.message)
            assertEquals("""{"seconds":86400}""", bodies["dep_k7m2q9x4abcd"])
            assertEquals("""{"seconds":7140}""", bodies["dep_b2x8n4v6qwer"])
            // docs-v2 has no lease yet; its lifetime can still grow to the limit.
            assertEquals("""{"seconds":86400}""", bodies["dep_f5c1z7l0poiu"])
        }
    }

    @Test fun aBulkActionReportsOperationsThatFailedOrEndedUnknown() {
        route { r ->
            val path = r.url.encodedPath
            when {
                r.method == "POST" -> operation("deployment.stop", "queued", id = "op_" + path.split("/")[3])
                path == "/v1/operations/op_dep_k7m2q9x4abcd" -> operation(
                    "deployment.stop", "failed", ""","error":{"code":"INTERRUPTED","message":"the server restarted"}""", id = "op_dep_k7m2q9x4abcd",
                )
                path.startsWith("/v1/operations/") -> operation("deployment.stop", "archived", id = path.substringAfterLast("/"))
                else -> json(twoRunning())
            }
        }
        val vm = DeploymentsViewModel(t.api, pollMillis = 10, clock = { fixtureTime })
        visibleList(vm) {
            vm.selectAllLoaded()
            vm.runBulk(BulkAction.Stop)
            val run = vm.state.await { it.bulk?.finished == true }.bulk!!
            assertEquals(
                mapOf("feat-checkout" to "INTERRUPTED", "fix-cart-total" to "UNKNOWN"),
                run.failures.associate { it.first to it.second.code },
            )
        }
    }

    @Test fun anOperationThatCantBeLookedUpIsGivenUpOn() {
        route { r ->
            val path = r.url.encodedPath
            when {
                r.method == "POST" -> operation("deployment.stop", "queued", id = "op_" + path.split("/")[3])
                // One operation went with its project; the other can't be read.
                path == "/v1/operations/op_dep_k7m2q9x4abcd" -> json("""{"error":{"code":"NOT_FOUND","message":"gone"}}""", code = 404)
                path.startsWith("/v1/operations/") -> json("""{"error":{"code":"FORBIDDEN","message":"token revoked"}}""", code = 403)
                else -> json(twoRunning())
            }
        }
        val vm = DeploymentsViewModel(t.api, pollMillis = 10, clock = { fixtureTime })
        visibleList(vm) {
            vm.selectAllLoaded()
            vm.runBulk(BulkAction.Stop)
            val run = vm.state.await { it.bulk?.finished == true }.bulk!!
            assertEquals(listOf("fix-cart-total"), run.failures.map { it.first })
            assertTrue(run.failures.single().second.message.startsWith("Lost track of it"))
        }
    }

    @Test fun hidingARunStopsFollowingIt() {
        val polls = AtomicInteger()
        route { r ->
            when {
                r.method == "POST" -> operation("deployment.update", "queued")
                r.url.encodedPath.startsWith("/v1/operations/") -> operation("deployment.update", "running").also { polls.incrementAndGet() }
                else -> json(twoRunning())
            }
        }
        val vm = DeploymentsViewModel(t.api, pollMillis = 10, clock = { fixtureTime })
        visibleList(vm) {
            vm.selectAllLoaded()
            vm.runBulk(BulkAction.Update)
            withTimeout(5_000) { while (polls.get() < 3) delay(10) }
            vm.dismissBulk()
            assertNull(vm.state.value.bulk)
            val after = polls.get()
            delay(200)
            assertTrue("following stopped", polls.get() <= after + 2)
            // The bar is free again.
            vm.selectAll()
            assertEquals(3, vm.state.value.targets(BulkAction.Destroy).size)
        }
    }

    @Test fun deploymentsThatDisappearLeaveTheSelection() {
        val gone = AtomicBoolean(false)
        route { _ ->
            val list = twoRunning()
            json(if (gone.get()) list.replace("\"id\": \"dep_f5c1z7l0poiu\"", "\"id\": \"dep_other\"") else list)
        }
        val vm = DeploymentsViewModel(t.api, pollMillis = 10, clock = { fixtureTime })
        visibleList(vm) {
            vm.selectAllLoaded()
            assertTrue("dep_f5c1z7l0poiu" in vm.state.value.selected)
            gone.set(true)
            vm.refresh()
            vm.state.await { "dep_f5c1z7l0poiu" !in it.selected }
            assertEquals(2, vm.state.value.selected.size)
        }
    }

    /** Runs [block] while the list is on screen, which is when bulk operations are followed. */
    private fun visibleList(vm: DeploymentsViewModel, block: suspend CoroutineScope.() -> Unit) = runBlocking {
        val polling = launch(Dispatchers.Default) { vm.poll() }
        try {
            block()
        } finally {
            polling.cancel()
        }
    }

    @Test fun severalBranchesAreDeployedAtOnce() = runBlocking {
        val deployed = java.util.Collections.synchronizedList(mutableListOf<String>())
        val created = AtomicInteger()
        val updated = AtomicBoolean(false)
        val followed = java.util.concurrent.atomic.AtomicReference<List<String>>()
        route { r ->
            when {
                r.url.encodedPath == "/v1/projects" -> json(fixture("projects.json"))
                r.url.encodedPath.endsWith("/branches") -> json(fixture("branches.json"))
                r.method == "POST" && r.url.encodedPath == "/v1/deployments" -> {
                    deployed += r.body!!.utf8()
                    // Each deployment gets its own operation; the requests run in parallel.
                    json(fixture("create_deployment.json").replace("op_2hd7s9k3mnbv", "op_new_${created.incrementAndGet()}"), code = 202)
                }
                r.method == "POST" && r.url.encodedPath.endsWith("/update") -> operation("deployment.update", "queued", id = "op_update").also { updated.set(true) }
                else -> json(fixture("deployments.json"))
            }
        }
        val vm = NewDeploymentViewModel(t.api, clock = { fixtureTime }, onSubmitted = { followed.set(it) })
        vm.state.await { it.branch == "main" }
        vm.pick("main")
        vm.setQuery("feature/x")
        vm.selectBranch("feature/x")
        vm.selectBranch("feat/checkout")
        val chosen = vm.state.value
        assertEquals(listOf("main", "feature/x", "feat/checkout"), chosen.selected)
        assertEquals(
            listOf("storefront-main" to null, "storefront-feature-x" to null, "feat-checkout" to "feat-checkout"),
            chosen.plans.map { it.name to it.update?.name },
        )
        vm.submit()
        assertEquals("Deploying 2 branches and updating 1 preview", vm.state.await { it.finished != null }.finished)
        assertEquals(2, deployed.size)
        assertTrue(deployed.any { "\"ref\":\"feature/x\"" in it && "\"name\":\"storefront-feature-x\"" in it })
        assertTrue(deployed.any { "\"ref\":\"main\"" in it && "\"name\":\"storefront-main\"" in it })
        assertTrue(updated.get())
        assertEquals(setOf("op_new_1", "op_new_2", "op_update"), followed.get().toSet())
    }

    @Test fun leavingWhileDeployingStillFollowsWhatStarted() = runBlocking {
        val followed = java.util.concurrent.atomic.AtomicReference<List<String>>()
        route { r ->
            when {
                r.url.encodedPath == "/v1/projects" -> json(fixture("projects.json"))
                r.url.encodedPath.endsWith("/branches") -> json(fixture("branches.json"))
                r.method == "POST" -> json(fixture("create_deployment.json"), code = 202).newBuilder().headersDelay(300, TimeUnit.MILLISECONDS).build()
                else -> json(fixture("deployments.json"))
            }
        }
        val vm = NewDeploymentViewModel(t.api, clock = { fixtureTime }, onSubmitted = { followed.set(it) })
        vm.state.await { it.branch == "main" }
        vm.pick("main")
        vm.selectBranch("feature/x")
        vm.submit()
        // The screen closes while the requests are on their way.
        vm.viewModelScope.cancel()
        withTimeout(5_000) { while (followed.get() == null) delay(20) }
        assertEquals(2, followed.get().size)
    }

    @Test fun aBranchListArrivingLateKeepsWhatWasChosen() = runBlocking {
        route { r ->
            when {
                r.url.encodedPath == "/v1/projects" -> json(fixture("projects.json"))
                r.url.encodedPath.endsWith("/branches") -> json(fixture("branches.json")).newBuilder().headersDelay(400, TimeUnit.MILLISECONDS).build()
                else -> json(fixture("deployments.json"))
            }
        }
        val vm = NewDeploymentViewModel(t.api, clock = { fixtureTime })
        vm.state.await { it.project != null && it.loadingBranches }
        vm.pick("feature/x")
        val loaded = vm.state.await { !it.loadingBranches }
        assertEquals("the default branch isn't added on top", listOf("feature/x"), loaded.selected)
    }

    @Test fun branchesThatFailStayChosenToTryAgain() = runBlocking {
        route { r ->
            when {
                r.url.encodedPath == "/v1/projects" -> json(fixture("projects.json"))
                r.url.encodedPath.endsWith("/branches") -> json(fixture("branches.json"))
                r.method == "POST" && r.body?.utf8()?.contains("feature/x") == true ->
                    json("""{"error":{"code":"QUOTA_EXCEEDED","message":"10 deployments are live, the most this server allows"}}""", code = 429)
                r.method == "POST" -> json(fixture("create_deployment.json"), code = 202)
                else -> json(fixture("deployments.json"))
            }
        }
        val vm = NewDeploymentViewModel(t.api, clock = { fixtureTime })
        vm.state.await { it.branch == "main" }
        vm.pick("main")
        vm.selectBranch("feature/x")
        vm.submit()
        val after = vm.state.await { it.failures.isNotEmpty() }
        assertEquals(listOf("feature/x"), after.selected)
        assertEquals("QUOTA_EXCEEDED", after.failures.single().second.code)
        assertNull(after.finished)
        assertNull(after.opened)
    }

    @Test fun aNewBuildIsReadAfreshInsteadOfSplicedOntoTheOldOne() = runBlocking {
        val replaced = AtomicBoolean(false)
        route { r ->
            if (r.url.encodedPath == "/v1/deployments/feat-checkout") {
                return@route deployment(operation = if (replaced.get()) "op_new" else "op_old")
            }
            val offset = r.url.queryParameter("offset")
            when {
                !replaced.get() && offset == null -> json("""{"data":{"logs":"#1 load\n#2 npm ci\n","offset":17}}""")
                !replaced.get() && offset == "17" -> json("""{"data":{"logs":"#3 build\n","offset":26}}""")
                !replaced.get() -> json("""{"data":{"logs":"","offset":26}}""")
                offset == null -> json("""{"data":{"logs":"#1 new build\n#2 its second line\n","offset":30}}""")
                offset == "30" -> json("""{"data":{"logs":"","offset":30}}""")
                // Reading the new log from the old offset: what a spliced view would show.
                else -> json("""{"data":{"logs":"spliced\n","offset":40}}""")
            }
        }
        val vm = LogsViewModel(t.api, "feat-checkout", build = true, buildPollMillis = 10)
        val job = launch(Dispatchers.Default) { vm.poll() }
        try {
            vm.state.await { s -> s.lines.map { it.text } == listOf("#1 load", "#2 npm ci", "#3 build") }
            replaced.set(true)
            vm.state.await { s -> s.lines.map { it.text } == listOf("#1 new build", "#2 its second line") }
            delay(100)
            assertFalse(vm.state.value.lines.any { it.text == "spliced" })
        } finally {
            job.cancel()
        }
    }

    @Test fun sharedLogsStaySmallEnoughToHandOver() = runBlocking {
        val long = (1..3_000).joinToString("\n", postfix = "\n") { "line $it " + "x".repeat(60) }
        route { json("""{"data":{"logs":${kotlinx.serialization.json.JsonPrimitive(long)}}}""") }
        val vm = LogsViewModel(t.api, "feat-checkout", build = false, appPollMillis = 10)
        val job = launch(Dispatchers.Default) { vm.poll() }
        try {
            vm.state.await { it.lines.size == 3_000 }
            val text = vm.text()
            assertTrue(text.length <= 100_000)
            assertTrue("starts at a whole line", text.startsWith("line "))
            assertTrue("keeps the newest lines", text.endsWith("line 3000 " + "x".repeat(60)))
        } finally {
            job.cancel()
        }
    }
}
