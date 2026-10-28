package io.github.filstawski.deployer.api

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class ServerAddressTest {
    @Test fun acceptsWhatPeopleType() {
        val cases = mapOf(
            "https://api.example.com" to "https://api.example.com",
            "api.example.com" to "https://api.example.com",
            "  https://api.example.com/  " to "https://api.example.com",
            "https://api.example.com:8443" to "https://api.example.com:8443",
            "HTTPS://API.Example.com" to "https://api.example.com",
        )
        for ((input, want) in cases) assertEquals(input, want, parseServerAddress(input).display())
    }

    @Test fun rejectsAddressesTheCliRejects() {
        val cases = mapOf(
            "" to "Enter your server's API address",
            "ssh://root@server.example.com" to "SSH connections work only from the CLI",
            "http://api.example.com" to "only sends tokens over HTTPS",
            "https://api.example.com/v1" to "without a path",
            "https://user:pw@api.example.com" to "Enter only the address",
            "https://api.example.com?x=1" to "Enter only the address",
            "ftp://api.example.com" to "isn't a web address",
        )
        for ((input, want) in cases) {
            val e = assertThrows(ApiException::class.java) { parseServerAddress(input) }
            assertEquals(ApiException.INVALID_INPUT, e.code)
            assertTrue("$input: ${e.message}", e.message.contains(want))
        }
    }

    @Test fun debugBuildsMayUsePlainHttpOnlyToTheDevelopmentMachine() {
        assertEquals("http://10.0.2.2:7420", parseServerAddress("http://10.0.2.2:7420", allowPlainHttp = true).display())
        assertEquals("http://localhost:7420", parseServerAddress("http://localhost:7420", allowPlainHttp = true).display())
        val e = assertThrows(ApiException::class.java) { parseServerAddress("http://api.example.com", allowPlainHttp = true) }
        assertTrue(e.message, e.message.contains("only sends tokens over HTTPS"))
    }
}
