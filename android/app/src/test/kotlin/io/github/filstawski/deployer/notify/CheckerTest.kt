package io.github.filstawski.deployer.notify

import io.github.filstawski.deployer.api.TestServer
import io.github.filstawski.deployer.api.fixture
import io.github.filstawski.deployer.api.json
import io.github.filstawski.deployer.data.KeyValueStore
import java.time.Instant
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class CheckerTest {
    private val t = TestServer()
    private val store = object : KeyValueStore {
        val map = mutableMapOf<String, String>()
        override fun get(key: String) = map[key]
        override fun put(key: String, value: String?) {
            if (value == null) map.remove(key) else map[key] = value
        }
    }

    @After fun close() = t.close()

    @Test fun remembersBetweenChecksAndTellsWhatChanged() = runBlocking {
        val list = AtomicReference(fixture("deployments.json"))
        t.server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when (request.url.encodedPath) {
                "/v1/server" -> json(fixture("server.json"))
                "/v1/deployments" -> json(list.get())
                else -> json(fixture("operation.json").replace("deployment.update", "deployment.create"))
            }
        }
        val checker = Checker(store)
        val now = Instant.parse("2026-10-28T15:00:00Z")
        val first = checker.check("server-1", t.api, now = now)
        assertTrue("the first check only remembers", first.alerts.isEmpty())

        // fix-cart-total finished building.
        list.set(list.get().replace("\"building\"", "\"running\""))
        val second = Checker(store).check("server-1", t.api, now = now)
        assertEquals(listOf("fix-cart-total"), second.alerts.filterIsInstance<Alert.Ready>().map { it.deployment.name })

        checker.forget("server-1")
        assertTrue(store.map.isEmpty())
    }
}
