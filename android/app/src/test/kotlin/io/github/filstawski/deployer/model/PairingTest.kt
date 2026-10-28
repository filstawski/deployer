package io.github.filstawski.deployer.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PairingTest {
    private val token = "dpl_" + "ab".repeat(32)

    @Test fun readsTheLinkDeployerPairMakes() {
        // As Go's url.Values.Encode writes it.
        val link = "deployer://pair?server=https%3A%2F%2Fapi.example.com&token=$token"
        assertEquals(Pairing("https://api.example.com", token), parsePairingLink(link))
        assertEquals(Pairing("https://api.example.com", token), parsePairingLink("deployer://pair?token=$token&server=https://api.example.com/"))
    }

    @Test fun rejectsAnythingElse() {
        val rejected = listOf(
            "https://api.example.com",
            "deployer://login?server=https%3A%2F%2Fapi.example.com&token=$token",
            "deployer://pair?server=http%3A%2F%2Fapi.example.com&token=$token",
            "deployer://pair?server=https%3A%2F%2Fapi.example.com",
            "deployer://pair?server=https%3A%2F%2Fapi.example.com&token=secret",
            "deployer://pair?server=ssh%3A%2F%2Froot%40server&token=$token",
            "not a link at all",
            "deployer://pair?server=%ZZ&token=$token",
        )
        for (link in rejected) assertNull(link, parsePairingLink(link))
    }

    @Test fun debugBuildsAcceptTheDevelopmentMachine() {
        val link = "deployer://pair?server=http%3A%2F%2F10.0.2.2%3A7420&token=$token"
        assertEquals(Pairing("http://10.0.2.2:7420", token), parsePairingLink(link, allowPlainHttp = true))
    }
}
