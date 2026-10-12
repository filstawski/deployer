package domain

import (
	"errors"
	"fmt"
)

// Code is a stable, machine-readable error identifier. Clients and agents
// branch on codes, so an existing code must never be renamed or repurposed.
type Code string

// Request errors.
const (
	CodeInvalidInput        Code = "INVALID_INPUT"
	CodeUnauthenticated     Code = "UNAUTHENTICATED"
	CodeForbidden           Code = "FORBIDDEN"
	CodeNotFound            Code = "NOT_FOUND"
	CodeAlreadyExists       Code = "ALREADY_EXISTS"
	CodeNameTaken           Code = "NAME_TAKEN"
	CodeIdempotencyConflict Code = "IDEMPOTENCY_CONFLICT"
	CodeInvalidState        Code = "INVALID_STATE"
	CodeBusy                Code = "OPERATION_IN_PROGRESS"
	CodeQuotaExceeded       Code = "QUOTA_EXCEEDED"
	CodeUnsupported         Code = "UNSUPPORTED"
	CodeUnavailable         Code = "UNAVAILABLE"
	CodeInternal            Code = "INTERNAL"
)

// Operation outcomes, recorded on failed operations and deployments.
const (
	CodeSourceFailed        Code = "SOURCE_FAILED"
	CodeInvalidRecipe       Code = "INVALID_RECIPE"
	CodeBuildFailed         Code = "BUILD_FAILED"
	CodeStartFailed         Code = "START_FAILED"
	CodeHealthcheckFailed   Code = "HEALTHCHECK_FAILED"
	CodeContainerExited     Code = "CONTAINER_EXITED"
	CodeTimeout             Code = "TIMEOUT"
	CodeInterrupted         Code = "INTERRUPTED"
	CodeCancelled           Code = "CANCELLED"
	CodeGitHubNotConfigured Code = "GITHUB_NOT_CONFIGURED"
	CodeGitHubFailed        Code = "GITHUB_FAILED"
)

// Client-only codes, produced by the CLI rather than the server.
const (
	CodeWaitTimeout Code = "WAIT_TIMEOUT"
	CodeClient      Code = "CLIENT_ERROR"
)

// Error is a failure that is safe to show to API clients. Messages never
// contain credentials, repository URLs with secrets, or raw build output.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	// Hint suggests the next action, such as a command to run.
	Hint string `json:"hint,omitempty"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Errorf returns a new *Error with a formatted message.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithHint returns a copy of e with the given hint.
func (e *Error) WithHint(format string, args ...any) *Error {
	c := *e
	c.Hint = fmt.Sprintf(format, args...)
	return &c
}

// AsError returns the *Error in err's chain, if there is one.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// CodeOf returns the code of err, or CodeInternal if err carries none.
func CodeOf(err error) Code {
	if e, ok := AsError(err); ok {
		return e.Code
	}
	return CodeInternal
}
