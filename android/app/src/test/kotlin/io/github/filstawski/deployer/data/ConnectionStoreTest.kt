package io.github.filstawski.deployer.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ConnectionStoreTest {
    private val values = MemoryStore()
    private val cipher = ReversingCipher()

    @Test fun savesConnectionsWithEncryptedTokens() {
        val store = ConnectionStore(values, cipher)
        val a = store.add("example.com", "https://api.example.com", "dpl_secret")
        assertEquals(a.id, store.activeId.value)
        assertFalse("tokens are not stored in the clear", values.map.values.any { "dpl_secret" in it })

        val reopened = ConnectionStore(values, cipher)
        assertEquals(listOf(a), reopened.connections.value)
        assertEquals(a, reopened.active)
    }

    @Test fun reconnectingReplacesTheToken() {
        val store = ConnectionStore(values, cipher)
        val first = store.add("example.com", "https://api.example.com", "dpl_old")
        store.add("other.dev", "https://api.other.dev", "dpl_other")
        val again = store.add("example.com", "https://api.example.com", "dpl_new")
        assertEquals(first.id, again.id)
        assertEquals(listOf("dpl_new", "dpl_other"), store.connections.value.map { it.token })
        assertEquals(again.id, store.activeId.value)
    }

    @Test fun removingTheActiveServerSwitchesToAnother() {
        val store = ConnectionStore(values, cipher)
        val a = store.add("a", "https://api.a.dev", "dpl_a")
        val b = store.add("b", "https://api.b.dev", "dpl_b")
        store.remove(b.id)
        assertEquals(a.id, store.activeId.value)
        store.remove(a.id)
        assertNull(store.active)
        assertTrue(ConnectionStore(values, cipher).connections.value.isEmpty())
    }

    @Test fun dropsTokensThatCanNoLongerBeDecrypted() {
        ConnectionStore(values, cipher).add("a", "https://api.a.dev", "dpl_a")
        val store = ConnectionStore(values, object : SecretCipher {
            override fun encrypt(plain: String) = plain
            override fun decrypt(sealed: String): String = throw IllegalStateException("key gone")
        })
        assertTrue(store.connections.value.isEmpty())
        assertNull(store.activeId.value)
    }

    private class MemoryStore : KeyValueStore {
        val map = mutableMapOf<String, String>()
        override fun get(key: String) = map[key]
        override fun put(key: String, value: String?) {
            if (value == null) map.remove(key) else map[key] = value
        }
    }

    private class ReversingCipher : SecretCipher {
        override fun encrypt(plain: String) = "sealed:" + plain.reversed()
        override fun decrypt(sealed: String) = sealed.removePrefix("sealed:").reversed()
    }
}
