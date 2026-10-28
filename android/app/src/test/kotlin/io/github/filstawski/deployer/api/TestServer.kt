package io.github.filstawski.deployer.api

import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer

/** The API's JSON as the Go server writes it; see internal/domain/wire_test.go. */
fun fixture(name: String): String =
    requireNotNull(TestServer::class.java.getResource("/wire/$name")) { "missing fixture $name" }.readText()

fun json(body: String, code: Int = 200): MockResponse =
    MockResponse.Builder().code(code).addHeader("Content-Type", "application/json").body(body).build()

/** A real HTTP server on localhost, and the app's client for it. */
class TestServer : AutoCloseable {
    val server = MockWebServer().apply { start() }
    val http = deployerHttpClient()
    val api = DeployerApi(server.url("/"), "dpl_test", http, "deployer-android/test")

    fun enqueue(vararg responses: MockResponse) = responses.forEach(server::enqueue)

    override fun close() = server.close()
}
