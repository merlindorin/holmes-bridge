package holmes

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is a non-2xx response from HolmesGPT.
type Error struct {
	Operation  string
	StatusCode int
	Body       string
}

func (e *Error) Error() string {
	body := e.Body
	if len(body) > 512 {
		body = body[:512] + "…"
	}

	return fmt.Sprintf("holmesgpt %s: %d: %s", e.Operation, e.StatusCode, body)
}

// Retryable reports whether the same call could plausibly succeed later.
func (e *Error) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests ||
		e.StatusCode == http.StatusRequestTimeout ||
		e.StatusCode >= http.StatusInternalServerError
}

// IsRetryable reports whether err is worth retrying, whether it came from
// HolmesGPT or from the transport underneath it.
func IsRetryable(err error) bool {
	var holmesErr *Error
	if errors.As(err, &holmesErr) {
		return holmesErr.Retryable()
	}

	// A transport failure — connection refused, timeout — is worth another go.
	return err != nil
}
