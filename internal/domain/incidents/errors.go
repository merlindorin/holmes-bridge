package incidents

import "errors"

var (
	// ErrNotFound is returned when no incident matches the given identifier.
	ErrNotFound = errors.New("incident not found")
	// ErrUpdateNotFound is returned when no incident update matches.
	ErrUpdateNotFound = errors.New("incident update not found")
	// ErrTimelineItemNotFound is returned when no timeline item matches.
	ErrTimelineItemNotFound = errors.New("incident timeline item not found")
	// ErrSeverityNotFound is returned when no severity matches.
	ErrSeverityNotFound = errors.New("severity not found")
	// ErrStatusNotFound is returned when no incident status matches.
	ErrStatusNotFound = errors.New("incident status not found")
	// ErrRoleNotFound is returned when no incident role matches.
	ErrRoleNotFound = errors.New("incident role not found")
	// ErrInvalidCursor is returned when an `after` cursor does not decode.
	ErrInvalidCursor = errors.New("invalid pagination cursor")
)

var (
	// ErrActionNotFound is returned when no action matches.
	ErrActionNotFound = errors.New("action not found")
	// ErrFollowUpNotFound is returned when no follow-up matches.
	ErrFollowUpNotFound = errors.New("follow-up not found")
)
