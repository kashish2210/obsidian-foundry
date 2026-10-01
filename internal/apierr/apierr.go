// Package apierr defines the error type every layer uses to describe a
// failure that maps to a specific HTTP status and error code.
package apierr

import (
	"fmt"
	"net/http"
)

// Error carries the HTTP status and machine-readable code of a failure.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New builds an Error with a formatted message.
func New(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func Malformed(format string, args ...any) *Error {
	return New(http.StatusBadRequest, "malformed_request", format, args...)
}

func MissingKey() *Error {
	return New(http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
}

func Unauthenticated(format string, args ...any) *Error {
	return New(http.StatusUnauthorized, "unauthenticated", format, args...)
}

func Forbidden(format string, args ...any) *Error {
	return New(http.StatusForbidden, "forbidden", format, args...)
}

func NotFound(format string, args ...any) *Error {
	return New(http.StatusNotFound, "not_found", format, args...)
}

func KeyReuse() *Error {
	return New(http.StatusConflict, "idempotency_key_reuse", "idempotency key was already used with a different request")
}

func Conflict(code, format string, args ...any) *Error {
	return New(http.StatusConflict, code, format, args...)
}

func Invalid(format string, args ...any) *Error {
	return New(http.StatusUnprocessableEntity, "validation_failed", format, args...)
}
