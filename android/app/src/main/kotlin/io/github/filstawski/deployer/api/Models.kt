package io.github.filstawski.deployer.api

import java.time.Instant
import java.time.OffsetDateTime
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonElement

// The API's resources, as documented in docs/api.md and defined in
// internal/domain. Field names follow the JSON exactly; the test fixtures in
// internal/domain/testdata/wire keep them in step with the server.

/** Observed lifecycle state of a deployment. */
@Serializable(with = StateSerializer::class)
enum class State(val wire: String) {
    Queued("queued"),
    Building("building"),
    Starting("starting"),
    Running("running"),
    Stopped("stopped"),
    Expired("expired"),
    Failed("failed"),
    Destroyed("destroyed"),

    /** A state added to the server after this app was built. */
    Unknown("unknown");

    /** The deployment has not become ready yet. */
    val inProgress: Boolean get() = this == Queued || this == Building || this == Starting
}

@Serializable(with = OperationStatusSerializer::class)
enum class OperationStatus(val wire: String) {
    Queued("queued"),
    Running("running"),
    Succeeded("succeeded"),
    Failed("failed"),
    Cancelled("cancelled"),
    Unknown("unknown");

    val done: Boolean get() = this == Succeeded || this == Failed || this == Cancelled
}

/** A failure the server explains, as carried in responses and on resources. */
@Serializable
data class ApiError(
    val code: String,
    val message: String,
    val hint: String? = null,
)

@Serializable
data class Deployment(
    val id: String,
    val name: String,
    @SerialName("project_id") val projectId: String,
    val project: String,
    val ref: String,
    val commit: String = "",
    val state: State,
    val error: ApiError? = null,
    val hostname: String,
    val url: String,
    /** Lifetime granted at first readiness. */
    @SerialName("ttl_seconds") val ttlSeconds: Long,
    @SerialName("created_by") val createdBy: String,
    @SerialName("created_at") @Serializable(InstantSerializer::class) val createdAt: Instant,
    @SerialName("updated_at") @Serializable(InstantSerializer::class) val updatedAt: Instant,
    @SerialName("ready_at") @Serializable(InstantSerializer::class) val readyAt: Instant? = null,
    @SerialName("expires_at") @Serializable(InstantSerializer::class) val expiresAt: Instant? = null,
    @SerialName("purge_at") @Serializable(InstantSerializer::class) val purgeAt: Instant? = null,
    @SerialName("operation_id") val operationId: String? = null,
    /** Set once the deployment has been built successfully. */
    val image: String = "",
    val port: Int = 0,
    @SerialName("health_path") val healthPath: String = "",
)

@Serializable
data class Operation(
    val id: String,
    val kind: String,
    val status: OperationStatus,
    @SerialName("deployment_id") val deploymentId: String? = null,
    val error: ApiError? = null,
    /** What a successful operation produced; its shape depends on [kind]. */
    val result: JsonElement? = null,
    @SerialName("created_at") @Serializable(InstantSerializer::class) val createdAt: Instant,
    @SerialName("finished_at") @Serializable(InstantSerializer::class) val finishedAt: Instant? = null,
)

/** The result of a successful deployment.update operation. */
@Serializable
data class UpdateResult(
    @SerialName("previous_commit") val previousCommit: String,
    val commit: String,
    /** False when the ref still pointed at the running commit. */
    val changed: Boolean,
)

@Serializable
data class Token(
    val id: String,
    val name: String,
    val scopes: List<String>,
    val projects: List<String>,
    @SerialName("expires_at") @Serializable(InstantSerializer::class) val expiresAt: Instant? = null,
) {
    /** The role these scopes amount to, or null for a custom set. */
    val role: String?
        get() = when (scopes.toSet()) {
            setOf("admin") -> "admin"
            setOf("server:read", "projects:read", "projects:write", "deployments:read", "deployments:write") -> "deployer"
            setOf("server:read", "projects:read", "deployments:read") -> "viewer"
            else -> null
        }

    /** Whether the token may change deployments, not only read them. */
    val canWrite: Boolean get() = "admin" in scopes || "deployments:write" in scopes
}

@Serializable
data class DeploymentsStatus(
    val live: Int,
    val max: Int,
    @SerialName("default_ttl_seconds") val defaultTtlSeconds: Long,
    @SerialName("max_ttl_seconds") val maxTtlSeconds: Long,
)

@Serializable
data class ServerStatus(
    val version: String,
    @SerialName("api_version") val apiVersion: Int,
    val domain: String,
    @SerialName("preview_url") val previewUrl: String,
    @SerialName("api_url") val apiUrl: String? = null,
    val deployments: DeploymentsStatus,
    val token: Token,
)

@Serializable
data class Logs(
    val logs: String,
    /** For build logs, the byte position to request next to follow the log. */
    val offset: Long = 0,
)

@Serializable
data class Project(
    val id: String,
    val name: String,
    val repository: String,
)

@Serializable
data class Branch(
    val name: String,
    val commit: String,
    /** The repository's default branch. */
    val default: Boolean = false,
)

@Serializable
data class CreateDeploymentRequest(
    /** A project name or ID. */
    val project: String,
    val ref: String,
    /** The preview's subdomain label; the server makes one up when null. */
    val name: String? = null,
    /** The lifetime after readiness; the server default when null. */
    @SerialName("ttl_seconds") val ttlSeconds: Long? = null,
)

@Serializable
data class CreateDeploymentResponse(
    val deployment: Deployment,
    val operation: Operation,
)

@Serializable
internal data class ExtendRequest(val seconds: Long)

/** Decodes RFC 3339 times, which the server sends in UTC. */
object InstantSerializer : KSerializer<Instant> {
    override val descriptor = PrimitiveSerialDescriptor("Instant", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: Instant) = encoder.encodeString(value.toString())
    override fun deserialize(decoder: Decoder): Instant = OffsetDateTime.parse(decoder.decodeString()).toInstant()
}

object StateSerializer : KSerializer<State> {
    override val descriptor = PrimitiveSerialDescriptor("State", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: State) = encoder.encodeString(value.wire)
    override fun deserialize(decoder: Decoder): State {
        val wire = decoder.decodeString()
        return State.entries.firstOrNull { it.wire == wire } ?: State.Unknown
    }
}

object OperationStatusSerializer : KSerializer<OperationStatus> {
    override val descriptor = PrimitiveSerialDescriptor("OperationStatus", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: OperationStatus) = encoder.encodeString(value.wire)
    override fun deserialize(decoder: Decoder): OperationStatus {
        val wire = decoder.decodeString()
        return OperationStatus.entries.firstOrNull { it.wire == wire } ?: OperationStatus.Unknown
    }
}
