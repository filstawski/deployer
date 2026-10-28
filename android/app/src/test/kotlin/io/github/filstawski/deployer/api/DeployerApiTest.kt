package io.github.filstawski.deployer.api

import java.time.Instant
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.jsonPrimitive
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class DeployerApiTest {
    private val t = TestServer()

    @After fun close() = t.close()

    @Test fun decodesTheServerFixtures() = runBlocking {
        t.enqueue(
            json(fixture("server.json")),
            json(fixture("deployments.json")),
            json(fixture("deployment.json")),
            json(fixture("operation.json")),
            json(fixture("logs.json")),
        )

        val status = t.api.server()
        assertEquals(7L * 86400, status.deployments.maxTtlSeconds)
        assertEquals("deployer", status.token.role)
        assertTrue(status.token.canWrite)

        val list = t.api.deployments()
        assertEquals(listOf(State.Running, State.Building, State.Failed), list.map { it.state })
        assertEquals("HEALTHCHECK_FAILED", list[2].error?.code)
        assertNull(list[1].expiresAt)

        val d = t.api.deployment("feat-checkout")
        assertEquals(Instant.parse("2026-10-29T13:02:00Z"), d.expiresAt)
        assertEquals("/healthz", d.healthPath)
        assertEquals(3000, d.port)

        val op = t.api.operation("op_9kq3w7e2rtyu")
        assertEquals(OperationStatus.Succeeded, op.status)
        val result = DeployerApi.json.decodeFromJsonElement(UpdateResult.serializer(), op.result!!)
        assertTrue(result.changed)

        val logs = t.api.logs("feat-checkout", build = true)
        assertEquals(34L, logs.offset)
    }

    @Test fun errorsCarryTheServersCodeAndHint() = runBlocking {
        t.enqueue(json(fixture("error.json"), code = 409))
        val e = expectFailure { t.api.stop("feat-checkout") }
        assertEquals("INVALID_STATE", e.code)
        assertEquals("start it with: deployer start feat-checkout", e.hint)
        assertEquals(409, e.status)
        assertEquals("req_a1s2d3f4g5hj", e.requestId)
    }

    @Test fun sendsTheTokenAndIdempotencyKeys() = runBlocking {
        t.enqueue(json(fixture("deployments.json")), json(fixture("operation.json")))
        t.api.deployments()
        t.api.stop("feat-checkout", key = "k1")

        val get = t.server.takeRequest()
        assertEquals("Bearer dpl_test", get.headers["Authorization"])
        assertEquals("deployer-android/test", get.headers["User-Agent"])
        assertNull(get.headers["Idempotency-Key"])

        val post = t.server.takeRequest()
        assertEquals("POST", post.method)
        assertEquals("/v1/deployments/feat-checkout/stop", post.url.encodedPath)
        assertEquals("k1", post.headers["Idempotency-Key"])
        assertEquals(0L, post.bodySize)
    }

    @Test fun requestBodiesMatchTheApi() = runBlocking {
        t.enqueue(json(fixture("deployment.json")), json(fixture("operation.json")), json(fixture("operation.json")))
        t.api.extend("feat-checkout", 3600)
        t.api.update("feat-checkout")
        t.api.destroy("feat-checkout")

        val extend = t.server.takeRequest()
        assertEquals("""{"seconds":3600}""", extend.body?.utf8())
        assertNull("extend has no idempotency key in the CLI either", extend.headers["Idempotency-Key"])
        assertEquals("{}", t.server.takeRequest().body?.utf8())
        assertEquals("DELETE", t.server.takeRequest().method)
    }

    @Test fun escapesNamesInPaths() = runBlocking {
        t.enqueue(json(fixture("deployment.json")))
        t.api.deployment("a/b c")
        assertEquals("/v1/deployments/a%2Fb%20c", t.server.takeRequest().url.encodedPath)
    }

    @Test fun retriesGatewayErrorsForSafeRequests() = runBlocking {
        t.enqueue(json("{}", code = 503).newBuilder().body("<html>busy</html>").build(), json(fixture("deployments.json")))
        assertEquals(3, t.api.deployments().size)
        assertEquals(2, t.server.requestCount)
    }

    @Test fun doesNotRepeatWritesWithoutAKey() = runBlocking {
        t.enqueue(json("""{"error":{"code":"UNAVAILABLE","message":"down"}}""", code = 503))
        val e = expectFailure { t.api.extend("feat-checkout", 60) }
        assertEquals("UNAVAILABLE", e.code)
        assertEquals(1, t.server.requestCount)
    }

    @Test fun explainsAnswersFromSomethingElse() = runBlocking {
        t.enqueue(json("<html>not found</html>", code = 404))
        val e = expectFailure { t.api.server() }
        assertEquals(ApiException.UNAVAILABLE, e.code)
        assertTrue(e.message, e.message.contains("did not answer like a Deployer server (HTTP 404)"))
    }

    @Test fun doesNotFollowRedirects() = runBlocking {
        val elsewhere = t.server.url("/elsewhere")
        t.enqueue(mockwebserver3.MockResponse.Builder().code(302).addHeader("Location", elsewhere.toString()).build())
        val e = expectFailure { t.api.server() }
        assertEquals(1, t.server.requestCount)
        assertTrue(e.message, e.message.contains("HTTP 302"))
    }

    @Test fun toleratesStatesAddedLater() = runBlocking {
        val body = fixture("deployment.json").replace("\"running\"", "\"sleeping\"").replace("\"port\"", "\"new_field\": 1, \"port\"")
        t.enqueue(json(body))
        assertEquals(State.Unknown, t.api.deployment("feat-checkout").state)
    }

    @Test fun reportsUnreachableServers() = runBlocking {
        val url = t.server.url("/")
        t.server.close()
        val api = DeployerApi(url, "dpl_test", t.http)
        val e = expectFailure { api.server() }
        assertEquals(ApiException.UNAVAILABLE, e.code)
        assertFalse(e.message.isEmpty())
    }

    private suspend fun expectFailure(block: suspend () -> Unit): ApiException {
        try {
            block()
        } catch (e: ApiException) {
            return e
        }
        fail("expected an ApiException")
        throw AssertionError()
    }
}
