//nolint:revive // "api" names the API error envelope, which is what this is
package api

import (
	"fmt"
	"net/http"
)

// Type is the machine-readable category incident.io puts on every error
// response. The mock reuses the real vocabulary so clients written against
// production behave identically here.
type Type string

const (
	TypeInvalidRequest    Type = "invalid_request_error"
	TypeAuthentication    Type = "authentication_error"
	TypeResourceForbidden Type = "resource_forbidden"
	TypeNotFound          Type = "not_found"
	TypeMethodNotAllowed  Type = "method_not_allowed"
	TypeConflict          Type = "conflict"
	TypeValidation        Type = "validation_error"
	TypeTooManyRequests   Type = "too_many_requests"
	TypeAPIError          Type = "api_error"
)

// Source points at the request field responsible for a validation failure.
type Source struct {
	Field   string `json:"field"`
	Pointer string `json:"pointer"`
}

// Single is one of possibly many errors that caused a request to fail.
type Single struct {
	Code     string            `json:"code"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Source   *Source           `json:"source,omitempty"`
}

// Error is the response body incident.io returns for any non-2xx, and doubles
// as a Go error so handlers can `c.Error(...)` and let the middleware render it.
type Error struct {
	Type      Type     `json:"type"`
	Status    int      `json:"status"`
	RequestID string   `json:"request_id"`
	Errors    []Single `json:"errors"`
}

func (e *Error) Error() string {
	if len(e.Errors) > 0 {
		return fmt.Sprintf("%s: %s", e.Type, e.Errors[0].Message)
	}

	return string(e.Type)
}

// New builds an error response. RequestID is filled in by the error middleware,
// which is the only place that knows the current request.
func New(status int, t Type, errs ...Single) *Error {
	return &Error{Type: t, Status: status, Errors: errs}
}

// NotFound reports a missing resource, matching incident.io's wording.
func NotFound(resource, id string) *Error {
	return New(http.StatusNotFound, TypeNotFound, Single{
		Code:     "not_found",
		Message:  fmt.Sprintf("Could not find %s with ID %q", resource, id),
		Metadata: map[string]string{"resource": resource, "id": id},
	})
}

// Unauthorized reports a missing or unusable API key.
func Unauthorized(message string) *Error {
	return New(http.StatusUnauthorized, TypeAuthentication, Single{
		Code:    "authentication_error",
		Message: message,
	})
}

// Validation reports a bad field in the request body.
func Validation(field, pointer, message string) *Error {
	return New(http.StatusUnprocessableEntity, TypeValidation, Single{
		Code:    "validation_error",
		Message: message,
		Source:  &Source{Field: field, Pointer: pointer},
	})
}

// BadRequest reports a malformed request that never reached validation.
func BadRequest(message string) *Error {
	return New(http.StatusBadRequest, TypeInvalidRequest, Single{
		Code:    "invalid_request_error",
		Message: message,
	})
}

// Internal reports a fault in the service itself.
func Internal(message string) *Error {
	return New(http.StatusInternalServerError, TypeAPIError, Single{
		Code:    "internal_server_error",
		Message: message,
	})
}
