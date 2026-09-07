package catalog

import "errors"

var (
	// ErrTypeNotFound is returned when no catalog type matches.
	ErrTypeNotFound = errors.New("catalog type not found")
	// ErrEntryNotFound is returned when no catalog entry matches.
	ErrEntryNotFound = errors.New("catalog entry not found")
)
