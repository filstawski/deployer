package io.github.filstawski.deployer.api

/**
 * A failed API call, with the server's stable error code. Network failures
 * use [UNAVAILABLE], like the CLI.
 */
class ApiException(
    val code: String,
    override val message: String,
    val hint: String? = null,
    /** HTTP status, or 0 when no response arrived. */
    val status: Int = 0,
    val requestId: String? = null,
) : Exception(message) {

    constructor(error: ApiError, status: Int, requestId: String?) :
        this(error.code, error.message, error.hint, status, requestId)

    companion object {
        const val INVALID_INPUT = "INVALID_INPUT"
        const val UNAUTHENTICATED = "UNAUTHENTICATED"
        const val FORBIDDEN = "FORBIDDEN"
        const val NOT_FOUND = "NOT_FOUND"
        const val INVALID_STATE = "INVALID_STATE"
        const val OPERATION_IN_PROGRESS = "OPERATION_IN_PROGRESS"
        const val UNAVAILABLE = "UNAVAILABLE"
    }
}
