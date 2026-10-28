package io.github.filstawski.deployer.model

import io.github.filstawski.deployer.api.DeploymentsStatus
import kotlin.time.Duration.Companion.days
import kotlin.time.Duration.Companion.hours
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class NamesTest {
    @Test fun slugsLikeTheServer() {
        // The cases of TestSlug in internal/domain.
        assertEquals("feature-new-login", slug("Feature/New_Login!!", 30))
        assertEquals("abc", slug("abc-def", 4))
    }

    @Test fun suggestsNamesLikeTheCli() {
        assertEquals("shop-feature-login", suggestName("shop", "feature/login", emptySet()))
        assertEquals("shop-feature-login-2", suggestName("shop", "feature/login", setOf("shop-feature-login")))
        assertEquals("shop-feature-login-3", suggestName("shop", "feature/login", setOf("shop-feature-login", "shop-feature-login-2")))
        assertEquals("shop-7d63b83", suggestName("shop", "7d63b83b6f7e029c4be79244204d87c9ef71a460", emptySet()))
        val long = suggestName("shop", "x".repeat(80), setOf(slug("shop-" + "x".repeat(80), 63)))
        assertEquals(63, long.length)
        assertEquals(true, long.endsWith("-2"))
    }

    @Test fun checksNamesLikeTheServer() {
        assertNull(nameProblem("shop-main"))
        assertEquals("api is reserved.", nameProblem("api"))
        listOf("", "-shop", "shop-", "Shop", "shop_main", "a".repeat(64)).forEach { assertEquals(it, true, nameProblem(it) != null) }
    }

    @Test fun offersLifetimesUpToTheMaximum() {
        val options = lifetimeOptions(DeploymentsStatus(live = 0, max = 10, defaultTtlSeconds = 1.days.inWholeSeconds, maxTtlSeconds = 7.days.inWholeSeconds))
        assertEquals(listOf(1.hours, 4.hours, 1.days, 3.days, 7.days), options.map { it.duration })
        assertEquals(listOf(1.days), options.filter { it.isDefault }.map { it.duration })
        assertEquals(listOf(7.days), options.filter { it.isMax }.map { it.duration })
        // An unusual default and maximum are offered too.
        val odd = lifetimeOptions(DeploymentsStatus(0, 10, 2.days.inWholeSeconds, 10.days.inWholeSeconds))
        assertEquals(listOf(1.hours, 4.hours, 1.days, 2.days, 3.days, 7.days, 10.days), odd.map { it.duration })
    }
}
