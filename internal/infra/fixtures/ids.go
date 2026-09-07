package fixtures

import (
	"crypto/rand"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// ReferencePrefix is what the mock puts in front of every incident reference.
//
// Deliberately not "INC-". Mock incidents show up in Slack channels, log lines,
// prompts and screenshots, and a reference that reads exactly like a production
// one is an easy thing to act on by mistake. Anything reading "TEST-201" is
// self-evidently not a real incident.
const ReferencePrefix = "TEST-"

// NewID mints an identifier shaped like incident.io's: an uppercase ULID, which
// sorts lexicographically by creation time. The in-memory store's cursor
// pagination depends on that ordering property.
func NewID() string {
	return strings.ToUpper(ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String())
}

// idFor returns the fixture-supplied identifier, or mints one when the fixture
// left it blank. Scenarios only spell out IDs they need to cross-reference.
func idFor(given string) string {
	if given != "" {
		return given
	}

	return NewID()
}
