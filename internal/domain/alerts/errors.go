package alerts

import "errors"

var (
	// ErrNotFound is returned when no alert matches the given identifier.
	ErrNotFound = errors.New("alert not found")
	// ErrAlreadyResolved is returned when resolving an already-resolved alert.
	ErrAlreadyResolved = errors.New("alert is already resolved")
)
