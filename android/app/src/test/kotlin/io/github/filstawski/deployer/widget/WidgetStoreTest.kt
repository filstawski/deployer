package io.github.filstawski.deployer.widget

import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.api.fixture
import io.github.filstawski.deployer.data.KeyValueStore
import io.github.filstawski.deployer.model.deployment
import java.time.Instant
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class WidgetStoreTest {
    private val now = Instant.parse("2026-10-28T15:00:00Z")

    @Test fun showsTheMostUrgentFirst() {
        val list = listOf(
            deployment(now.plusSeconds(90_000)).copy(id = "a", name = "later"),
            deployment(null, state = State.Stopped).copy(id = "b", name = "stopped"),
            deployment(now.plusSeconds(600)).copy(id = "c", name = "soon"),
            deployment(null, state = State.Failed).copy(id = "d", name = "broken"),
            deployment(null, state = State.Building).copy(id = "e", name = "building"),
            deployment(null, state = State.Destroyed).copy(id = "f", name = "gone"),
        )
        assertEquals(listOf("building", "broken", "soon", "later", "stopped"), rows(list).map { it.name })
        assertEquals(2, rows(list, limit = 2).size)
    }

    @Test fun savesOnlyWhatChanged() {
        val values = mutableMapOf<String, String>()
        val store = WidgetStore(object : KeyValueStore {
            override fun get(key: String) = values[key]
            override fun put(key: String, value: String?) {
                if (value == null) values.remove(key) else values[key] = value
            }
        })
        val status = DeployerApi.json
            .decodeFromJsonElement(ServerStatus.serializer(), Json.parseToJsonElement(fixture("server.json")).jsonObject["data"]!!)
        val list = listOf(deployment(now.plusSeconds(600)))
        assertTrue(store.save("example.com", status, list))
        assertFalse(store.save("example.com", status, list))
        assertEquals(3, store.load()?.live)
        assertEquals(10, store.load()?.max)
        store.clear()
        assertEquals(null, store.load())
    }
}
