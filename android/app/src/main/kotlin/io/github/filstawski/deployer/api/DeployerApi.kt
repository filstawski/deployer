package io.github.filstawski.deployer.api

import java.io.IOException
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.util.UUID
import javax.net.ssl.SSLException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response

/**
 * A client for one server's /v1 API, the counterpart of the CLI's
 * internal/client. Requests that are safe to repeat (GETs, and writes with an
 * idempotency key) are retried on connection failures and gateway errors.
 */
class DeployerApi(
    val baseUrl: HttpUrl,
    private val token: String,
    private val http: OkHttpClient,
    private val userAgent: String = "deployer-android",
) {
    suspend fun server(): ServerStatus = get(ServerStatus.serializer(), "v1", "server")

    suspend fun projects(): List<Project> = get(ListSerializer(Project.serializer()), "v1", "projects")

    /** The project's branches, read from its repository now; the default branch first. */
    suspend fun branches(project: String): List<Branch> =
        get(ListSerializer(Branch.serializer()), "v1", "projects", project, "branches")

    /** Starts a deployment; its URL is reserved at once, and the operation builds it. */
    suspend fun deploy(request: CreateDeploymentRequest, key: String = newKey()): CreateDeploymentResponse {
        val body = json.encodeToJsonElement(CreateDeploymentRequest.serializer(), request)
        return send(CreateDeploymentResponse.serializer(), "POST", key, body, "v1", "deployments")
    }

    /** Live deployments, newest first. */
    suspend fun deployments(): List<Deployment> = get(ListSerializer(Deployment.serializer()), "v1", "deployments")

    suspend fun deployment(ref: String): Deployment = get(Deployment.serializer(), "v1", "deployments", ref)

    suspend fun operation(id: String): Operation = get(Operation.serializer(), "v1", "operations", id)

    suspend fun stop(ref: String, key: String = newKey()): Operation =
        send(Operation.serializer(), "POST", key, null, "v1", "deployments", ref, "stop")

    suspend fun start(ref: String, key: String = newKey()): Operation =
        send(Operation.serializer(), "POST", key, null, "v1", "deployments", ref, "start")

    /** Rebuilds the deployment from the latest commit of its ref. */
    suspend fun update(ref: String, key: String = newKey()): Operation =
        send(Operation.serializer(), "POST", key, JsonObject(emptyMap()), "v1", "deployments", ref, "update")

    suspend fun destroy(ref: String, key: String = newKey()): Operation =
        send(Operation.serializer(), "DELETE", key, null, "v1", "deployments", ref)

    suspend fun extend(ref: String, seconds: Long): Deployment {
        val body = json.encodeToJsonElement(ExtendRequest.serializer(), ExtendRequest(seconds))
        return send(Deployment.serializer(), "POST", null, body, "v1", "deployments", ref, "extend")
    }

    /**
     * App output, or the build log when [build] is set. For the build log,
     * [offset] reads from a byte position, and the result's offset is where to
     * continue.
     */
    suspend fun logs(ref: String, build: Boolean = false, lines: Int = 0, offset: Long? = null): Logs {
        val url = url("v1", "deployments", ref, "logs").newBuilder()
            .addQueryParameter("source", if (build) "build" else "app")
            .apply {
                if (lines > 0) addQueryParameter("lines", lines.toString())
                if (offset != null) addQueryParameter("offset", offset.toString())
            }
            .build()
        return call(Logs.serializer(), "GET", url, null, null)
    }

    private suspend fun <T> get(serializer: KSerializer<T>, vararg path: String): T =
        call(serializer, "GET", url(*path), null, null)

    private suspend fun <T> send(
        serializer: KSerializer<T>,
        method: String,
        key: String?,
        body: JsonElement?,
        vararg path: String,
    ): T = call(serializer, method, url(*path), key, body)

    private fun url(vararg path: String): HttpUrl =
        baseUrl.newBuilder().apply { path.forEach { addPathSegment(it) } }.build()

    private suspend fun <T> call(
        serializer: KSerializer<T>,
        method: String,
        url: HttpUrl,
        key: String?,
        body: JsonElement?,
    ): T {
        val retryable = method == "GET" || key != null
        var attempt = 0
        while (true) {
            try {
                return once(serializer, method, url, key, body)
            } catch (e: Transient) {
                attempt++
                if (!retryable || attempt == ATTEMPTS) throw e.error
                delay(attempt * 500L)
            }
        }
    }

    private suspend fun <T> once(
        serializer: KSerializer<T>,
        method: String,
        url: HttpUrl,
        key: String?,
        body: JsonElement?,
    ): T {
        val request = Request.Builder()
            .url(url)
            .header("Authorization", "Bearer $token")
            .header("Accept", "application/json")
            .header("User-Agent", userAgent)
            .apply { if (key != null) header("Idempotency-Key", key) }
            .method(method, requestBody(method, body))
            .build()
        val (status, text) = try {
            http.newCall(request).await().use { response ->
                response.code to withContext(Dispatchers.IO) { response.body.string() }
            }
        } catch (e: IOException) {
            throw Transient(unreachable(e))
        }
        val transient = status == 502 || status == 503 || status == 504
        val envelope = try {
            json.parseToJsonElement(text).jsonObject
        } catch (e: Exception) {
            val error = ApiException(
                ApiException.UNAVAILABLE,
                "${baseUrl.host} did not answer like a Deployer server (HTTP $status). Check the address.",
                status = status,
            )
            throw if (transient) Transient(error) else error
        }
        val requestId = envelope["request_id"]?.jsonPrimitive?.content
        val error = envelope["error"]?.takeUnless { it is JsonNull }
        if (error != null) {
            val decoded = try {
                json.decodeFromJsonElement(ApiError.serializer(), error)
            } catch (_: SerializationException) {
                ApiError(ApiException.UNAVAILABLE, "HTTP $status from ${baseUrl.host}.")
            }
            val e = ApiException(decoded, status, requestId)
            throw if (transient) Transient(e) else e
        }
        if (status !in 200..299) {
            val e = ApiException(ApiException.UNAVAILABLE, "Unexpected HTTP $status from ${baseUrl.host}.", status = status)
            throw if (transient) Transient(e) else e
        }
        try {
            return json.decodeFromJsonElement(serializer, envelope["data"] ?: JsonNull)
        } catch (e: SerializationException) {
            throw ApiException(
                ApiException.UNAVAILABLE,
                "The server's response didn't match what this app expects. Update the app or the server.",
                status = status,
                requestId = requestId,
            )
        }
    }

    /** POSTs without parameters, such as stop, are sent with an empty body. */
    private fun requestBody(method: String, body: JsonElement?) = when {
        body != null -> json.encodeToString(JsonElement.serializer(), body).toRequestBody(JSON)
        method == "POST" -> ByteArray(0).toRequestBody()
        else -> null
    }

    private fun unreachable(e: IOException): ApiException {
        val host = baseUrl.host
        val message = when (e) {
            is UnknownHostException -> "Can't find $host. Check the address and your connection."
            is SocketTimeoutException -> "$host did not answer in time."
            is ConnectException -> "Can't connect to $host."
            is SSLException -> "The secure connection to $host failed: ${e.message}"
            else -> "Can't reach $host: ${e.message ?: e.javaClass.simpleName}"
        }
        return ApiException(ApiException.UNAVAILABLE, message)
    }

    /** A failure that may pass if the request is repeated. */
    private class Transient(val error: ApiException) : Exception(error)

    companion object {
        private const val ATTEMPTS = 3
        private val JSON = "application/json".toMediaType()

        val json = Json {
            ignoreUnknownKeys = true
            explicitNulls = false
        }

        fun newKey(): String = "android-" + UUID.randomUUID()
    }
}

/** Runs the call without blocking, and cancels it with the coroutine. */
private suspend fun Call.await(): Response = suspendCancellableCoroutine { cont ->
    cont.invokeOnCancellation { cancel() }
    enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) = cont.resumeWithException(e)
        override fun onResponse(call: Call, response: Response) = cont.resume(response) { _, value, _ -> value.close() }
    })
}
