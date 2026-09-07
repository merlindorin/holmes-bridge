package incidentio

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// APIError is a non-2xx response from incident.io, decoded far enough to be
// actionable in a log line.
type APIError struct {
	Operation  string
	StatusCode int
	Type       string
	Messages   []string
	Body       string
}

func (e *APIError) Error() string {
	if len(e.Messages) > 0 {
		return fmt.Sprintf("%s: %d %s: %v", e.Operation, e.StatusCode, e.Type, e.Messages)
	}

	return fmt.Sprintf("%s: %d: %s", e.Operation, e.StatusCode, truncate(e.Body, 256))
}

// Retryable reports whether trying the same call again could plausibly work.
// Rate limits and server faults are transient; a rejected payload is not.
func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= http.StatusInternalServerError
}

// IsNotFound reports whether err is a 404 from incident.io.
func IsNotFound(err error) bool {
	var apiErr *APIError

	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// IsUnauthorized reports whether err is an authentication failure, which almost
// always means a misconfigured API key rather than a transient fault.
func IsUnauthorized(err error) bool {
	var apiErr *APIError

	return errors.As(err, &apiErr) &&
		(apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

func statusError(operation string, status int, body []byte) error {
	e := &APIError{Operation: operation, StatusCode: status, Body: string(body)}

	var envelope struct {
		Type   string `json:"type"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &envelope); err == nil {
		e.Type = envelope.Type
		for _, single := range envelope.Errors {
			e.Messages = append(e.Messages, single.Message)
		}
	}

	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "…"
}
